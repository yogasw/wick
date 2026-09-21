package agents

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/usagereport"
	"github.com/yogasw/wick/pkg/tool"
)

// api_usage.go serves the token ledger: what each provider burned, which
// projects and people it was burned for, and where a given provider is
// actually being used.
//
// The report itself is built by internal/agents/usagereport, which the
// admin analytics page reads too — one ledger, two renderers. What is
// left here is the HTTP shape: who may ask, for which window, and how
// the answer is cached.

// ledger is the process-wide report builder. Created on first use
// because the layout is only known once the tool is configured.
var ledger struct {
	mu sync.Mutex
	b  *usagereport.Builder
}

func usageLedger() *usagereport.Builder {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.b == nil || ledger.b.Layout != globalLayout {
		ledger.b = usagereport.New(globalLayout, func(id string) string {
			return channelOwnerNames()[id]
		})
	}
	return ledger.b
}

// apiUsageReport serves GET /api/providers/usage?window=today|7d|30d|90d|all.
func apiUsageReport(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if !requireProviderMenu(c) {
		return
	}
	w := usagereport.ParseWindow(c.Query("window"), time.Now())
	rep, err := usageLedger().Report(w, c.Query("refresh") == "1")
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rep)
}

// apiProviderUsage serves GET /api/providers/{type}/{name}/usage.
func apiProviderUsage(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if !requireProviderMenu(c) {
		return
	}
	key := c.PathValue("type") + "/" + c.PathValue("name")
	w := usagereport.ParseWindow(c.Query("window"), time.Now())
	rep, err := usageLedger().Provider(key, w, c.Query("refresh") == "1")
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, rep)
}

// SessionContextDTO is the composer's context meter: how full the window
// is right now, and what the session has spent getting there.
//
// Per provider, because a session that switched providers has two
// different windows and only the active one belongs in the meter — the
// others are history, shown in the panel but never in the ring.
type SessionContextDTO struct {
	SessionID string `json:"session_id"`
	// Provider is the active one ("type/name"), empty for a session that
	// has not run a turn yet.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Used / Window are the last reading. Window is 0 when the CLI does
	// not report a limit — the UI must then show tokens without a ring
	// rather than inventing a denominator.
	Used   int `json:"used"`
	Window int `json:"window"`
	// Pct is Used/Window as a percentage, 0 when Window is unknown.
	Pct float64 `json:"pct"`
	// Turns and Totals cover the whole session, all providers.
	Turns  int                `json:"turns"`
	Totals usagereport.Totals `json:"totals"`
	// Providers lists every provider this session used, newest reading
	// first for the active one.
	Providers []SessionContextProviderDTO `json:"providers"`
	// Trend is the recent per-turn context level for the active provider,
	// oldest first — the rise and fall the ring alone cannot show.
	Trend []int `json:"trend,omitempty"`
	// TrendAt timestamps those points, same order and length. A hovered
	// point has to be able to say WHEN it was: "88k at turn 24" is only
	// half an answer when the question is what made the window jump.
	TrendAt []string `json:"trend_at,omitempty"`
	// TrendSpent is the CUMULATIVE tokens this provider had spent by each
	// of those points. The level says how full the window was; this says
	// what it had cost to get there, which is the other half of every
	// question asked about a step in the curve.
	TrendSpent []int `json:"trend_spent,omitempty"`
	// CanCompact reports whether /compact does anything on this
	// session's provider. False for codex, whose exec mode has no slash
	// commands at all — the panel must not offer a button that would
	// only make the model SAY it compacted. See provider.CanCompact.
	CanCompact bool `json:"can_compact"`
	// CompactNote explains a false CanCompact in one sentence, so the
	// UI never has to keep its own copy of the reason.
	CompactNote string `json:"compact_note,omitempty"`
}

// SessionContextProviderDTO is one provider's row inside the panel.
type SessionContextProviderDTO struct {
	Provider string             `json:"provider"`
	Model    string             `json:"model,omitempty"`
	Used     int                `json:"used"`
	Window   int                `json:"window"`
	Pct      float64            `json:"pct"`
	Turns    int                `json:"turns"`
	Totals   usagereport.Totals `json:"totals"`
	LastAt   string             `json:"last_at,omitempty"`
}

// contextTrendMax caps the trend the API returns. The composer draws a
// sparkline, not a chart — more points would be invisible and would make
// a frequently-polled endpoint fat for nothing.
const contextTrendMax = 40

// apiSessionContext serves GET /api/sessions/{id}/context.
func apiSessionContext(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if !callerProjectAccess(c).allowSession(sess.Meta.ProjectID, sess.Meta.UserID, sess.Meta.Participants) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	su, err := store.LoadSessionUsage(globalLayout, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	out := SessionContextDTO{SessionID: id, Turns: su.Turns, Totals: usagereport.TotalsOf(su.Totals)}
	// The active provider is the one that answered most recently — not
	// the session's configured provider, which may have been switched a
	// second ago and not yet run a turn. The meter has to describe a
	// window that actually exists.
	var newest time.Time
	for name, p := range su.Providers {
		row := SessionContextProviderDTO{
			Provider: name,
			Model:    p.Model,
			Used:     p.ContextUsed,
			Window:   p.ContextWindow,
			Pct:      pctOf(p.ContextUsed, p.ContextWindow),
			Turns:    p.Turns,
			Totals:   usagereport.TotalsOf(p.UsageTotals),
		}
		if !p.LastAt.IsZero() {
			row.LastAt = p.LastAt.UTC().Format(time.RFC3339)
		}
		out.Providers = append(out.Providers, row)
		if p.LastAt.After(newest) {
			newest = p.LastAt
			out.Provider, out.Model = name, p.Model
			out.Used, out.Window = p.ContextUsed, p.ContextWindow
			out.Pct = row.Pct
			out.Trend, out.TrendAt = trendOf(p.Series)
		}
	}
	sortContextProviders(out.Providers, out.Provider)
	out.CanCompact = provider.CanCompact(provider.Type(contextProviderType(sess, out.Provider)))
	if !out.CanCompact {
		out.CompactNote = provider.CompactUnsupportedNote
	}
	c.JSON(http.StatusOK, out)
}

// contextProviderType names the provider type the NEXT turn will run
// on. The ledger's active provider is the best answer once a turn has
// finished; before that (or after a switch nobody has used yet) the
// session's own agent entry is, since that is what a /compact typed now
// would actually reach.
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

func pctOf(used, window int) float64 {
	if window <= 0 || used <= 0 {
		return 0
	}
	return float64(used) / float64(window) * 100
}

// trendOf returns the recent levels and when each was recorded. Both,
// because a hovered point that cannot say WHEN it was only answers half
// the question the curve raises.
func trendOf(series []store.UsagePoint) ([]int, []string) {
	if len(series) == 0 {
		return nil, nil
	}
	start := 0
	if len(series) > contextTrendMax {
		start = len(series) - contextTrendMax
	}
	levels := make([]int, 0, len(series)-start)
	at := make([]string, 0, len(series)-start)
	for _, pt := range series[start:] {
		levels = append(levels, pt.ContextUsed)
		if pt.At.IsZero() {
			at = append(at, "")
			continue
		}
		at = append(at, pt.At.UTC().Format(time.RFC3339))
	}
	return levels, at
}

// pointTokens is everything one turn put on the wire.
func pointTokens(pt store.UsagePoint) int {
	return pt.Input + pt.CacheRead + pt.CacheWrite + pt.Output
}

// trendSpentOf returns the cumulative spend through each point the trend
// returns, for the same window trendOf returns.
//
// Anchored to the provider's EXACT total and walked backwards, never
// summed forwards: the per-turn trail is capped, so a forward sum would
// start from whatever survived and quietly under-report every point by
// the history that fell off the end. Subtracting from a known total
// keeps the newest points right — and the newest points are the ones
// somebody is pointing at.
func trendSpentOf(series []store.UsagePoint, total int) []int {
	if len(series) == 0 {
		return nil
	}
	start := 0
	if len(series) > contextTrendMax {
		start = len(series) - contextTrendMax
	}
	out := make([]int, len(series)-start)
	after := 0
	for i := len(series) - 1; i >= start; i-- {
		out[i-start] = total - after
		after += pointTokens(series[i])
	}
	return out
}

// sortContextProviders puts the active provider first and the rest in a
// stable order, so the panel does not reshuffle between polls.
func sortContextProviders(rows []SessionContextProviderDTO, active string) {
	sort.Slice(rows, func(i, j int) bool {
		if (rows[i].Provider == active) != (rows[j].Provider == active) {
			return rows[i].Provider == active
		}
		return rows[i].Provider < rows[j].Provider
	})
}
