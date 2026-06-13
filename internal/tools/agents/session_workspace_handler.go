package agents

import (
	"net/http"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/sessionworkspace"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/enc"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// Session workspace (the Config tab) — the USER-facing surface for a
// session's ephemeral connector instances. Each instance is a throwaway
// clone of a base connector, scoped to this session, with its own
// config. Same store the wick_session_workspace MCP tool writes to; here
// the user adds, fills, tests, and removes instances directly in the
// web UI.
//
// Secrets are master-encrypted (system-only) before they ever hit disk;
// responses never echo a secret value back — only whether a field is set.

// wsFieldVM is one config field of a session instance in the Config tab.
type wsFieldVM struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // "text" | "secret" | "dropdown"
	Secret   bool     `json:"secret"`
	Value    string   `json:"value"` // non-secret current value; "" for secrets
	Set      bool     `json:"set"`   // whether a value is stored (any field)
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required"`
	Help     string   `json:"help,omitempty"`
}

// wsInstanceVM is one session instance card in the Config tab.
type wsInstanceVM struct {
	ID      string      `json:"id"`
	BaseKey string      `json:"base_key"`
	Label   string      `json:"label"`
	Status  string      `json:"status"` // ready | needs_setup
	Fields  []wsFieldVM `json:"fields"`
}

// wsBaseVM is one base connector the user may add as a session instance.
type wsBaseVM struct {
	BaseKey string `json:"base_key"`
	Label   string `json:"label"`
}

// wsBuildFields renders a base module's config schema with the instance's
// current values. Secret values are never sent — only whether set.
func wsBuildFields(specs []entity.Config, cfg map[string]string) []wsFieldVM {
	fields := make([]wsFieldVM, 0, len(specs))
	for _, sp := range specs {
		if sp.Hidden {
			continue
		}
		_, set := cfg[sp.Key]
		f := wsFieldVM{
			Key:      sp.Key,
			Label:    sp.Key,
			Secret:   sp.IsSecret,
			Set:      set,
			Required: sp.Required,
			Help:     sp.Description,
		}
		switch {
		case sp.IsSecret:
			f.Type = "secret"
		case sp.Type == "dropdown" && sp.Options != "":
			f.Type = "dropdown"
			f.Options = strings.Split(sp.Options, "|")
			f.Value = wsValueOr(cfg, sp.Key, sp.Value)
		default:
			f.Type = "text"
			f.Value = wsValueOr(cfg, sp.Key, sp.Value)
		}
		fields = append(fields, f)
	}
	return fields
}

func wsValueOr(cfg map[string]string, key, fallback string) string {
	if v, ok := cfg[key]; ok {
		return v
	}
	return fallback
}

func wsStatus(specs []entity.Config, cfg map[string]string) string {
	for _, sp := range specs {
		if sp.Hidden || !sp.Required {
			continue
		}
		if strings.TrimSpace(cfg[sp.Key]) == "" {
			return "needs_setup"
		}
	}
	return "ready"
}

// wsBases lists the base connectors the caller may add: module declares
// AllowSessionConfig AND a visible instance has the per-instance toggle on.
func wsBases(c *tool.Ctx) []wsBaseVM {
	if globalConnectors == nil {
		return nil
	}
	user := login.GetUser(c.Context())
	if user == nil {
		return nil
	}
	var tagIDs []string
	if globalAuth != nil {
		tagIDs = globalAuth.GetUserFilterTagIDs(c.Context(), user.ID)
	}
	rows, err := globalConnectors.ListVisibleTo(c.Context(), tagIDs, user.IsAdmin())
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]wsBaseVM, 0)
	for _, row := range rows {
		if seen[row.Key] {
			continue
		}
		mod, ok := globalConnectors.Module(row.Key)
		if !ok || !mod.AllowSessionConfig || !row.AllowSessionConfig {
			continue
		}
		seen[row.Key] = true
		out = append(out, wsBaseVM{BaseKey: row.Key, Label: mod.Meta.Name})
	}
	return out
}

func wsBaseAllowed(c *tool.Ctx, key string) bool {
	for _, b := range wsBases(c) {
		if b.BaseKey == key {
			return true
		}
	}
	return false
}

func wsInstanceToVM(in sessionworkspace.Instance) wsInstanceVM {
	vm := wsInstanceVM{ID: in.ID, BaseKey: in.BaseKey, Label: in.Label, Status: "needs_setup"}
	mod, ok := globalConnectors.Module(in.BaseKey)
	if !ok {
		return vm
	}
	specs := mod.Configs
	vm.Fields = wsBuildFields(specs, in.Config)
	vm.Status = wsStatus(specs, in.Config)
	return vm
}

// sessionWorkspaceListUI drives the Config tab: this session's instances
// (editable cards) + the bases the user can add.
func sessionWorkspaceListUI(c *tool.Ctx) {
	if globalConnectors == nil {
		c.JSON(http.StatusOK, map[string]any{"instances": []any{}, "bases": []any{}})
		return
	}
	sid := c.PathValue("id")
	if strings.TrimSpace(sid) == "" {
		c.Error(http.StatusBadRequest, "session id required")
		return
	}
	instances, err := sessionworkspace.List(globalLayout, sid)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	vms := make([]wsInstanceVM, 0, len(instances))
	for _, in := range instances {
		vms = append(vms, wsInstanceToVM(in))
	}
	c.JSON(http.StatusOK, map[string]any{"instances": vms, "bases": wsBases(c)})
}

// sessionWorkspaceAddUI creates a blank instance from a base connector.
func sessionWorkspaceAddUI(c *tool.Ctx) {
	sid := c.PathValue("id")
	if strings.TrimSpace(sid) == "" {
		c.Error(http.StatusBadRequest, "session id required")
		return
	}
	var body struct {
		BaseKey string `json:"base_key"`
		Label   string `json:"label"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.Error(http.StatusBadRequest, "invalid JSON")
		return
	}
	body.BaseKey = strings.TrimSpace(body.BaseKey)
	if body.BaseKey == "" {
		c.Error(http.StatusBadRequest, "base_key required")
		return
	}
	if !wsBaseAllowed(c, body.BaseKey) {
		c.Error(http.StatusForbidden, "this connector is not available for session instances")
		return
	}
	mod, _ := globalConnectors.Module(body.BaseKey)
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = mod.Meta.Name + " (session)"
	}
	inst, err := sessionworkspace.Add(globalLayout, sid, sessionworkspace.Instance{
		BaseKey:   body.BaseKey,
		Label:     label,
		CreatedBy: "user",
	})
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, wsInstanceToVM(inst))
}

// sessionWorkspaceInstanceUI returns one instance's field schema + state.
func sessionWorkspaceInstanceUI(c *tool.Ctx) {
	sid := c.PathValue("id")
	cid := c.PathValue("cid")
	inst, specs, ok := wsResolve(c, sid, cid)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, wsInstanceVM{
		ID:      inst.ID,
		BaseKey: inst.BaseKey,
		Label:   inst.Label,
		Status:  wsStatus(specs, inst.Config),
		Fields:  wsBuildFields(specs, inst.Config),
	})
}

// wsResolve loads an instance + its base module config specs, writing the
// error and returning ok=false when missing.
func wsResolve(c *tool.Ctx, sid, cid string) (sessionworkspace.Instance, []entity.Config, bool) {
	if globalConnectors == nil {
		c.Error(http.StatusServiceUnavailable, "connectors not ready")
		return sessionworkspace.Instance{}, nil, false
	}
	if strings.TrimSpace(sid) == "" || strings.TrimSpace(cid) == "" {
		c.Error(http.StatusBadRequest, "session id and instance id required")
		return sessionworkspace.Instance{}, nil, false
	}
	inst, ok, err := sessionworkspace.Get(globalLayout, sid, cid)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return sessionworkspace.Instance{}, nil, false
	}
	if !ok {
		c.Error(http.StatusNotFound, "session instance not found")
		return sessionworkspace.Instance{}, nil, false
	}
	mod, ok := globalConnectors.Module(inst.BaseKey)
	if !ok {
		c.Error(http.StatusNotFound, "base connector module not registered")
		return sessionworkspace.Instance{}, nil, false
	}
	return inst, mod.Configs, true
}

// sessionWorkspaceSetUI stores user-entered config. Secrets are
// master-encrypted before they touch disk; the agent never sees them.
func sessionWorkspaceSetUI(c *tool.Ctx) {
	sid := c.PathValue("id")
	cid := c.PathValue("cid")
	inst, specs, ok := wsResolve(c, sid, cid)
	if !ok {
		return
	}
	specByKey := make(map[string]entity.Config, len(specs))
	for _, sp := range specs {
		specByKey[sp.Key] = sp
	}
	var body struct {
		Values map[string]string `json:"values"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.Error(http.StatusBadRequest, "invalid JSON")
		return
	}
	toStore := make(map[string]string, len(body.Values))
	for k, v := range body.Values {
		sp, known := specByKey[k]
		if !known {
			c.Error(http.StatusBadRequest, "unknown config key: "+k)
			return
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue // empty = leave as-is
		}
		if sp.IsSecret && !enc.IsMasterToken(v) {
			e := globalConnectors.Enc()
			if e == nil || e.Disabled() {
				c.Error(http.StatusServiceUnavailable, "encryption disabled — cannot store secret safely")
				return
			}
			tok, err := e.EncryptMaster(v)
			if err != nil {
				c.Error(http.StatusInternalServerError, "encrypt: "+err.Error())
				return
			}
			v = tok
		}
		toStore[k] = v
	}
	if len(toStore) == 0 {
		c.JSON(http.StatusOK, map[string]any{"applied": []string{}})
		return
	}
	if err := sessionworkspace.SetConfig(globalLayout, sid, inst.ID, toStore); err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	keys := make([]string, 0, len(toStore))
	for k := range toStore {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	c.JSON(http.StatusOK, map[string]any{"applied": keys})
}

// sessionWorkspaceDuplicateUI copies an instance (config and all).
func sessionWorkspaceDuplicateUI(c *tool.Ctx) {
	sid := c.PathValue("id")
	cid := c.PathValue("cid")
	inst, _, ok := wsResolve(c, sid, cid)
	if !ok {
		return
	}
	cfg := make(map[string]string, len(inst.Config))
	for k, v := range inst.Config {
		cfg[k] = v
	}
	copyInst, err := sessionworkspace.Add(globalLayout, sid, sessionworkspace.Instance{
		BaseKey:   inst.BaseKey,
		Label:     inst.Label + " (copy)",
		Config:    cfg,
		CreatedBy: "user",
	})
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, wsInstanceToVM(copyInst))
}

// sessionWorkspaceTestUI verifies an instance: base HealthCheck, or run a
// named operation (?operation=…) as the probe.
func sessionWorkspaceTestUI(c *tool.Ctx) {
	sid := c.PathValue("id")
	cid := c.PathValue("cid")
	inst, _, ok := wsResolve(c, sid, cid)
	if !ok {
		return
	}
	user := login.GetUser(c.Context())
	if user == nil {
		c.Error(http.StatusUnauthorized, "no user")
		return
	}

	if opKey := strings.TrimSpace(c.Query("operation")); opKey != "" {
		var body struct {
			Params map[string]string `json:"params"`
		}
		_ = c.BindJSON(&body)
		res, execErr := globalConnectors.Execute(c.Context(), connectors.ExecuteParams{
			ConnectorID:  inst.ID,
			OperationKey: opKey,
			Input:        body.Params,
			Source:       entity.ConnectorRunSourceTest,
			UserID:       user.ID,
			IsAdmin:      user.IsAdmin(),
			SessionInstance: &connectors.SessionInstanceTarget{
				BaseKey: inst.BaseKey, Label: inst.Label, Config: inst.Config,
			},
		})
		if execErr != nil {
			c.JSON(http.StatusOK, map[string]any{"ok": false, "error": execErr.Error()})
			return
		}
		c.JSON(http.StatusOK, map[string]any{"ok": true, "response": res.ResponseJSON})
		return
	}

	report, err := globalConnectors.HealthCheckSessionInstance(c.Context(), inst.BaseKey, inst.ID, inst.Config)
	if err != nil {
		if err == connectors.ErrNoHealthCheck {
			c.JSON(http.StatusOK, map[string]any{"ok": false, "no_health_check": true, "note": "No health check — pass ?operation= to run a real call."})
			return
		}
		c.JSON(http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	allOK := true
	for _, h := range report {
		if !h.OK {
			allOK = false
			break
		}
	}
	c.JSON(http.StatusOK, map[string]any{"ok": allOK, "report": report})
}

// sessionWorkspaceRemoveUI deletes an instance.
func sessionWorkspaceRemoveUI(c *tool.Ctx) {
	sid := c.PathValue("id")
	cid := c.PathValue("cid")
	if strings.TrimSpace(sid) == "" || strings.TrimSpace(cid) == "" {
		c.Error(http.StatusBadRequest, "session id and instance id required")
		return
	}
	removed, err := sessionworkspace.Remove(globalLayout, sid, cid)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, map[string]any{"removed": removed})
}
