package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/login"
)

// context_usage.go answers the two questions an agent cannot answer about
// itself — "how full is my window" and "what has this conversation spent" —
// plus the one action that follows from them, /compact.
//
// All three take no session id in normal use: the call already carries one.
// An explicit session_id still names another session you own, gated by
// canManageSession, because "why is that run so expensive" is a question
// about somebody else's conversation by definition.

// contextTrendMax caps the per-turn trail returned. The same bound the web
// panel uses: a sparkline, not a chart.
const contextTrendMax = 40

type usageTotalsOut struct {
	Input      int     `json:"input"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Output     int     `json:"output"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
}

func totalsOut(t store.UsageTotals) usageTotalsOut {
	return usageTotalsOut{
		Input: t.Input, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite,
		Output: t.Output, CostUSD: t.CostUSD,
	}
}

type providerUsageOut struct {
	Provider string         `json:"provider"`
	Model    string         `json:"model,omitempty"`
	Used     int            `json:"used"`
	Window   int            `json:"window"`
	Pct      float64        `json:"pct"`
	Turns    int            `json:"turns"`
	Totals   usageTotalsOut `json:"totals"`
	LastAt   string         `json:"last_at,omitempty"`
}

// resolveManagedSession picks the session these tools act on and checks the
// caller may see it. An explicit id wins (that is how you ask about another
// run); otherwise the call's own session answers.
func resolveManagedSession(r *http.Request, layout agentconfig.Layout, args map[string]any, tool string) (session.Session, string, bool, string) {
	raw, _ := args["session_id"].(string)
	id := ResolveSessionPreferArg(SessionOf(r), raw)
	if id == "" {
		return session.Session{}, "", false, "session_id is required (no session on the call)"
	}
	sess, err := session.Load(layout, id)
	if err != nil {
		return session.Session{}, id, false, "load session: " + err.Error()
	}
	if !canManageSession(login.GetUser(r.Context()), sess.Meta.UserID) {
		return session.Session{}, id, false, fmt.Sprintf("session not found: %s", id)
	}
	return sess, id, true, ""
}

// WickContext reports how full the model's context window is right now,
// for the provider that most recently answered — the reading a /compact
// decision is made from.
func WickContext(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, args map[string]any) {
	const tool = "wick_context"
	sess, id, ok, msg := resolveManagedSession(r, layout, args, tool)
	if !ok {
		rsp.ToolError(w, req.ID, msg, tool)
		return
	}
	su, err := store.LoadSessionUsage(layout, id)
	if err != nil {
		rsp.ToolError(w, req.ID, "load usage: "+err.Error(), tool)
		return
	}

	out := map[string]any{"session_id": id, "turns": su.Turns, "totals": totalsOut(su.Totals)}
	rows := make([]providerUsageOut, 0, len(su.Providers))
	// The ACTIVE provider is whichever answered most recently, not the one
	// configured: a switch that has not run a turn yet describes a window
	// that does not exist.
	var newest time.Time
	active := ""
	for name, p := range su.Providers {
		row := providerUsageOut{
			Provider: name, Model: p.Model,
			Used: p.ContextUsed, Window: p.ContextWindow,
			Pct: pctOfInt(p.ContextUsed, p.ContextWindow), Turns: p.Turns,
			Totals: totalsOut(p.UsageTotals),
		}
		if !p.LastAt.IsZero() {
			row.LastAt = p.LastAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, row)
		if p.LastAt.After(newest) {
			newest = p.LastAt
			active = name
			out["provider"], out["model"] = name, p.Model
			out["used"], out["window"], out["pct"] = p.ContextUsed, p.ContextWindow, row.Pct
			if trend := trendOfSeries(p.Series); len(trend) > 0 {
				out["trend"] = trend
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if (rows[i].Provider == active) != (rows[j].Provider == active) {
			return rows[i].Provider == active
		}
		return rows[i].LastAt > rows[j].LastAt
	})
	out["providers"] = rows

	canCompact := provider.CanCompact(provider.Type(contextProviderType(sess, active)))
	out["can_compact"] = canCompact
	if !canCompact {
		out["compact_note"] = provider.CompactUnsupportedNote
	}
	if su.Turns == 0 {
		out["note"] = "no turn has run in this session yet, so there is no window reading"
	}
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{Content: []ToolContent{{Type: "text", Text: string(b)}}})
}

// WickUsage reports what a session has SPENT — token flows and cost, whole
// session and per provider. Distinct from wick_context on purpose: flows
// are additive and historical, a window level is neither.
func WickUsage(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, quota AccountQuotaFn, args map[string]any) {
	const tool = "wick_usage"
	sess, id, ok, msg := resolveManagedSession(r, layout, args, tool)
	if !ok {
		rsp.ToolError(w, req.ID, msg, tool)
		return
	}
	su, err := store.LoadSessionUsage(layout, id)
	if err != nil {
		rsp.ToolError(w, req.ID, "load usage: "+err.Error(), tool)
		return
	}
	rows := make([]providerUsageOut, 0, len(su.Providers))
	for name, p := range su.Providers {
		row := providerUsageOut{
			Provider: name, Model: p.Model,
			Used: p.ContextUsed, Window: p.ContextWindow,
			Pct: pctOfInt(p.ContextUsed, p.ContextWindow), Turns: p.Turns,
			Totals: totalsOut(p.UsageTotals),
		}
		if !p.LastAt.IsZero() {
			row.LastAt = p.LastAt.UTC().Format(time.RFC3339)
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].LastAt > rows[j].LastAt })

	out := map[string]any{
		"session_id": id,
		"turns":      su.Turns,
		"totals":     totalsOut(su.Totals),
		"tokens":     su.Totals.Input + su.Totals.CacheRead + su.Totals.CacheWrite + su.Totals.Output,
		"providers":  rows,
	}
	if !su.UpdatedAt.IsZero() {
		out["updated_at"] = su.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if su.Turns == 0 {
		out["note"] = "nothing recorded yet — no turn has run in this session"
	}
	// The other half of "usage": what the ACCOUNT has left. Keyed off the
	// provider that will actually run the next turn, which is the one the
	// limit would stop.
	if quota != nil {
		key := activeProviderKey(su)
		if key == "" {
			key = sessionProviderKey(sess)
		}
		if q, ok := quota(r.Context(), key); ok {
			out["account"] = q
		}
	}
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{Content: []ToolContent{{Type: "text", Text: string(b)}}})
}

// AccountQuotaWindow is one rate-limit window of the provider account —
// "Session (5hr) at 25%", "Weekly at 9%".
type AccountQuotaWindow struct {
	Key         string  `json:"key"`
	Label       string  `json:"label,omitempty"`
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at,omitempty"`
	ResetsInS   int     `json:"resets_in_s,omitempty"`
}

// AccountQuota is what the provider ACCOUNT has left, as opposed to what
// this conversation has spent.
//
// The two are asked for with the same word and are not the same thing:
// the ledger says a session cost $4, the quota says whether the next turn
// runs at all. An agent that has just been told it is at 100% of its
// weekly limit can stop and say so; one that only knows its token count
// finds out by failing.
type AccountQuota struct {
	Provider  string               `json:"provider,omitempty"`
	Supported bool                 `json:"supported"`
	Reason    string               `json:"reason,omitempty"`
	Connected bool                 `json:"connected"`
	Plan      string               `json:"plan,omitempty"`
	Org       string               `json:"org,omitempty"`
	Windows   []AccountQuotaWindow `json:"windows,omitempty"`
	FetchedAt string               `json:"fetched_at,omitempty"`
	// AgeS is how old the reading is. Always sent when there is one: the
	// probe is paced and cached, so a number with no provenance implies a
	// live call nobody made.
	AgeS     int    `json:"age_s,omitempty"`
	Pending  bool   `json:"pending,omitempty"`
	Checking bool   `json:"checking,omitempty"`
	Err      string `json:"error,omitempty"`
}

// AccountQuotaFn reads the cached quota for a provider key. Supplied by
// the server, which owns the paced probe cache; nil in stdio mode and
// tests, where wick_usage simply omits the account section rather than
// inventing one.
type AccountQuotaFn func(ctx context.Context, providerKey string) (AccountQuota, bool)

// quotaLabels names the windows the way the UI does, so an agent quoting
// a number uses the same words the person reading the panel sees.
var quotaLabels = map[string]string{
	"five_hour": "Session (5hr)",
	"seven_day": "Weekly (7 day)",
	"weekly":    "Weekly (7 day)",
}

// QuotaWindowLabel names a rate-limit window, falling back to its key —
// providers add windows (extra_usage, nimbus_quill) faster than any map
// can track, and an unknown key is still worth showing.
func QuotaWindowLabel(key string) string {
	if l := quotaLabels[key]; l != "" {
		return l
	}
	return key
}

// SessionSender delivers a message into a session the way a human message
// arrives. Satisfied by *pool.Pool; nil in stdio mode and tests, where
// wick_compact then reports itself unavailable rather than pretending.
type SessionSender func(ctx context.Context, sessionID, agentName, source, role, text string) error

// WickCompact asks a session to fold its history into a summary.
//
// It does NOT compact inside this call, and says so: /compact is delivered
// as an ordinary message, so it runs after the current turn finishes (or
// wakes an idle session, spawning it). Compacting the conversation you are
// mid-turn in cannot affect that turn anyway — its context was sent to the
// model before this call existed.
func WickCompact(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, send SessionSender, args map[string]any) {
	const tool = "wick_compact"
	sess, id, ok, msg := resolveManagedSession(r, layout, args, tool)
	if !ok {
		rsp.ToolError(w, req.ID, msg, tool)
		return
	}
	if send == nil {
		rsp.ToolError(w, req.ID, "compaction is unavailable on this transport (no agent pool)", tool)
		return
	}
	agentName := strings.TrimSpace(argString(args, "agent_name"))

	// Refuse rather than queue a command the provider would read as prose:
	// the model would answer "Context compacted." and the window would keep
	// filling. Same guard the pool applies, reported before the send so the
	// caller gets a reason instead of silence.
	ptype := contextProviderType(sess, activeProviderOf(layout, id))
	if !provider.CanCompact(provider.Type(ptype)) {
		rsp.ToolError(w, req.ID, provider.CompactUnsupportedNote, tool)
		return
	}

	if err := send(r.Context(), id, agentName, "mcp", "user", "/compact"); err != nil {
		rsp.ToolError(w, req.ID, "deliver /compact: "+err.Error(), tool)
		return
	}
	out := map[string]any{
		"session_id": id,
		"status":     "queued",
		"provider":   ptype,
		"note": "/compact was delivered as a message. A running turn finishes first; " +
			"an idle session is woken to do it. The result appears in that session's transcript.",
	}
	if agentName != "" {
		out["agent_name"] = agentName
	}
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{Content: []ToolContent{{Type: "text", Text: string(b)}}})
}

// activeProviderOf reports the provider key that answered most recently in
// a session, or "" when no turn has run.
func activeProviderOf(layout agentconfig.Layout, sessionID string) string {
	su, err := store.LoadSessionUsage(layout, sessionID)
	if err != nil {
		return ""
	}
	var newest time.Time
	active := ""
	for name, p := range su.Providers {
		if p.LastAt.After(newest) {
			newest, active = p.LastAt, name
		}
	}
	return active
}

// activeProviderKey is the provider that answered most recently in a
// ledger already loaded — the one whose account limit would stop the
// next turn.
func activeProviderKey(su *store.SessionUsage) string {
	var newest time.Time
	active := ""
	for name, p := range su.Providers {
		if p.LastAt.After(newest) {
			newest, active = p.LastAt, name
		}
	}
	return active
}

// sessionProviderKey is the provider a session WOULD run on before any
// turn has finished: its active agent's instance. Without it, asking a
// fresh session about its quota would answer nothing at all — which is
// exactly when someone checks whether they have room to start.
func sessionProviderKey(sess session.Session) string {
	name := sess.Meta.ActiveAgent
	for _, a := range sess.Agents {
		if (name == "" || a.Name == name) && a.Provider != "" {
			return a.Provider
		}
	}
	return ""
}

// contextProviderType names the provider type the NEXT turn runs on: the
// ledger's active provider once one has answered, else the session's own
// agent entry — which is what a /compact sent now would actually reach.
func contextProviderType(sess session.Session, active string) string {
	if active != "" {
		typ, _ := provider.SplitInstanceKey(active)
		return typ
	}
	name := sess.Meta.ActiveAgent
	for _, a := range sess.Agents {
		if (name == "" || a.Name == name) && a.Provider != "" {
			typ, _ := provider.SplitInstanceKey(a.Provider)
			return typ
		}
	}
	return ""
}

// pctOfInt is used/window as a percentage, 0 when the window is unknown —
// a denominator nobody reported must not be invented.
func pctOfInt(used, window int) float64 {
	if window <= 0 || used <= 0 {
		return 0
	}
	return math.Round(float64(used)/float64(window)*1000) / 10
}

// trendOfSeries returns the recent per-turn context levels, oldest first.
func trendOfSeries(series []store.UsagePoint) []int {
	if len(series) == 0 {
		return nil
	}
	if len(series) > contextTrendMax {
		series = series[len(series)-contextTrendMax:]
	}
	out := make([]int, 0, len(series))
	for _, p := range series {
		out = append(out, p.ContextUsed)
	}
	return out
}
