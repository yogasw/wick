package handlers

import (
	"fmt"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/sessionworkspace"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/pkg/connector"
)

// SessionInstanceFor resolves a connector id + session_id into an
// Execute target when the id is a session-workspace instance. ok=false
// means the id is a normal DB connector and the caller should fall back
// to the row-based path. A session-workspace id with no session_id (or a
// missing instance) is an error — the agent must pass the owning
// session_id, exactly like the rest of the session-scoped tools.
func SessionInstanceFor(layout agentconfig.Layout, args map[string]any, connectorID string) (*connectors.SessionInstanceTarget, bool, error) {
	if !sessionworkspace.IsInstanceID(connectorID) {
		return nil, false, nil
	}
	sid, _ := args["session_id"].(string)
	sid = strings.TrimSpace(sid)
	if sid == "" {
		return nil, false, fmt.Errorf("session_id is required to use the session connector %q", connectorID)
	}
	inst, ok, err := sessionworkspace.Get(layout, sid, connectorID)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, fmt.Errorf("session connector %q not found in this session", connectorID)
	}
	return &connectors.SessionInstanceTarget{
		BaseKey: inst.BaseKey,
		Label:   inst.Label,
		Config:  inst.Config,
	}, true, nil
}

// sessionInstanceStatus mirrors Service.Status for a virtual instance:
// "ready" when every required (non-hidden) base config field has a value
// in the instance config, "needs_setup" otherwise.
func sessionInstanceStatus(mod connector.Module, cfg map[string]string) string {
	for _, sp := range mod.Configs {
		if sp.Hidden || !sp.Required {
			continue
		}
		if strings.TrimSpace(cfg[sp.Key]) == "" {
			return "needs_setup"
		}
	}
	return "ready"
}

// sessionInstanceSummaries renders a session's workspace instances as
// wick_list entries, so they appear alongside real connectors as if they
// were brand-new connectors scoped to this session. Returns the entries
// plus their total tool count. A missing base module (def deleted
// mid-session) drops that instance silently.
func sessionInstanceSummaries(svc *connectors.Service, layout agentconfig.Layout, sessionID string) ([]connectorSummary, int) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, 0
	}
	instances, err := sessionworkspace.List(layout, sessionID)
	if err != nil || len(instances) == 0 {
		return nil, 0
	}
	out := make([]connectorSummary, 0, len(instances))
	total := 0
	for _, in := range instances {
		mod, ok := svc.Module(in.BaseKey)
		if !ok {
			continue
		}
		count := len(mod.Operations)
		total += count
		label := in.Label
		if strings.TrimSpace(label) == "" {
			label = mod.Meta.Name + " (session)"
		}
		out = append(out, connectorSummary{
			ID:          in.ID,
			Connector:   label,
			Description: mod.Meta.Description + " — session-only instance; config lives in this session.",
			TotalTools:  count,
			Status:      sessionInstanceStatus(mod, in.Config),
			Kind:        "session",
		})
	}
	return out, total
}

// sessionInstanceDetail renders one session-workspace instance's op
// schema for wick_get, with tool ids that route back through wick_execute
// (conn:<sw_id>/<opKey>). Returns ok=false when the instance or its base
// module is gone.
func sessionInstanceDetail(svc *connectors.Service, target *connectors.SessionInstanceTarget, instanceID string) (connectorDetail, bool) {
	mod, ok := svc.Module(target.BaseKey)
	if !ok {
		return connectorDetail{}, false
	}
	label := target.Label
	if strings.TrimSpace(label) == "" {
		label = mod.Meta.Name + " (session)"
	}
	tools := make([]toolDetail, 0, len(mod.Operations))
	for i := range mod.Operations {
		op := mod.Operations[i]
		desc := op.Description
		if op.Destructive {
			desc += " ⚠ DESTRUCTIVE: Always confirm with the user before executing this operation."
		}
		tools = append(tools, toolDetail{
			ToolID:      FormatToolID(instanceID, op.Key),
			Name:        op.Name,
			Description: desc,
			Destructive: op.Destructive,
			InputSchema: ConfigsToJSONSchema(op.Input),
		})
	}
	return connectorDetail{
		ID:          instanceID,
		Connector:   label,
		Description: mod.Meta.Description,
		Tools:       tools,
	}, true
}
