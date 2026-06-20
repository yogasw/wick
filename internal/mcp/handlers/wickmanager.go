package handlers

import (
	"context"
	"net/http"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
)

const (
	// wickManagerKey is the connector definition slug whose ops surface as
	// top-level MCP tools (see internal/planning/archive/plan_wickmanager.md).
	wickManagerKey = "wickmanager"
	// WickManagerPrefix marks a tools/call name that targets a wickmanager op.
	WickManagerPrefix = "wick_manager_"
)

// WickManagerToolDescriptors expands the wickmanager connector's enabled ops
// into top-level wick_manager_<op> descriptors, gated by row visibility.
// Returns nil when the connector is absent or the caller can't see it.
func WickManagerToolDescriptors(ctx context.Context, svc *connectors.Service, tagIDs []string, isAdmin bool) []ToolDescriptor {
	if svc == nil {
		return nil
	}
	rows, err := svc.ListByKey(ctx, wickManagerKey)
	if err != nil || len(rows) == 0 {
		return nil
	}
	row := rows[0]
	if ok, verr := svc.IsVisibleTo(ctx, row.ID, tagIDs, isAdmin); verr != nil || !ok {
		return nil
	}
	mod, ok := svc.Module(row.Key)
	if !ok {
		return nil
	}
	states, err := svc.OperationStates(ctx, row.ID, row.Key)
	if err != nil {
		return nil
	}
	out := make([]ToolDescriptor, 0, len(mod.AllOps()))
	for _, op := range mod.AllOps() {
		if !states[op.Key] {
			continue
		}
		out = append(out, ToolDescriptor{
			Name:        WickManagerPrefix + op.Key,
			Description: op.Description,
			InputSchema: ConfigsToJSONSchema(op.Input),
		})
	}
	return out
}

// WickManagerExecute translates a wick_manager_<op> call into the canonical
// wick_execute path (tool_id = conn:<wickmanager_id>/<op>), inheriting its
// visibility check and the connector's per-op access gates.
func WickManagerExecute(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, name string, params map[string]any, user *entity.User, tagIDs []string) {
	rows, err := svc.ListByKey(r.Context(), wickManagerKey)
	if err != nil || len(rows) == 0 {
		rsp.ToolError(w, req.ID, "wickmanager connector not available", name)
		return
	}
	op := strings.TrimPrefix(name, WickManagerPrefix)
	args := map[string]any{
		"tool_id": FormatToolID(rows[0].ID, op),
		"params":  params,
	}
	// Zero layout: wick_manager_* ops manage wick itself — session
	// config overrides don't apply, and no session_id arg is passed.
	WickExecute(w, r, req, rsp, svc, agentconfig.Layout{}, args, user, tagIDs)
}
