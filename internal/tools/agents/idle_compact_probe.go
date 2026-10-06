package agents

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/pkg/tool"
)

// idleCompactProbeResult answers "would this session be compacted?" for
// the scope and patterns being edited, before they are saved.
type idleCompactProbeResult struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title"`
	Project   string `json:"project"`
	ProjectID string `json:"project_id"`
	// Rule is the 1-based pattern line that matched, 0 for none; RuleText
	// is that line.
	Rule     int    `json:"rule"`
	RuleText string `json:"rule_text,omitempty"`
	// InScope is true when the scope leaves the session to be compacted.
	InScope bool `json:"in_scope"`
	// Enabled is the instance's saved on/off switch.
	Enabled bool `json:"enabled"`
	// SessionProvider is the instance the session's context runs on; the
	// policy only applies when it is this instance (SameProvider).
	SessionProvider string `json:"session_provider,omitempty"`
	SameProvider    bool   `json:"same_provider"`
	ContextUsed     int    `json:"context_used"`
	ContextWindow   int    `json:"context_window"`
	// OverThreshold reports whether the context is past the threshold now.
	OverThreshold bool `json:"over_threshold"`
}

// probeIdleCompact handles POST /providers/idle-compact-probe/{type}/{name}
// with session (a session link or id), scope and match.
func probeIdleCompact(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	t := provider.Type(c.PathValue("type"))
	name := c.PathValue("name")
	if !canManageProvider(c, t, name) && !requireProviderAdmin(c) {
		return
	}
	ins, err := provider.Find(t, name)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
		return
	}
	sid := sessionIDFromLink(c.Form("session"))
	if sid == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "paste a session link or id"})
		return
	}
	sess, ok := globalMgr.Registry().Session(sid)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found: " + sid})
		return
	}
	scope, raw := c.Form("scope"), c.Form("match")
	if err := provider.ValidateInstanceConfigKey("idle_compact_scope", scope); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := provider.ValidateInstanceConfigKey("idle_compact_match", raw); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	draft := ins
	draft.IdleCompactScope, draft.IdleCompactMatch = scope, raw
	pol := provider.IdleCompactPolicyOf(draft)

	res := idleCompactProbeResult{SessionID: sid, Title: sess.Meta.Label, ProjectID: sess.Meta.ProjectID, Enabled: pol.Enabled}
	if res.ProjectID != "" {
		if p, err := project.Load(globalLayout, res.ProjectID); err == nil {
			res.Project = p.Meta.Name
		}
	}
	ref := provider.SessionRef{ID: sid, Title: res.Title, Project: res.Project, ProjectID: res.ProjectID}
	// Match line by line so the reported number is the line the user sees.
	if pol.Scope != provider.IdleCompactScopeAll {
		lines := provider.IdleCompactMatchLines(raw)
		if pol.Scope == provider.IdleCompactScopeSkip && strings.TrimSpace(raw) == "" {
			lines = provider.IdleCompactMatchLines(provider.DefaultIdleCompactSkip)
		}
		for i, line := range lines {
			ps, _ := provider.ParseSessionPatterns(line)
			if len(ps) == 1 && ps[0].Match(ref) {
				res.Rule, res.RuleText = i+1, line
				break
			}
		}
	}
	res.InScope = !pol.Skips(ref)

	if su, err := store.LoadSessionUsage(globalLayout, sid); err == nil {
		key := activeContextProvider(su.Providers)
		res.SessionProvider = key
		if typ, n := provider.SplitInstanceKey(key); typ == string(ins.Type) && n == ins.Name {
			res.SameProvider = true
		}
		if p := su.Providers[key]; p != nil {
			res.ContextUsed, res.ContextWindow = p.ContextUsed, p.ContextWindow
			// Due with an idle stretch long enough isolates the threshold.
			on := pol
			on.Enabled = true
			res.OverThreshold = on.Due(p.ContextUsed, p.ContextWindow, pol.Idle)
		}
	}
	c.JSON(http.StatusOK, res)
}

// sessionIDFromLink pulls a session id out of a pasted session link
// (…/sessions/<id>, ?session=<id>) or returns the text as an id.
func sessionIDFromLink(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && (u.Scheme != "" || strings.HasPrefix(s, "/")) {
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i := 0; i+1 < len(segs); i++ {
			if segs[i] == "sessions" || segs[i] == "session" {
				return segs[i+1]
			}
		}
		for _, k := range []string{"session", "session_id", "sid"} {
			if v := u.Query().Get(k); v != "" {
				return v
			}
		}
		if len(segs) > 0 {
			return segs[len(segs)-1]
		}
	}
	return s
}
