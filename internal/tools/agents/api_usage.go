package agents

import (
	"net/http"
	"sort"
	"strings"
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

// scopeFromQuery reads the channel filter off the query, in the same
// shape the analytics page sends it.
//
// Channels only. This mount resolves sessions with the ledger's default
// reader, which knows a session's origin but not WHICH bot of that
// channel served it — so an instance filter here would match nothing and
// return an empty report that looks like a real answer. Instance
// filtering lives where the instance is known (the admin analytics page,
// which supplies its own resolver).
func scopeFromQuery(c *tool.Ctx) usagereport.Scope {
	return usagereport.Scope{Channels: splitCSV(c.Query("channels"))}
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" && !strings.EqualFold(v, "all") {
			out = append(out, v)
		}
	}
	return out
}

// apiUsageReport serves GET /api/providers/usage?window=today|7d|30d|90d|all.
func apiUsageReport(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if !requireProviderMenu(c) {
		return
	}
	w := usagereport.WindowFromQuery(c.Query("window"), c.Query("since"), c.Query("until"), time.Now())
	rep, err := usageLedger().Report(w, scopeFromQuery(c), c.Query("refresh") == "1")
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
	w := usagereport.WindowFromQuery(c.Query("window"), c.Query("since"), c.Query("until"), time.Now())
	rep, err := usageLedger().Provider(key, w, scopeFromQuery(c), c.Query("refresh") == "1")
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
	// The active provider's trail, flattened into the top level for the
	// sparkline: trend, trend_at, trend_spent and the rest. Every
	// provider's own trail is on its row in Providers.
	ContextSeriesDTO
	// CanCompact reports whether /compact does anything on this
	// session's provider. False for codex, whose exec mode has no slash
	// commands at all — the panel must not offer a button that would
	// only make the model SAY it compacted. See provider.CanCompact.
	CanCompact bool `json:"can_compact"`
	// CompactNote explains a false CanCompact in one sentence, so the
	// UI never has to keep its own copy of the reason.
	CompactNote string `json:"compact_note,omitempty"`
	// AutoCompact is whether the active provider compacts by itself once
	// the window fills, as it last reported (omp get_state, opencode
	// config); omitted when it never said.
	AutoCompact *bool `json:"auto_compact,omitempty"`
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
	// This provider's own trail, so a session that switched providers
	// can be drawn as one curve, merged in time order.
	ContextSeriesDTO
}

// ContextSeriesDTO is one provider's per-turn trail as the panel draws
// it. Every slice has one entry per point, oldest first; a level-only
// point (a turn that ended without usage, a compaction) has null in
// trend_turn, trend_sent and trend_cost.
type ContextSeriesDTO struct {
	// Trend is the per-turn context level — the rise and fall the ring
	// alone cannot show.
	Trend []int `json:"trend,omitempty"`
	// TrendAt timestamps those points. A hovered point has to be able
	// to say WHEN it was: "88k at turn 24" is only half an answer when
	// the question is what made the window jump.
	TrendAt []string `json:"trend_at,omitempty"`
	// TrendSpent is the CUMULATIVE tokens this provider had spent by each
	// of those points. The level says how full the window was; this says
	// what it had cost to get there, which is the other half of every
	// question asked about a step in the curve.
	TrendSpent []int `json:"trend_spent,omitempty"`
	// TrendTurn is the provider's real turn number of each point, counted
	// back from its turn total — so it stays right after the stored trail
	// was capped, instead of restarting at 1.
	TrendTurn []*int `json:"trend_turn,omitempty"`
	// TrendSent / TrendCost are what each turn itself put on the wire
	// and cost.
	TrendSent []*int     `json:"trend_sent,omitempty"`
	TrendCost []*float64 `json:"trend_cost,omitempty"`
	// TrendTotal is how many points are stored, whatever was returned:
	// the trail is capped at store.UsageSeriesMax.
	TrendTotal int `json:"trend_total,omitempty"`
}

// contextTrendMax caps the trend the API returns. The composer draws a
// sparkline, not a chart — more points would be invisible and would make
// a frequently-polled endpoint fat for nothing. ?trend=all lifts it for
// the panel's "All" range.
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
	// ownsSession, not project access alone: a shared Team agent's chats
	// live in the owner's agent project, which a recipient cannot reach.
	if !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	su, err := store.LoadSessionUsage(globalLayout, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	limit := contextTrendMax
	if c.Query("trend") == "all" {
		limit = 0
	}
	out := sessionContextOf(id, su, limit)
	out.CanCompact = provider.CanCompact(provider.Type(contextProviderType(sess, out.Provider)))
	if !out.CanCompact {
		out.CompactNote = provider.CompactUnsupportedNote
	}
	c.JSON(http.StatusOK, out)
}

// sessionContextOf builds the panel's answer from the ledger, every
// provider's trail cut to the last limit points (0 = all of them).
func sessionContextOf(id string, su *store.SessionUsage, limit int) SessionContextDTO {
	out := SessionContextDTO{SessionID: id, Turns: su.Turns, Totals: usagereport.TotalsOf(su.Totals)}
	// The active provider is the one that answered most recently — not
	// the session's configured provider, which may have been switched a
	// second ago and not yet run a turn. The meter has to describe a
	// window that actually exists.
	active := activeContextProvider(su.Providers)
	for name, p := range su.Providers {
		if p == nil {
			continue
		}
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
		row.ContextSeriesDTO = seriesOf(p, row.Totals.Total, limit)
		out.Providers = append(out.Providers, row)
		if name == active {
			out.Provider, out.Model = name, p.Model
			out.Used, out.Window = p.ContextUsed, p.ContextWindow
			out.AutoCompact = p.AutoCompact
			out.Pct = row.Pct
			out.ContextSeriesDTO = row.ContextSeriesDTO
		}
	}
	sortContextProviders(out.Providers, out.Provider)
	return out
}

// seriesOf is one provider's trail, the last limit points (0 = all).
func seriesOf(p *store.ProviderUsage, total, limit int) ContextSeriesDTO {
	start := trendStart(len(p.Series), limit)
	out := ContextSeriesDTO{TrendTotal: len(p.Series)}
	out.Trend, out.TrendAt = trendOf(p.Series, start)
	// The spend behind each point. It needs the provider's exact total
	// to anchor against — hovering a point once reported "total 0" for
	// every point on every session because this was never called.
	out.TrendSpent = trendSpentOf(p.Series, total, start)
	out.TrendTurn = trendTurnOf(p.Series, p.Turns, start)
	out.TrendSent, out.TrendCost = trendSendOf(p.Series, start)
	return out
}

// trendStart is the index of the first point returned out of n stored.
func trendStart(n, limit int) int {
	if limit > 0 && n > limit {
		return n - limit
	}
	return 0
}

// levelOnly reports a point that records a level and nothing else: a
// turn that ended without usage, or a compaction. Turn points always
// carry a model (UnknownModel when the vendor named none); points older
// than that field still carry their flows.
func levelOnly(pt store.UsagePoint) bool {
	return pt.Model == "" && pointTokens(pt) == 0 && pt.CostUSD == 0
}

// trendTurnOf numbers each point with the provider's real turn, counted
// back from its turn total: the trail is capped, so counting forward
// from the first surviving point would restart at 1. Level-only points
// get nil and do not take a number.
func trendTurnOf(series []store.UsagePoint, turns, start int) []*int {
	if len(series) == 0 {
		return nil
	}
	out := make([]*int, len(series)-start)
	turn := turns
	for i := len(series) - 1; i >= start; i-- {
		if levelOnly(series[i]) {
			continue
		}
		if turn > 0 {
			n := turn
			out[i-start] = &n
		}
		turn--
	}
	return out
}

// trendSendOf returns what each turn sent and cost; nil for a level-only
// point, which spent nothing it could report.
func trendSendOf(series []store.UsagePoint, start int) ([]*int, []*float64) {
	if len(series) == 0 {
		return nil, nil
	}
	sent := make([]*int, len(series)-start)
	cost := make([]*float64, len(series)-start)
	for i, pt := range series[start:] {
		if levelOnly(pt) {
			continue
		}
		n, c := pointTokens(pt), pt.CostUSD
		sent[i], cost[i] = &n, &c
	}
	return sent, cost
}

// activeContextProvider names the provider whose window the meter shows.
//
// Newest LastAt wins, as before. What is new is that a provider which has
// never FINISHED a turn still counts when it already reported a level:
// LastAt is only written when a turn completes, so a run still on its
// first turn had every field of the meter blanked while the row right
// underneath it carried a real reading. Sub-agents live in that state —
// most of them are looked at while they are still working.
//
// Ties (and the all-zero case) break on the provider name, because map
// iteration order is random and a meter that names a different provider
// on each poll is worse than one that names an arbitrary but stable one.
func activeContextProvider(ps map[string]*store.ProviderUsage) string {
	var active string
	var newest time.Time
	for name, p := range ps {
		if p == nil {
			continue
		}
		switch {
		case active == "", p.LastAt.After(newest):
		case p.LastAt.Equal(newest) && name < active:
		default:
			continue
		}
		active, newest = name, p.LastAt
	}
	return active
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
func trendOf(series []store.UsagePoint, start int) ([]int, []string) {
	if len(series) == 0 {
		return nil, nil
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
func trendSpentOf(series []store.UsagePoint, total, start int) []int {
	if len(series) == 0 {
		return nil
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
