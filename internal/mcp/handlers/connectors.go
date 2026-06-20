package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

// connectorSummary is one entry in the wick_list response.
type connectorSummary struct {
	ID          string `json:"id"`
	Connector   string `json:"connector"`
	Description string `json:"description"`
	TotalTools  int    `json:"total_tools"`
	Status      string `json:"status"`
	// Kind is "connector" for a standard instance or "account" for a
	// connected OAuth account entry. Use kind to distinguish bot vs personal.
	Kind     string `json:"kind"`
	// ParentID is the connector row ID when Kind == "account".
	ParentID string `json:"parent_id,omitempty"`
}

type ListResult struct {
	Connectors      []connectorSummary `json:"connectors"`
	TotalConnectors int                `json:"total_connectors"`
	TotalTools      int                `json:"total_tools"`
	// SessionConfigBases lists connectors that CAN be cloned into a
	// per-session instance (capability + admin opt-in) but aren't shown as
	// usable tools until added. Populated only when session_id is passed,
	// so the agent can proactively offer to spin one up. Add via
	// wick_session_workspace action=add base_key=<base_key>.
	SessionConfigBases []sessionBaseHint `json:"session_config_bases,omitempty"`
}

// sessionBaseHint names a connector the caller may clone into the current
// session workspace.
type sessionBaseHint struct {
	BaseKey string `json:"base_key"`
	Label   string `json:"label"`
}

type searchTool struct {
	ToolID      string `json:"tool_id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Destructive bool   `json:"destructive"`
}

type searchGroup struct {
	ID          string       `json:"id"`
	Connector   string       `json:"connector"`
	Description string       `json:"description"`
	Status      string       `json:"status"`
	Tools       []searchTool `json:"tools"`
}

type searchResult struct {
	Connectors []searchGroup `json:"connectors"`
	Total      int           `json:"total"`
	Query      string        `json:"query"`
}

type toolDetail struct {
	ToolID      string     `json:"tool_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Destructive bool       `json:"destructive"`
	InputSchema JSONSchema `json:"input_schema"`
}

type connectorDetail struct {
	ID          string       `json:"id"`
	Connector   string       `json:"connector"`
	Description string       `json:"description"`
	Tools       []toolDetail `json:"tools"`
}

func WickList(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, args map[string]any, tagIDs []string, isAdmin bool) {
	rows, err := svc.ListVisibleTo(r.Context(), tagIDs, isAdmin)
	if err != nil {
		rsp.ToolError(w, req.ID, "list connectors: "+err.Error(), "")
		return
	}
	summaries := make([]connectorSummary, 0, len(rows))
	totalTools := 0
	for _, row := range rows {
		if row.Key == wickManagerKey {
			continue // surfaced as top-level wick_manager_* tools, not via meta-tools
		}
		mod, ok := svc.Module(row.Key)
		if !ok {
			continue
		}
		states, err := svc.OperationStates(r.Context(), row.ID, row.Key)
		if err != nil {
			continue
		}
		count := 0
		for _, op := range mod.AllOps() {
			if states[op.Key] {
				count++
			}
		}
		// Live-catalog modules (custom MCP) at zero ops may simply not
		// have synced yet — run the lazy refresh (throttled) and
		// recount before deciding to hide the connector. Without this,
		// an unsynced connector would never surface: invisible here
		// means no wick_get, and no wick_get means no refresh.
		if count == 0 && mod.Meta.LiveCatalog {
			svc.CatalogRefresh(r.Context(), row.Key, row.ID)
			if fresh, ok2 := svc.Module(row.Key); ok2 {
				mod = fresh
				if states, err = svc.OperationStates(r.Context(), row.ID, row.Key); err != nil {
					continue
				}
				for _, op := range mod.AllOps() {
					if states[op.Key] {
						count++
					}
				}
			}
		}
		if count == 0 {
			continue
		}
		status := svc.Status(row)
		if status == "needs_setup" {
			continue
		}
		totalTools += count
		// Always add the connector entry itself.
		summaries = append(summaries, connectorSummary{
			ID:          row.ID,
			Connector:   row.Label,
			Description: mod.Meta.Description,
			TotalTools:  count,
			Status:      status,
			Kind:        "connector",
		})
		// For OAuth connectors, also add one entry per connected account.
		if mod.OAuth != nil {
			if accs, err2 := svc.ListAccounts(r.Context(), row.ID); err2 == nil {
				for _, acc := range accs {
					summaries = append(summaries, connectorSummary{
						ID:        row.ID + "/" + acc.ID,
						Connector: row.Label + " – @" + acc.DisplayName,
						Description: mod.Meta.Description +
							" (running as @" + acc.DisplayName + ")",
						TotalTools: count,
						Status:     status,
						Kind:       "account",
						ParentID:   row.ID,
					})
				}
			}
		}
	}
	// Session-workspace instances: ephemeral connectors scoped to the
	// caller's session, listed only when a session_id is passed. They
	// appear like brand-new connectors but live and die with the session.
	var sessionBases []sessionBaseHint
	if sid, _ := args["session_id"].(string); strings.TrimSpace(sid) != "" {
		sessSummaries, sessTools := sessionInstanceSummaries(svc, layout, strings.TrimSpace(sid))
		summaries = append(summaries, sessSummaries...)
		totalTools += sessTools
		// Connectors that COULD be cloned per-session — surfaced so the
		// agent knows the option exists without being asked.
		sessionBases = sessionConfigBases(r, svc, tagIDs, isAdmin)
	}

	rsp.ToolJSON(w, req.ID, ListResult{
		Connectors:         summaries,
		TotalConnectors:    len(summaries),
		TotalTools:         totalTools,
		SessionConfigBases: sessionBases,
	})
}

func WickSearch(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, args map[string]any, tagIDs []string, isAdmin bool) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		rsp.ToolError(w, req.ID, "query is required", "")
		return
	}
	rows, err := svc.ListVisibleTo(r.Context(), tagIDs, isAdmin)
	if err != nil {
		rsp.ToolError(w, req.ID, "search: "+err.Error(), "")
		return
	}
	needle := strings.ToLower(query)
	groups := make([]searchGroup, 0)
	total := 0
	for _, row := range rows {
		if row.Key == wickManagerKey {
			continue // surfaced as top-level wick_manager_* tools, not via meta-tools
		}
		mod, ok := svc.Module(row.Key)
		if !ok {
			continue
		}
		states, err := svc.OperationStates(r.Context(), row.ID, row.Key)
		if err != nil {
			continue
		}
		matched := make([]searchTool, 0)
		for _, op := range mod.AllOps() {
			if !states[op.Key] {
				continue
			}
			hay := strings.ToLower(row.Label + " " + op.Name + " " + op.Description)
			if !strings.Contains(hay, needle) {
				continue
			}
			searchDesc := op.Description
			if op.Destructive {
				searchDesc += " ⚠ DESTRUCTIVE: Always confirm with the user before executing this operation."
			}
			matched = append(matched, searchTool{
				ToolID:      FormatToolID(row.ID, op.Key),
				Name:        op.Name,
				Description: searchDesc,
				Destructive: op.Destructive,
			})
		}
		if len(matched) == 0 {
			continue
		}
		status := svc.Status(row)
		if status == "needs_setup" {
			continue
		}
		total += len(matched)
		groups = append(groups, searchGroup{
			ID:          row.ID,
			Connector:   row.Label,
			Description: mod.Meta.Description,
			Status:      status,
			Tools:       matched,
		})
	}
	// Session-workspace instances: matched only when a session_id is
	// passed, same as wick_list. Without this, searching for a connector
	// the user spun up for this session returns nothing.
	if sid, _ := args["session_id"].(string); strings.TrimSpace(sid) != "" {
		sg, st := sessionInstanceSearch(svc, layout, strings.TrimSpace(sid), needle)
		groups = append(groups, sg...)
		total += st
	}

	rsp.ToolJSON(w, req.ID, searchResult{Connectors: groups, Total: total, Query: query})
}

func WickGet(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, args map[string]any, tagIDs []string, isAdmin bool) {
	rawID, _ := args["id"].(string)
	rawID = strings.TrimSpace(rawID)
	// id may be composite "connectorID/accountID" from wick_list account entries.
	connectorID, scopedAccountID, _ := strings.Cut(rawID, "/")
	if connectorID == "" {
		rsp.ToolError(w, req.ID, "id is required", "")
		return
	}
	// Session-workspace instance: resolve from the session file and render
	// the base module's op schema, no DB row involved.
	if target, ok, err := SessionInstanceFor(layout, args, connectorID); err != nil {
		rsp.ToolError(w, req.ID, err.Error(), connectorID)
		return
	} else if ok {
		detail, found := sessionInstanceDetail(svc, target, connectorID)
		if !found {
			rsp.ToolError(w, req.ID, "session connector base module not registered", connectorID)
			return
		}
		rsp.ToolJSON(w, req.ID, detail)
		return
	}
	allowed, err := svc.IsVisibleTo(r.Context(), connectorID, tagIDs, isAdmin)
	if err != nil || !allowed {
		rsp.ToolError(w, req.ID, "connector not found or not accessible", connectorID)
		return
	}
	row, err := svc.Get(r.Context(), connectorID)
	if err != nil {
		rsp.ToolError(w, req.ID, "get connector: "+err.Error(), connectorID)
		return
	}
	// Custom MCP connectors lazily re-sync their live tool catalog here
	// (throttled), so the schemas the LLM reads are near-fresh without
	// wick_list paying a network round-trip per call.
	svc.CatalogRefresh(r.Context(), row.Key, row.ID)
	mod, ok := svc.Module(row.Key)
	if !ok {
		rsp.ToolError(w, req.ID, "connector module not registered", connectorID)
		return
	}
	states, err := svc.OperationStates(r.Context(), row.ID, row.Key)
	if err != nil {
		rsp.ToolError(w, req.ID, "load operation states: "+err.Error(), connectorID)
		return
	}
	ops := mod.AllOps()
	tools := make([]toolDetail, 0, len(ops))
	for _, op := range ops {
		if !states[op.Key] {
			continue
		}
		desc := op.Description
		if op.Destructive {
			desc += " ⚠ DESTRUCTIVE: Always confirm with the user before executing this operation."
		}
		toolID := FormatToolID(row.ID, op.Key)
		if scopedAccountID != "" {
			toolID = FormatToolIDWithAccount(row.ID, op.Key, scopedAccountID)
		}
		tools = append(tools, toolDetail{
			ToolID:      toolID,
			Name:        op.Name,
			Description: desc,
			Destructive: op.Destructive,
			InputSchema: ConfigsToJSONSchema(op.Input),
		})
	}
	rsp.ToolJSON(w, req.ID, connectorDetail{
		ID:          row.ID,
		Connector:   row.Label,
		Description: mod.Meta.Description,
		Tools:       tools,
	})
}

func WickExecute(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, args map[string]any, user *entity.User, tagIDs []string) {
	toolID, _ := args["tool_id"].(string)
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		rsp.ToolError(w, req.ID, "tool_id is required", toolID)
		return
	}
	connectorID, opKey, accountID, err := ParseToolIDFull(toolID)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), toolID)
		return
	}
	// Session-workspace instance: run against the ephemeral instance's own
	// config (no DB row, no tag visibility — the session itself is the
	// authorization scope).
	sessionTarget, isSession, err := SessionInstanceFor(layout, args, connectorID)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), toolID)
		return
	}
	if !isSession {
		allowed, err := svc.IsVisibleTo(r.Context(), connectorID, tagIDs, user.IsAdmin())
		if err != nil || !allowed {
			rsp.ToolError(w, req.ID, "tool_id not found or not accessible", toolID)
			return
		}
	}
	rawParams, _ := args["params"].(map[string]any)
	input := StringifyArgs(rawParams)
	res, execErr := svc.Execute(r.Context(), connectors.ExecuteParams{
		ConnectorID:     connectorID,
		OperationKey:    opKey,
		Input:           input,
		Source:          entity.ConnectorRunSourceMCP,
		UserID:          user.ID,
		IsAdmin:         user.IsAdmin(),
		IPAddress:       ClientIP(r),
		UserAgent:       r.Header.Get("User-Agent"),
		AccountID:       accountID,
		SessionInstance: sessionTarget,
	})
	if execErr != nil {
		body := execErr.Error()
		if res != nil && res.ResponseJSON != "" {
			body = res.ResponseJSON
		}
		rsp.ToolError(w, req.ID, body, toolID)
		return
	}
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: res.ResponseJSON}},
		IsError: false,
	})
}

// FormatToolID produces the opaque tool identifier.
func FormatToolID(connectorID, opKey string) string {
	return "conn:" + connectorID + "/" + opKey
}

// FormatToolIDWithAccount produces a tool identifier scoped to a specific
// connected OAuth account. Format: conn:{rowID}/{opKey}@{accountID}
func FormatToolIDWithAccount(connectorID, opKey, accountID string) string {
	return "conn:" + connectorID + "/" + opKey + "@" + accountID
}

// ParseToolID inverts FormatToolID and FormatToolIDWithAccount.
// Returns connectorID, opKey, accountID (empty when no account suffix).
func ParseToolID(id string) (connectorID, opKey string, err error) {
	const prefix = "conn:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", errors.New("tool_id must start with 'conn:'")
	}
	connectorID, rest, ok := strings.Cut(id[len(prefix):], "/")
	if !ok || connectorID == "" || rest == "" {
		return "", "", errors.New("tool_id must be of the form 'conn:{connector_id}/{op_key}'")
	}
	opKey = rest
	return connectorID, opKey, nil
}

// ParseToolIDFull parses tool_id including optional @accountID suffix.
func ParseToolIDFull(id string) (connectorID, opKey, accountID string, err error) {
	connectorID, opKey, err = ParseToolID(id)
	if err != nil {
		return
	}
	// opKey may carry "@{accountID}" suffix.
	if opKey, accountID, _ = strings.Cut(opKey, "@"); opKey == "" {
		err = errors.New("op_key is empty after stripping account suffix")
	}
	return
}

// StringifyArgs flattens JSON params into string map.
func StringifyArgs(args map[string]any) map[string]string {
	out := make(map[string]string, len(args))
	for k, v := range args {
		out[k] = stringifyOne(v)
	}
	return out
}

func stringifyOne(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

// ClientIP returns the request's resolved client IP.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if first, _, ok := strings.Cut(xff, ","); ok {
			return strings.TrimSpace(first)
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}
