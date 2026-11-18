package agents

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/sessionworkspace"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/pkg/tool"
)

// Connector access per session (Workspace panel): what this session's
// agent did with each connector, and the session's own say over each one
// (allow, ask each time, block). The policy is a small file next to the
// session's access history, `sessions/<id>/access/policy.json`; the usage
// comes from the run log, which already records the session.

const sessionPolicyFile = "policy.json"

type sessionPolicyDoc struct {
	Connectors map[string]string `json:"connectors"`
	UpdatedAt  time.Time         `json:"updated_at"`
	UpdatedBy  string            `json:"updated_by,omitempty"`
}

var sessionPolicyCache sync.Map // sessionID → sessionPolicyDoc

func sessionPolicyPath(sessionID string) string {
	return filepath.Join(accessSessionDir(globalLayout, sessionID), sessionPolicyFile)
}

func loadSessionPolicy(sessionID string) sessionPolicyDoc {
	if v, ok := sessionPolicyCache.Load(sessionID); ok {
		return v.(sessionPolicyDoc)
	}
	var d sessionPolicyDoc
	if b, err := os.ReadFile(sessionPolicyPath(sessionID)); err == nil {
		_ = json.Unmarshal(b, &d)
	}
	if d.Connectors == nil {
		d.Connectors = map[string]string{}
	}
	sessionPolicyCache.Store(sessionID, d)
	return d
}

// SessionConnectorPolicy is a session's policy for one connector: allow
// (default), ask or block.
func SessionConnectorPolicy(sessionID, connectorID string) string {
	if sessionID == "" || connectorID == "" || globalLayout.BaseDir == "" || !safeAccessID(sessionID) {
		return connectors.SessionPolicyAllow
	}
	if m := loadSessionPolicy(sessionID).Connectors[connectorID]; m != "" {
		return m
	}
	return connectors.SessionPolicyAllow
}

func saveSessionPolicy(sessionID, connectorID, mode, by string) error {
	d := loadSessionPolicy(sessionID)
	next := map[string]string{}
	for k, v := range d.Connectors {
		next[k] = v
	}
	if mode == connectors.SessionPolicyAllow {
		delete(next, connectorID)
	} else {
		next[connectorID] = mode
	}
	d = sessionPolicyDoc{Connectors: next, UpdatedAt: time.Now().UTC(), UpdatedBy: by}
	dir := accessSessionDir(globalLayout, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := sessionPolicyPath(sessionID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, sessionPolicyPath(sessionID)); err != nil {
		return err
	}
	sessionPolicyCache.Store(sessionID, d)
	return nil
}

// sessionConnectorUsage is one connector's line in the panel.
type sessionConnectorUsage struct {
	ConnectorID string    `json:"connector_id"`
	Key         string    `json:"key"`
	Label       string    `json:"label"`
	Kind        string    `json:"kind"` // saved | session
	Calls       int       `json:"calls"`
	Errors      int       `json:"errors"`
	Overrides   int       `json:"overrides"`
	Approved    int       `json:"approved"`
	LastAt      time.Time `json:"last_at"`
	LastOp      string    `json:"last_op"`
	LastStatus  string    `json:"last_status"`
	LastError   string    `json:"last_error,omitempty"`
	LastNote    string    `json:"last_note,omitempty"`
	Mode        string    `json:"mode"`
}

// sessionAccessRecent caps how many runs the panel reads.
const sessionAccessRecent = 1000

func sessionAccessJSON(c *tool.Ctx) {
	sid := c.PathValue("id")
	if !safeAccessID(sid) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "bad session id"})
		return
	}
	ctx := c.Context()
	byID := map[string]*sessionConnectorUsage{}
	var order []string
	get := func(id string) *sessionConnectorUsage {
		if u, ok := byID[id]; ok {
			return u
		}
		u := &sessionConnectorUsage{ConnectorID: id, Kind: "saved", Label: id}
		if strings.HasPrefix(id, "sw_") {
			u.Kind = "session"
			if inst, ok, _ := sessionworkspace.Get(globalLayout, sid, id); ok {
				u.Key, u.Label = inst.BaseKey, inst.Label
			}
		} else if globalConnectors != nil {
			if row, err := globalConnectors.Get(ctx, id); err == nil && row != nil {
				u.Key, u.Label = row.Key, row.Label
			}
		}
		byID[id] = u
		order = append(order, id)
		return u
	}
	if globalConnectors != nil {
		runs, _ := globalConnectors.ListRunsAudit(ctx, connectors.AuditFilter{SessionID: sid}, sessionAccessRecent, 0)
		ids := make([]string, 0, len(runs))
		for _, r := range runs {
			ids = append(ids, r.ID)
		}
		lends, _ := globalConnectors.RunLends(ctx, ids)
		for _, r := range runs { // newest first
			u := get(r.ConnectorID)
			u.Calls++
			if r.Status == "error" {
				u.Errors++
			}
			if strings.Contains(r.RequestJSON, `"`+connectors.ConfigOverrideParam+`"`) {
				u.Overrides++
			}
			if _, lent := lends[r.ID]; lent {
				u.Approved++
			}
			if u.LastAt.IsZero() {
				u.LastAt, u.LastOp, u.LastStatus = r.StartedAt, r.OperationKey, string(r.Status)
				u.LastError = truncateText(r.ErrorMsg, 160)
				u.LastNote = overrideNote(r.RequestJSON)
			}
		}
	}
	// Connectors with a policy but no run yet, and the session's own
	// connectors, show too.
	pol := loadSessionPolicy(sid)
	for id := range pol.Connectors {
		get(id)
	}
	if insts, err := sessionworkspace.List(globalLayout, sid); err == nil {
		for _, in := range insts {
			get(in.ID)
		}
	}
	list := make([]sessionConnectorUsage, 0, len(order))
	for _, id := range order {
		u := byID[id]
		u.Mode = SessionConnectorPolicy(sid, id)
		list = append(list, *u)
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].LastAt.After(list[j].LastAt) })

	enabled := false
	var grants []map[string]any
	if globalAccess != nil {
		enabled = globalAccess.Enabled(sid)
		gs, _ := globalAccess.Grants(ctx, "session", sid)
		for _, g := range gs {
			if g.RevokedAt != nil {
				continue
			}
			grants = append(grants, map[string]any{
				"id": g.ID, "connector_id": g.ConnectorID, "op_key": g.OpKey, "target": g.Target,
				"approver": globalAccess.PersonName(ctx, g.ApproverID), "created_at": g.CreatedAt,
			})
		}
	}
	if grants == nil {
		grants = []map[string]any{}
	}
	c.JSON(http.StatusOK, map[string]any{
		"enabled": enabled, "connectors": list, "requests": sessionAccessHistory(sid, 30), "grants": grants,
	})
}

// sessionAccessHistory reads the session's request snapshots, newest
// first.
func sessionAccessHistory(sessionID string, limit int) []map[string]any {
	out := []map[string]any{}
	dir := accessSessionDir(globalLayout, sessionID)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	type item struct {
		at time.Time
		m  map[string]any
	}
	var items []item
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") || name == sessionPolicyFile {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, stringOf(m["created_at"]))
		items = append(items, item{at: at, m: m})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at.After(items[j].at) })
	for i, it := range items {
		if i >= limit {
			break
		}
		out = append(out, it.m)
	}
	return out
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

// overrideNote renders the override a run used ("mode=write"), "" when
// none.
func overrideNote(requestJSON string) string {
	if !strings.Contains(requestJSON, connectors.ConfigOverrideParam) {
		return ""
	}
	var m map[string]any
	if json.Unmarshal([]byte(requestJSON), &m) != nil {
		return ""
	}
	ov, _ := m[connectors.ConfigOverrideParam].(map[string]any)
	var parts []string
	for k, v := range ov {
		parts = append(parts, k+"="+stringOf(v))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func sessionAccessPolicySet(c *tool.Ctx) {
	if !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	sid := c.PathValue("id")
	var body struct {
		ConnectorID string `json:"connector_id"`
		Mode        string `json:"mode"`
	}
	if err := c.BindJSON(&body); err != nil || !safeAccessID(sid) || strings.TrimSpace(body.ConnectorID) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "connector_id and mode are required"})
		return
	}
	switch body.Mode {
	case connectors.SessionPolicyAllow, connectors.SessionPolicyAsk, connectors.SessionPolicyBlock:
	default:
		c.JSON(http.StatusBadRequest, map[string]string{"error": "mode must be allow, ask or block"})
		return
	}
	if err := saveSessionPolicy(sid, body.ConnectorID, body.Mode, u.ID); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"connector_id": body.ConnectorID, "mode": body.Mode})
}

func sessionAccessGrantRevoke(c *tool.Ctx) {
	if !accessReady(c) || !accessSameOrigin(c) {
		return
	}
	u := accessUser(c)
	if u == nil {
		return
	}
	if err := globalAccess.RevokeSessionGrant(c.Context(), c.PathValue("id"), c.PathValue("gid"), u.ID); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]bool{"ok": true})
}
