package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/askuser"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/sessionworkspace"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
)

const sessionWorkspaceToolName = "wick_session_workspace"

// WickSessionWorkspace handles the wick_session_workspace tool — the
// agent-facing surface for a session's ephemeral connector instances.
//
// A session instance is a throwaway clone of a base connector module
// (e.g. httprest): it has its own id, label, and config, lives in the
// session dir, shows up in wick_list/wick_get/wick_execute only for this
// session, and is purged when the session ends. This lets the agent spin
// up "a connector pointed at staging" or "a second API key" on the fly
// without ever touching the saved connector rows.
//
// The agent NEVER sees config values: it can create blank instances and
// open a fill modal for the user (configure), but the user types the
// values, secrets are master-encrypted server-side, and only the key
// names ever come back. Config always comes from the human.
//
// Actions:
//   - list:      session instances + the base connectors available to add
//   - add:       create a blank instance from a base connector key
//                (optionally pop the fill modal for the user right away)
//   - duplicate: copy an existing session instance (config and all)
//   - configure:  open the fill modal so the user edits an instance's config
//   - set_config: write config values directly (no modal) — for transports
//                 with no UI (e.g. Slack) and automations. Secrets should be
//                 passed as enc tokens so the plaintext never reaches the agent.
//   - test:       verify setup — base HealthCheck, or run a named operation
//   - remove:     delete a session instance
func WickSessionWorkspace(
	w http.ResponseWriter,
	r *http.Request,
	req RPCRequest,
	rsp Responder,
	svc *connectors.Service,
	layout agentconfig.Layout,
	asks askuser.Asker,
	askAllowed func(sessionID string) (bool, string),
	args map[string]any,
	user *entity.User,
	tagIDs []string,
) {
	action := strings.TrimSpace(argString(args, "action"))
	sessionID := strings.TrimSpace(argString(args, "session_id"))
	if sessionID == "" {
		rsp.ToolError(w, req.ID, "session_id is required", sessionWorkspaceToolName)
		return
	}
	// Validate the session id refers to a real session (and reject
	// path-mangling ids — no meta.json → error).
	if _, err := session.Load(layout, sessionID); err != nil {
		rsp.ToolError(w, req.ID, "load session: "+err.Error(), sessionWorkspaceToolName)
		return
	}

	switch action {
	case "list":
		sessionWorkspaceList(w, req, rsp, svc, layout, sessionID, r, tagIDs, user.IsAdmin())
	case "add":
		sessionWorkspaceAdd(w, r, req, rsp, svc, layout, asks, askAllowed, sessionID, args, tagIDs, user.IsAdmin())
	case "duplicate":
		sessionWorkspaceDuplicate(w, req, rsp, svc, layout, sessionID, args)
	case "configure":
		sessionWorkspaceConfigure(w, r, req, rsp, svc, layout, asks, askAllowed, sessionID, args)
	case "set_config":
		sessionWorkspaceSetConfig(w, req, rsp, svc, layout, sessionID, args)
	case "test":
		sessionWorkspaceTest(w, r, req, rsp, svc, layout, sessionID, args, user)
	case "remove":
		sessionWorkspaceRemove(w, req, rsp, layout, sessionID, args)
	default:
		rsp.ToolError(w, req.ID, "action must be one of: list, add, duplicate, configure, set_config, test, remove", sessionWorkspaceToolName)
	}
}

// baseConnector is one connector module the user is allowed to clone into
// a session workspace (capability declared + an admin enabled the toggle
// on at least one visible instance).
type baseConnector struct {
	Key   string `json:"base_key"`
	Label string `json:"label"`
}

// sessionWorkspaceBases returns the base connector keys the caller may
// add as session instances: module capability AllowSessionConfig AND a
// visible connector row with the per-instance toggle on.
func sessionWorkspaceBases(r *http.Request, svc *connectors.Service, tagIDs []string, isAdmin bool) []baseConnector {
	rows, err := svc.ListVisibleTo(r.Context(), tagIDs, isAdmin)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]baseConnector, 0)
	for _, row := range rows {
		if seen[row.Key] {
			continue
		}
		mod, ok := svc.Module(row.Key)
		if !ok || !mod.AllowSessionConfig || !row.AllowSessionConfig {
			continue
		}
		seen[row.Key] = true
		out = append(out, baseConnector{Key: row.Key, Label: mod.Meta.Name})
	}
	return out
}

func sessionWorkspaceBaseAllowed(r *http.Request, svc *connectors.Service, tagIDs []string, isAdmin bool, key string) bool {
	for _, b := range sessionWorkspaceBases(r, svc, tagIDs, isAdmin) {
		if b.Key == key {
			return true
		}
	}
	return false
}

// instanceVM is one session instance in the list/add/configure response.
// Config values are NEVER included — only which keys are still missing.
type instanceVM struct {
	ID       string   `json:"id"`
	BaseKey  string   `json:"base_key"`
	Label    string   `json:"label"`
	Status   string   `json:"status"` // ready | needs_setup
	Missing  []string `json:"missing_keys,omitempty"`
}

func sessionWorkspaceVM(svc *connectors.Service, in sessionworkspace.Instance) instanceVM {
	vm := instanceVM{ID: in.ID, BaseKey: in.BaseKey, Label: in.Label, Status: "needs_setup"}
	mod, ok := svc.Module(in.BaseKey)
	if !ok {
		return vm
	}
	// A required field is "missing" only when the instance config is empty
	// AND the base spec ships no default — same rule as sessionConfigStatus,
	// so a defaulted required field never blocks a fresh instance.
	for _, sp := range mod.Configs {
		if sp.Hidden || !sp.Required {
			continue
		}
		if strings.TrimSpace(in.Config[sp.Key]) == "" && strings.TrimSpace(sp.Value) == "" {
			vm.Missing = append(vm.Missing, sp.Key)
		}
	}
	if len(vm.Missing) == 0 {
		vm.Status = "ready"
	}
	return vm
}

func sessionWorkspaceList(w http.ResponseWriter, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, sessionID string, r *http.Request, tagIDs []string, isAdmin bool) {
	instances, err := sessionworkspace.List(layout, sessionID)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionWorkspaceToolName)
		return
	}
	vms := make([]instanceVM, 0, len(instances))
	for _, in := range instances {
		vms = append(vms, sessionWorkspaceVM(svc, in))
	}
	writeWorkspaceResult(w, req, rsp, map[string]any{
		"session_id":      sessionID,
		"instances":       vms,
		"available_bases": sessionWorkspaceBases(r, svc, tagIDs, isAdmin),
	})
}

func sessionWorkspaceAdd(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, asks askuser.Asker, askAllowed func(string) (bool, string), sessionID string, args map[string]any, tagIDs []string, isAdmin bool) {
	baseKey := strings.TrimSpace(argString(args, "base_key"))
	if baseKey == "" {
		rsp.ToolError(w, req.ID, "base_key is required (call action=list to see available_bases)", sessionWorkspaceToolName)
		return
	}
	if !sessionWorkspaceBaseAllowed(r, svc, tagIDs, isAdmin, baseKey) {
		rsp.ToolError(w, req.ID, fmt.Sprintf("connector %q is not available for session instances — its module must declare AllowSessionConfig and an admin must enable the per-instance toggle", baseKey), sessionWorkspaceToolName)
		return
	}
	mod, _ := svc.Module(baseKey)
	label := strings.TrimSpace(argString(args, "label"))
	if label == "" {
		label = mod.Meta.Name + " (session)"
	}
	inst, err := sessionworkspace.Add(layout, sessionID, sessionworkspace.Instance{
		BaseKey:   baseKey,
		Label:     label,
		CreatedBy: "ai",
	})
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionWorkspaceToolName)
		return
	}

	// prompt defaults to true: pop the fill modal so the user supplies the
	// config right away. The agent stays blind to the values.
	prompt := true
	if v, ok := args["prompt"].(bool); ok {
		prompt = v
	}
	applied := []string{}
	if prompt && asks != nil {
		if ok, _ := askAllowedOK(askAllowed, sessionID); ok {
			applied, _ = openConfigModal(r, svc, layout, asks, sessionID, inst, args)
		}
	}
	reloaded, _, _ := sessionworkspace.Get(layout, sessionID, inst.ID)
	writeWorkspaceResult(w, req, rsp, map[string]any{
		"session_id":  sessionID,
		"instance":    sessionWorkspaceVM(svc, reloaded),
		"applied":     applied,
		"note":        "Session instance created. It now appears in wick_list (pass this session_id). Tell the user it was added and that they can edit its config in the session Config tab; you can also call action=configure to reopen the fill modal.",
	})
}

func sessionWorkspaceDuplicate(w http.ResponseWriter, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, sessionID string, args map[string]any) {
	src := strings.TrimSpace(argString(args, "connector_id"))
	if src == "" {
		rsp.ToolError(w, req.ID, "connector_id is required (the session instance to duplicate)", sessionWorkspaceToolName)
		return
	}
	orig, ok, err := sessionworkspace.Get(layout, sessionID, src)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionWorkspaceToolName)
		return
	}
	if !ok {
		rsp.ToolError(w, req.ID, fmt.Sprintf("session instance %q not found", src), sessionWorkspaceToolName)
		return
	}
	label := strings.TrimSpace(argString(args, "label"))
	if label == "" {
		label = orig.Label + " (copy)"
	}
	cfg := make(map[string]string, len(orig.Config))
	for k, v := range orig.Config {
		cfg[k] = v
	}
	inst, err := sessionworkspace.Add(layout, sessionID, sessionworkspace.Instance{
		BaseKey:   orig.BaseKey,
		Label:     label,
		Config:    cfg,
		CreatedBy: "ai",
	})
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionWorkspaceToolName)
		return
	}
	writeWorkspaceResult(w, req, rsp, map[string]any{
		"session_id": sessionID,
		"instance":   sessionWorkspaceVM(svc, inst),
		"note":       "Duplicated. The copy has its own id; edit its config independently via action=configure or the Config tab.",
	})
}

func sessionWorkspaceConfigure(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, asks askuser.Asker, askAllowed func(string) (bool, string), sessionID string, args map[string]any) {
	cid := strings.TrimSpace(argString(args, "connector_id"))
	if cid == "" {
		rsp.ToolError(w, req.ID, "connector_id is required (the session instance to configure)", sessionWorkspaceToolName)
		return
	}
	inst, ok, err := sessionworkspace.Get(layout, sessionID, cid)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionWorkspaceToolName)
		return
	}
	if !ok {
		rsp.ToolError(w, req.ID, fmt.Sprintf("session instance %q not found", cid), sessionWorkspaceToolName)
		return
	}
	if asks == nil {
		rsp.ToolError(w, req.ID, "configure needs the agents UI (modal) which is not available on this transport — ask the user to edit it in the session Config tab instead", sessionWorkspaceToolName)
		return
	}
	if ok, reason := askAllowedOK(askAllowed, sessionID); !ok {
		msg := "configure modal blocked by policy"
		if strings.TrimSpace(reason) != "" {
			msg += " (" + reason + ")"
		}
		msg += " — ask the user to edit the instance in the session Config tab instead."
		rsp.ToolError(w, req.ID, msg, sessionWorkspaceToolName)
		return
	}
	applied, err := openConfigModal(r, svc, layout, asks, sessionID, inst, args)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionWorkspaceToolName)
		return
	}
	reloaded, _, _ := sessionworkspace.Get(layout, sessionID, cid)
	writeWorkspaceResult(w, req, rsp, map[string]any{
		"session_id": sessionID,
		"instance":   sessionWorkspaceVM(svc, reloaded),
		"applied":    applied,
	})
}

// sessionWorkspaceSetConfig writes config values onto a session instance
// without a modal — the path for UI-less transports (Slack) and
// automations. Values arrive in the `values` arg (a map). Secret fields
// SHOULD be passed as enc tokens (wick_cenc_ / wick_enc_) so the plaintext
// never flows through the agent; a plaintext secret is master-encrypted
// server-side as a fallback. Unknown keys are rejected so a typo can't
// silently no-op.
func sessionWorkspaceSetConfig(w http.ResponseWriter, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, sessionID string, args map[string]any) {
	cid := strings.TrimSpace(argString(args, "connector_id"))
	if cid == "" {
		rsp.ToolError(w, req.ID, "connector_id is required (the session instance to configure)", sessionWorkspaceToolName)
		return
	}
	rawValues, ok := args["values"].(map[string]any)
	if !ok || len(rawValues) == 0 {
		rsp.ToolError(w, req.ID, "values is required — a map of config key to value (pass secrets as wick_cenc_/wick_enc_ tokens)", sessionWorkspaceToolName)
		return
	}
	inst, ok, err := sessionworkspace.Get(layout, sessionID, cid)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionWorkspaceToolName)
		return
	}
	if !ok {
		rsp.ToolError(w, req.ID, fmt.Sprintf("session instance %q not found", cid), sessionWorkspaceToolName)
		return
	}
	mod, ok := svc.Module(inst.BaseKey)
	if !ok {
		rsp.ToolError(w, req.ID, fmt.Sprintf("base module %q not registered", inst.BaseKey), sessionWorkspaceToolName)
		return
	}
	specByKey := make(map[string]entity.Config, len(mod.Configs))
	for _, sp := range mod.Configs {
		specByKey[sp.Key] = sp
	}
	// Reject unknown keys up front — a typo shouldn't silently no-op.
	values := StringifyArgs(rawValues)
	for k := range values {
		if _, known := specByKey[k]; !known {
			rsp.ToolError(w, req.ID, "unknown config key: "+k, sessionWorkspaceToolName)
			return
		}
	}

	applied, err := storeSessionConfig(svc, layout, sessionID, inst.ID, specByKey, values)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionWorkspaceToolName)
		return
	}
	reloaded, _, _ := sessionworkspace.Get(layout, sessionID, cid)
	writeWorkspaceResult(w, req, rsp, map[string]any{
		"session_id": sessionID,
		"instance":   sessionWorkspaceVM(svc, reloaded),
		"applied":    applied,
		"note":       "Config written. Run action=test to confirm setup before relying on it.",
	})
}

func sessionWorkspaceTest(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, svc *connectors.Service, layout agentconfig.Layout, sessionID string, args map[string]any, user *entity.User) {
	cid := strings.TrimSpace(argString(args, "connector_id"))
	if cid == "" {
		rsp.ToolError(w, req.ID, "connector_id is required (the session instance to test)", sessionWorkspaceToolName)
		return
	}
	inst, ok, err := sessionworkspace.Get(layout, sessionID, cid)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionWorkspaceToolName)
		return
	}
	if !ok {
		rsp.ToolError(w, req.ID, fmt.Sprintf("session instance %q not found", cid), sessionWorkspaceToolName)
		return
	}

	// Explicit op test: run a real operation as the probe.
	if opKey := strings.TrimSpace(argString(args, "operation")); opKey != "" {
		rawParams, _ := args["params"].(map[string]any)
		res, execErr := svc.Execute(r.Context(), connectors.ExecuteParams{
			ConnectorID:  inst.ID,
			OperationKey: opKey,
			Input:        StringifyArgs(rawParams),
			Source:       entity.ConnectorRunSourceTest,
			UserID:       user.ID,
			IsAdmin:      user.IsAdmin(),
			IPAddress:    ClientIP(r),
			UserAgent:    r.Header.Get("User-Agent"),
			SessionInstance: &connectors.SessionInstanceTarget{
				BaseKey: inst.BaseKey,
				Label:   inst.Label,
				Config:  inst.Config,
			},
		})
		out := map[string]any{"session_id": sessionID, "connector_id": cid, "operation": opKey}
		if execErr != nil {
			out["ok"] = false
			out["error"] = execErr.Error()
		} else {
			out["ok"] = true
			out["response"] = json.RawMessage(res.ResponseJSON)
		}
		writeWorkspaceResult(w, req, rsp, out)
		return
	}

	// Default: base module HealthCheck.
	report, err := svc.HealthCheckSessionInstance(r.Context(), inst.BaseKey, inst.ID, inst.Config)
	if err != nil {
		if err == connectors.ErrNoHealthCheck {
			writeWorkspaceResult(w, req, rsp, map[string]any{
				"session_id":   sessionID,
				"connector_id": cid,
				"ok":           false,
				"note":         "This connector has no health check. Pass an operation (+ params) to run a real call as the test instead.",
			})
			return
		}
		writeWorkspaceResult(w, req, rsp, map[string]any{
			"session_id": sessionID, "connector_id": cid, "ok": false, "error": err.Error(),
		})
		return
	}
	allOK := true
	for _, h := range report {
		if !h.OK {
			allOK = false
			break
		}
	}
	writeWorkspaceResult(w, req, rsp, map[string]any{
		"session_id": sessionID, "connector_id": cid, "ok": allOK, "report": report,
	})
}

func sessionWorkspaceRemove(w http.ResponseWriter, req RPCRequest, rsp Responder, layout agentconfig.Layout, sessionID string, args map[string]any) {
	cid := strings.TrimSpace(argString(args, "connector_id"))
	if cid == "" {
		rsp.ToolError(w, req.ID, "connector_id is required (the session instance to remove)", sessionWorkspaceToolName)
		return
	}
	removed, err := sessionworkspace.Remove(layout, sessionID, cid)
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), sessionWorkspaceToolName)
		return
	}
	writeWorkspaceResult(w, req, rsp, map[string]any{
		"session_id": sessionID, "connector_id": cid, "removed": removed,
	})
}

// openConfigModal builds a fill form from the base module's config schema
// (prefilled with non-secret current values), shows it to the user, then
// master-encrypts any secret the user typed and stores it on the
// instance. Returns the keys actually applied. The agent never sees the
// values — only the key names flow back.
func openConfigModal(r *http.Request, svc *connectors.Service, layout agentconfig.Layout, asks askuser.Asker, sessionID string, inst sessionworkspace.Instance, args map[string]any) ([]string, error) {
	mod, ok := svc.Module(inst.BaseKey)
	if !ok {
		return nil, fmt.Errorf("base module %q not registered", inst.BaseKey)
	}

	// Optional subset of keys to ask for; default = every config field.
	wanted := map[string]bool{}
	if rawKeys, ok := args["keys"].([]any); ok {
		for _, k := range rawKeys {
			if s, ok := k.(string); ok && strings.TrimSpace(s) != "" {
				wanted[strings.TrimSpace(s)] = true
			}
		}
	}

	specByKey := make(map[string]entity.Config, len(mod.Configs))
	fields := make([]askuser.Field, 0, len(mod.Configs))
	for _, sp := range mod.Configs {
		if sp.Hidden {
			continue
		}
		if len(wanted) > 0 && !wanted[sp.Key] {
			continue
		}
		specByKey[sp.Key] = sp
		f := askuser.Field{
			Key:      sp.Key,
			Label:    sp.Key,
			Help:     sp.Description,
			Required: false, // empty = keep current
		}
		switch {
		case sp.IsSecret:
			f.Type = "secret"
			f.Placeholder = "Leave empty to keep current"
		case sp.Type == "dropdown" && sp.Options != "":
			f.Type = "dropdown"
			for _, o := range strings.Split(sp.Options, "|") {
				f.Options = append(f.Options, askuser.Option{Label: o, Value: o})
			}
			f.Value = workspaceOverrideOr(inst.Config, sp.Key, sp.Value)
		default:
			f.Type = "text"
			f.Value = workspaceOverrideOr(inst.Config, sp.Key, sp.Value)
		}
		fields = append(fields, f)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("no config fields to fill for this connector")
	}

	question := strings.TrimSpace(argString(args, "reason"))
	if question == "" {
		question = "Configure this session connector"
	}
	question = inst.Label + " — " + question

	done := make(chan struct{})
	if r != nil {
		ctx := r.Context()
		go func() {
			<-ctx.Done()
			close(done)
		}()
	}
	ans, err := asks.Ask(askuser.Question{
		SessionID: sessionID,
		Question:  question,
		Fields:    fields,
		Timeout:   4 * time.Minute,
	}, done)
	if err != nil {
		return nil, err
	}
	if len(ans.Values) == 0 {
		return []string{}, nil
	}
	return storeSessionConfig(svc, layout, sessionID, inst.ID, specByKey, ans.Values)
}

// storeSessionConfig validates the supplied values against the base
// module's config specs and persists them on the session instance,
// returning the keys actually written (sorted). Shared by the modal path
// (openConfigModal) and the modal-less action=set_config path.
//
// Value handling per key:
//   - unknown key (not in the base specs) → skipped.
//   - empty value → skipped ("leave as-is", same rule as the UI Save).
//   - secret field, value already an enc token (wick_cenc_ master OR
//     wick_enc_ per-user) → stored verbatim. This is the automation path:
//     the caller relays an already-encrypted token, so the plaintext never
//     passes through the agent.
//   - secret field, plaintext value → master-encrypted before it touches
//     disk (errors if encryption is disabled on this server).
//   - non-secret field → stored verbatim.
func storeSessionConfig(svc *connectors.Service, layout agentconfig.Layout, sessionID, instID string, specByKey map[string]entity.Config, values map[string]string) ([]string, error) {
	toStore := make(map[string]string, len(values))
	for k, v := range values {
		sp, ok := specByKey[k]
		if !ok || v == "" {
			continue
		}
		if sp.IsSecret && !enc.IsMasterToken(v) && !enc.IsToken(v) {
			e := svc.Enc()
			if e == nil || e.Disabled() {
				return nil, fmt.Errorf("config %q is secret but encryption is disabled on this server — cannot store it safely", k)
			}
			tok, err := e.EncryptMaster(v)
			if err != nil {
				return nil, fmt.Errorf("encrypt secret: %w", err)
			}
			v = tok
		}
		toStore[k] = v
	}
	if len(toStore) == 0 {
		return []string{}, nil
	}
	if err := sessionworkspace.SetConfig(layout, sessionID, instID, toStore); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(toStore))
	for k := range toStore {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

func askAllowedOK(askAllowed func(string) (bool, string), sessionID string) (bool, string) {
	if askAllowed == nil {
		return true, ""
	}
	return askAllowed(sessionID)
}

func argString(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func workspaceOverrideOr(cfg map[string]string, key, fallback string) string {
	if v, ok := cfg[key]; ok {
		return v
	}
	return fallback
}

func writeWorkspaceResult(w http.ResponseWriter, req RPCRequest, rsp Responder, out map[string]any) {
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}
