package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

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
}

type ListResult struct {
	Connectors      []connectorSummary `json:"connectors"`
	TotalConnectors int                `json:"total_connectors"`
	TotalTools      int                `json:"total_tools"`
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

func WickList(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, tagIDs []string, isAdmin bool) {
	rows, err := svc.ListVisibleTo(r.Context(), tagIDs, isAdmin)
	if err != nil {
		rsp.ToolError(w, req.ID, "list connectors: "+err.Error(), "")
		return
	}
	summaries := make([]connectorSummary, 0, len(rows))
	totalTools := 0
	for _, row := range rows {
		mod, ok := svc.Module(row.Key)
		if !ok {
			continue
		}
		states, err := svc.OperationStates(r.Context(), row.ID, row.Key)
		if err != nil {
			continue
		}
		count := 0
		for _, op := range mod.Operations {
			if states[op.Key] {
				count++
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
		summaries = append(summaries, connectorSummary{
			ID:          row.ID,
			Connector:   row.Label,
			Description: mod.Meta.Description,
			TotalTools:  count,
			Status:      status,
		})
	}
	rsp.ToolJSON(w, req.ID, ListResult{
		Connectors:      summaries,
		TotalConnectors: len(summaries),
		TotalTools:      totalTools,
	})
}

func WickSearch(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, args map[string]any, tagIDs []string, isAdmin bool) {
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
		mod, ok := svc.Module(row.Key)
		if !ok {
			continue
		}
		states, err := svc.OperationStates(r.Context(), row.ID, row.Key)
		if err != nil {
			continue
		}
		matched := make([]searchTool, 0)
		for _, op := range mod.Operations {
			if !states[op.Key] {
				continue
			}
			hay := strings.ToLower(row.Label + " " + op.Name + " " + op.Description)
			if !strings.Contains(hay, needle) {
				continue
			}
			matched = append(matched, searchTool{
				ToolID:      FormatToolID(row.ID, op.Key),
				Name:        op.Name,
				Description: op.Description,
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
	rsp.ToolJSON(w, req.ID, searchResult{Connectors: groups, Total: total, Query: query})
}

func WickGet(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, args map[string]any, tagIDs []string, isAdmin bool) {
	connectorID, _ := args["id"].(string)
	connectorID = strings.TrimSpace(connectorID)
	if connectorID == "" {
		rsp.ToolError(w, req.ID, "id is required", "")
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
	tools := make([]toolDetail, 0, len(mod.Operations))
	for _, op := range mod.Operations {
		if !states[op.Key] {
			continue
		}
		tools = append(tools, toolDetail{
			ToolID:      FormatToolID(row.ID, op.Key),
			Name:        op.Name,
			Description: op.Description,
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

func WickExecute(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, args map[string]any, user *entity.User, tagIDs []string) {
	toolID, _ := args["tool_id"].(string)
	toolID = strings.TrimSpace(toolID)
	if toolID == "" {
		rsp.ToolError(w, req.ID, "tool_id is required", toolID)
		return
	}
	connectorID, opKey, err := ParseToolID(toolID)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), toolID)
		return
	}
	allowed, err := svc.IsVisibleTo(r.Context(), connectorID, tagIDs, user.IsAdmin())
	if err != nil || !allowed {
		rsp.ToolError(w, req.ID, "tool_id not found or not accessible", toolID)
		return
	}
	rawParams, _ := args["params"].(map[string]any)
	input := StringifyArgs(rawParams)
	res, execErr := svc.Execute(r.Context(), connectors.ExecuteParams{
		ConnectorID:  connectorID,
		OperationKey: opKey,
		Input:        input,
		Source:       entity.ConnectorRunSourceMCP,
		UserID:       user.ID,
		IsAdmin:      user.IsAdmin(),
		IPAddress:    ClientIP(r),
		UserAgent:    r.Header.Get("User-Agent"),
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

// ParseToolID inverts FormatToolID.
func ParseToolID(id string) (connectorID, opKey string, err error) {
	const prefix = "conn:"
	if !strings.HasPrefix(id, prefix) {
		return "", "", errors.New("tool_id must start with 'conn:'")
	}
	connectorID, opKey, ok := strings.Cut(id[len(prefix):], "/")
	if !ok || connectorID == "" || opKey == "" {
		return "", "", errors.New("tool_id must be of the form 'conn:{connector_id}/{op_key}'")
	}
	return connectorID, opKey, nil
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
