package handlers

import (
	"fmt"
	"net/http"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/sessionworkspace"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// SessionInstanceFor resolves a connector id + session_id into an
// Execute target when the id is a session-workspace instance. ok=false
// means the id is a normal DB connector and the caller should fall back
// to the row-based path. A session-workspace id with no session_id (or a
// missing instance) is an error — the agent must pass the owning
// session_id, exactly like the rest of the session-scoped tools.
func SessionInstanceFor(layout agentconfig.Layout, args map[string]any, connectorID string) (*connectors.SessionInstanceTarget, bool, error) {
	sid, _ := args["session_id"].(string)
	return SessionInstanceForID(layout, sid, connectorID)
}

// SessionInstanceForID is SessionInstanceFor with the session id passed
// directly, used by the batch path where each call carries its own
// session_id rather than a shared args map.
func SessionInstanceForID(layout agentconfig.Layout, sessionID, connectorID string) (*connectors.SessionInstanceTarget, bool, error) {
	if !sessionworkspace.IsInstanceID(connectorID) {
		return nil, false, nil
	}
	sid := strings.TrimSpace(sessionID)
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
// "ready" when every required (non-hidden) base config field is satisfied,
// "needs_setup" otherwise. A field counts as satisfied when the instance
// config carries a value OR the base spec ships a non-empty default — the
// runtime falls back to that default (see entity.MapToStruct), and the
// Config-tab form already renders it, so a freshly-added instance whose
// required fields all default must read as ready without a redundant save.
func sessionInstanceStatus(mod connector.Module, cfg map[string]string) string {
	return sessionConfigStatus(mod.Configs, cfg)
}

// sessionConfigStatus is the shared required-fields check used by every
// session-instance surface (MCP list/workspace + the Config-tab UI) so all
// three agree on when an instance is "ready".
func sessionConfigStatus(specs []entity.Config, cfg map[string]string) string {
	for _, sp := range specs {
		if sp.Hidden || !sp.Required {
			continue
		}
		if strings.TrimSpace(cfg[sp.Key]) == "" && strings.TrimSpace(sp.Value) == "" {
			return "needs_setup"
		}
	}
	return "ready"
}

// sessionConfigBases lists the connectors the caller may clone into a
// session workspace: module declares AllowSessionConfig AND a visible
// instance has the per-instance toggle on. Deduped by connector key.
// Surfaced in wick_list so the agent knows the option exists.
func sessionConfigBases(r *http.Request, svc *connectors.Service, tagIDs []string, isAdmin bool) []sessionBaseHint {
	rows, err := svc.ListVisibleTo(r.Context(), tagIDs, isAdmin)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]sessionBaseHint, 0)
	for _, row := range rows {
		if seen[row.Key] {
			continue
		}
		mod, ok := svc.Module(row.Key)
		if !ok || !mod.AllowSessionConfig || !row.AllowSessionConfig {
			continue
		}
		seen[row.Key] = true
		out = append(out, sessionBaseHint{BaseKey: row.Key, Label: mod.Meta.Name})
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
		count := len(mod.AllOps())
		total += count
		label := in.Label
		if strings.TrimSpace(label) == "" {
			label = mod.Meta.Name + " (session)"
		}
		// kind=session + needs_setup_workspace are the load-bearing
		// signals; how to handle them (config via the Session Workspace,
		// not the admin dashboard) lives once in the system prompt, not
		// repeated per entry. Keep the description short.
		status := sessionInstanceStatus(mod, in.Config)
		if status == "needs_setup" {
			status = "needs_setup_workspace"
		}
		out = append(out, connectorSummary{
			ID:          in.ID,
			Connector:   label,
			Description: mod.Meta.Description + " (session connector — this session only)",
			TotalTools:  count,
			Status:      status,
			Kind:        "session",
		})
	}
	return out, total
}

// sessionInstanceSearch matches a session's workspace instances against
// a search needle (label + base name/key + op name/desc), mirroring the
// global wick_search loop so a connector the user spun up for this
// session shows up in results too. needs_setup_workspace instances are
// included (unlike global needs_setup) so the agent can still find + then
// configure them.
func sessionInstanceSearch(svc *connectors.Service, layout agentconfig.Layout, sessionID, needle string) ([]searchGroup, int) {
	instances, err := sessionworkspace.List(layout, sessionID)
	if err != nil || len(instances) == 0 {
		return nil, 0
	}
	groups := make([]searchGroup, 0)
	total := 0
	for _, in := range instances {
		mod, ok := svc.Module(in.BaseKey)
		if !ok {
			continue
		}
		label := in.Label
		if strings.TrimSpace(label) == "" {
			label = mod.Meta.Name + " (session)"
		}
		matched := make([]searchTool, 0)
		ops := mod.AllOps()
		for i := range ops {
			op := ops[i]
			hay := strings.ToLower(label + " " + mod.Meta.Name + " " + mod.Meta.Key + " " + op.Name + " " + op.Description)
			if !strings.Contains(hay, needle) {
				continue
			}
			desc := op.Description
			if op.Destructive {
				desc += " ⚠ DESTRUCTIVE: Always confirm with the user before executing this operation."
			}
			matched = append(matched, searchTool{
				ToolID:      FormatToolID(in.ID, op.Key),
				Name:        op.Name,
				Description: desc,
				Destructive: op.Destructive,
			})
		}
		if len(matched) == 0 {
			continue
		}
		status := sessionInstanceStatus(mod, in.Config)
		if status == "needs_setup" {
			status = "needs_setup_workspace"
		}
		total += len(matched)
		groups = append(groups, searchGroup{
			ID:          in.ID,
			Connector:   label,
			Description: mod.Meta.Description + " (session connector — this session only)",
			Status:      status,
			Tools:       matched,
		})
	}
	return groups, total
}

// sessionInstanceDetail renders one session-workspace instance for wick_get,
// with tool ids that route back through wick_execute (conn:<sw_id>/<opKey>).
// selector follows the same three-level rule as buildConnectorDetail: ""
// lists categories, a category title lists its ops, an op key returns that
// op's schema. Returns ok=false when the instance or its base module is gone;
// err is non-nil for an unknown selector.
func sessionInstanceDetail(svc *connectors.Service, target *connectors.SessionInstanceTarget, instanceID, selector string) (connectorDetail, bool, error) {
	mod, ok := svc.Module(target.BaseKey)
	if !ok {
		return connectorDetail{}, false, nil
	}
	label := target.Label
	if strings.TrimSpace(label) == "" {
		label = mod.Meta.Name + " (session)"
	}
	// Session instances have no per-op disable state — every op is enabled.
	detail, err := buildConnectorDetail(mod, instanceID, label, selector,
		func(opKey string) string { return FormatToolID(instanceID, opKey) },
		func(string) bool { return true })
	if err != nil {
		return connectorDetail{}, true, err
	}
	return detail, true, nil
}
