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
	"github.com/yogasw/wick/pkg/connector"
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
	ToolID      string      `json:"tool_id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Destructive bool        `json:"destructive"`
	Category    string      `json:"category,omitempty"`
	// InputSchema is a pointer so it can be omitted in list mode
	// (wick_get without a category). It is populated only when the caller
	// scopes the request to a single category.
	InputSchema *JSONSchema `json:"input_schema,omitempty"`
}

// categoryDetail is one group entry in the wick_get list-mode response.
type categoryDetail struct {
	Category    string `json:"category"`
	Description string `json:"description"`
	TotalTools  int    `json:"total_tools"`
}

type connectorDetail struct {
	ID          string       `json:"id"`
	Connector   string       `json:"connector"`
	Description string       `json:"description"`
	// Categories lists the op groups when wick_get is called without a
	// category argument (list mode). The caller picks one and re-calls
	// wick_get with category=<title> to get the op schemas.
	Categories []categoryDetail `json:"categories,omitempty"`
	// Tools carries the per-op schemas. In list mode (no category arg) it
	// holds lightweight entries WITHOUT input_schema; in scoped mode (a
	// category was supplied) it holds the full schemas for that category.
	Tools []toolDetail `json:"tools"`
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
	// The drill-down selector accepts a category title or an op key. Accept a
	// few arg names so the LLM can pass whichever reads naturally.
	selector := firstNonEmpty(args, "selector", "category", "op_key")
	// Session-workspace instance: resolve from the session file and render
	// the base module's category list / op list / op schema, no DB row involved.
	if target, ok, err := SessionInstanceFor(layout, args, connectorID); err != nil {
		rsp.ToolError(w, req.ID, err.Error(), connectorID)
		return
	} else if ok {
		detail, found, derr := sessionInstanceDetail(svc, target, connectorID, selector)
		if !found {
			rsp.ToolError(w, req.ID, "session connector base module not registered", connectorID)
			return
		}
		if derr != nil {
			rsp.ToolError(w, req.ID, derr.Error(), connectorID)
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
	toolIDFor := func(opKey string) string {
		if scopedAccountID != "" {
			return FormatToolIDWithAccount(row.ID, opKey, scopedAccountID)
		}
		return FormatToolID(row.ID, opKey)
	}
	detail, err := buildConnectorDetail(mod, row.ID, row.Label, selector, toolIDFor, func(opKey string) bool { return states[opKey] })
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), connectorID)
		return
	}
	rsp.ToolJSON(w, req.ID, detail)
}

// buildConnectorDetail renders a wick_get response across three drill-down
// levels, selected by the `selector` argument. This keeps the LLM's context
// from ballooning: a connector with dozens of ops never dumps every op (let
// alone every schema) in one response — the caller asks for exactly the next
// level it needs.
//
//   - selector == "": CATEGORY LIST. Returns just the category groups (title +
//     description + enabled-op count). No ops, no schemas. The cheapest level.
//     (For a flat connector with no named categories there is nothing to group
//     by, so this falls through to the OP LIST of every op instead.)
//   - selector matches a category title: OP LIST. Returns the lightweight op
//     entries (tool_id, name, description, destructive) in that category. Still
//     no input_schema.
//   - selector matches an op key: OP SCHEMA. Returns that single op with its
//     input_schema — the only level that carries a schema.
//
// A selector that matches neither a category nor an op key is an error.
//
// enabled reports whether an op key is currently enabled for this instance;
// toolIDFor builds the tool_id for an op key (account suffix handled by the
// caller). Sharing this between the DB-row and session-instance paths keeps
// the two from drifting.
func buildConnectorDetail(mod connector.Module, id, label, selector string, toolIDFor func(opKey string) string, enabled func(opKey string) bool) (connectorDetail, error) {
	detail := connectorDetail{ID: id, Connector: label, Description: mod.Meta.Description}
	selector = strings.TrimSpace(selector)

	// OP SCHEMA: selector names an enabled op key → return just that op + schema.
	if selector != "" {
		for _, op := range mod.AllOps() {
			if op.Key != selector {
				continue
			}
			if !enabled(op.Key) {
				return connectorDetail{}, fmt.Errorf("operation %q is disabled on this connector", selector)
			}
			schema := ConfigsToJSONSchema(op.Input)
			detail.Tools = append(detail.Tools, toolDetail{
				ToolID:      toolIDFor(op.Key),
				Name:        op.Name,
				Description: opDescription(op),
				Destructive: op.Destructive,
				Category:    mod.CategoryOf(op.Key),
				InputSchema: &schema,
			})
			return detail, nil
		}
	}

	// OP LIST: selector names a category → list its enabled ops, no schema.
	if selector != "" {
		for i := range mod.Operations {
			if !strings.EqualFold(mod.Operations[i].Title, selector) {
				continue
			}
			cat := mod.Operations[i]
			for _, op := range cat.Ops {
				if !enabled(op.Key) {
					continue
				}
				detail.Tools = append(detail.Tools, toolDetail{
					ToolID:      toolIDFor(op.Key),
					Name:        op.Name,
					Description: opDescription(op),
					Destructive: op.Destructive,
					Category:    cat.Title,
				})
			}
			if len(detail.Tools) == 0 {
				return connectorDetail{}, fmt.Errorf("category %q has no enabled operations", cat.Title)
			}
			return detail, nil
		}
		// Matched neither an op key nor a category.
		return connectorDetail{}, fmt.Errorf("unknown category or operation %q — call wick_get with no selector to list categories, then with a category to list its operations", selector)
	}

	// CATEGORY LIST (selector == ""). Flat connectors (no named categories)
	// have nothing to group by, so fall through to listing every enabled op.
	if !hasNamedCategories(mod) {
		for _, op := range mod.AllOps() {
			if !enabled(op.Key) {
				continue
			}
			detail.Tools = append(detail.Tools, toolDetail{
				ToolID:      toolIDFor(op.Key),
				Name:        op.Name,
				Description: opDescription(op),
				Destructive: op.Destructive,
			})
		}
		return detail, nil
	}
	for _, cat := range mod.Operations {
		count := 0
		for _, op := range cat.Ops {
			if enabled(op.Key) {
				count++
			}
		}
		if count > 0 {
			detail.Categories = append(detail.Categories, categoryDetail{
				Category:    cat.Title,
				Description: cat.Description,
				TotalTools:  count,
			})
		}
	}
	return detail, nil
}

// firstNonEmpty returns the first arg value (by key, in order) that is a
// non-empty trimmed string, or "".
func firstNonEmpty(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k].(string); ok {
			if t := strings.TrimSpace(v); t != "" {
				return t
			}
		}
	}
	return ""
}

// hasNamedCategories reports whether the module groups its ops under at
// least one non-empty category title. False for the flat Cat("", "", …)
// case, where there is nothing to drill into — wick_get lists every op
// directly at the top level.
func hasNamedCategories(mod connector.Module) bool {
	for _, cat := range mod.Operations {
		if strings.TrimSpace(cat.Title) != "" {
			return true
		}
	}
	return false
}

// opDescription appends the destructive warning suffix used across wick_get.
func opDescription(op connector.Operation) string {
	if op.Destructive {
		return op.Description + " ⚠ DESTRUCTIVE: Always confirm with the user before executing this operation."
	}
	return op.Description
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
		RawInput:        rawParams,
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
