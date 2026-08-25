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

	// subAgentsKey / SubAgentsPrefix do the same for the sub-agents
	// connector. Delegation ops are the hottest path an agent has — a
	// game or an investigation calls them every turn — and the two-hop
	// dance (wick_get "sub-agents" → wick_execute) is exactly where the
	// lab sessions showed models wandering off to a provider's native
	// agent tools instead. The connector row stays: wick_list discovery,
	// the admin page, tag visibility and per-op audit all still hang off
	// it; these are shortcuts to the same ops, not a second surface.
	subAgentsKey    = "sub-agents"
	SubAgentsPrefix = "wick_agent_"
)

// topLevelConnector describes a connector whose enabled ops also surface
// as top-level MCP tools named <prefix><op_key>.
type topLevelConnector struct {
	key    string
	prefix string
	// withSessionID adds an optional session_id property to each op's
	// schema. Ops that resolve a caller (delegation) need the session;
	// the per-spawn X-Wick-Session-Id header still wins over the arg.
	withSessionID bool
}

var wickManagerTopLevel = topLevelConnector{key: wickManagerKey, prefix: WickManagerPrefix}
var subAgentsTopLevel = topLevelConnector{key: subAgentsKey, prefix: SubAgentsPrefix, withSessionID: true}

// descriptors expands the connector's enabled ops into top-level
// descriptors, gated by row visibility. Returns nil when the connector
// is absent or the caller can't see it.
func (t topLevelConnector) descriptors(ctx context.Context, svc *connectors.Service, tagIDs []string, isAdmin bool) []ToolDescriptor {
	if svc == nil {
		return nil
	}
	rows, err := svc.ListByKey(ctx, t.key)
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
		schema := ConfigsToJSONSchema(op.Input)
		if t.withSessionID {
			schema = withSessionIDProperty(schema)
		}
		out = append(out, ToolDescriptor{
			Name:        t.prefix + op.Key,
			Description: op.Description,
			InputSchema: schema,
		})
	}
	return out
}

// withSessionIDProperty adds an optional session_id to an op schema.
// The properties map is fresh out of ConfigsToJSONSchema (one map per
// call), so adding the key mutates nothing shared.
func withSessionIDProperty(schema JSONSchema) JSONSchema {
	if schema.Properties == nil {
		schema.Properties = map[string]JSONSchemaProperty{}
	}
	schema.Properties["session_id"] = JSONSchemaProperty{
		Type:        "string",
		Description: "Active wick agent session id. Resolved automatically from the transport header inside a wick spawn — pass it only when no header carries it.",
	}
	return schema
}

// execute translates a <prefix><op> call into the canonical wick_execute
// path (tool_id = conn:<id>/<op>), inheriting its visibility check and
// the connector's per-op access gates. When the connector carries
// session_id, the arg is lifted out of params to the top level where
// ResolveCallSession expects it (the transport header still wins).
func (t topLevelConnector) execute(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, name string, params map[string]any, user *entity.User, tagIDs []string) {
	rows, err := svc.ListByKey(r.Context(), t.key)
	if err != nil || len(rows) == 0 {
		rsp.ToolError(w, req.ID, t.key+" connector not available", name)
		return
	}
	op := strings.TrimPrefix(name, t.prefix)
	args := map[string]any{
		"tool_id": FormatToolID(rows[0].ID, op),
		"params":  params,
	}
	if t.withSessionID {
		if sid, ok := params["session_id"].(string); ok {
			delete(params, "session_id")
			args["session_id"] = sid
		}
	}
	WickExecute(w, r, req, rsp, svc, layout, args, user, tagIDs)
}

// WickManagerToolDescriptors expands the wickmanager connector's enabled ops
// into top-level wick_manager_<op> descriptors, gated by row visibility.
func WickManagerToolDescriptors(ctx context.Context, svc *connectors.Service, tagIDs []string, isAdmin bool) []ToolDescriptor {
	return wickManagerTopLevel.descriptors(ctx, svc, tagIDs, isAdmin)
}

// WickManagerExecute translates a wick_manager_<op> call into the canonical
// wick_execute path.
func WickManagerExecute(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, name string, params map[string]any, user *entity.User, tagIDs []string) {
	// Zero layout: wick_manager_* ops manage wick itself — session
	// config overrides don't apply, and no session_id arg is passed.
	wickManagerTopLevel.execute(w, r, req, rsp, svc, agentconfig.Layout{}, name, params, user, tagIDs)
}

// SubAgentsToolDescriptors expands the sub-agents connector's enabled ops
// into top-level wick_agent_<op> descriptors, gated by row visibility.
func SubAgentsToolDescriptors(ctx context.Context, svc *connectors.Service, tagIDs []string, isAdmin bool) []ToolDescriptor {
	return subAgentsTopLevel.descriptors(ctx, svc, tagIDs, isAdmin)
}

// SubAgentsExecute translates a wick_agent_<op> call into the canonical
// wick_execute path. The real layout is passed through because delegation
// ops resolve the calling session.
func SubAgentsExecute(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, name string, params map[string]any, user *entity.User, tagIDs []string) {
	subAgentsTopLevel.execute(w, r, req, rsp, svc, layout, name, params, user, tagIDs)
}
