package agents

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/pkg/tool"
)

// api_usage.go serves the token ledger: what each provider burned, which
// projects and people it was burned for, and where a given provider is
// actually being used.
//
// The numbers come from per-session usage.json files written by the
// store. Rolling them up means reading one small file per session, and
// there are thousands of sessions on a busy host — so the roll-up is
// cached for a few seconds. Usage is an analytics view, not a control
// surface: a reading that is ten seconds stale costs nobody anything,
// while re-walking the whole session tree on every poll would show up as
// disk churn on a 2-vCPU box.

// usageRollupTTL is how long a computed roll-up is reused. Short enough
// that a finished turn shows up while the user is still looking at the
// page, long enough that a dashboard polling every second does not walk
// the session tree every second.
const usageRollupTTL = 15 * time.Second

var tokenLedgerCache struct {
	mu   sync.Mutex
	at   time.Time
	roll store.UsageRollup
}

// loadUsageRollup returns the cached roll-up, recomputing when stale.
func loadUsageRollup(layout config.Layout, force bool) (store.UsageRollup, error) {
	tokenLedgerCache.mu.Lock()
	defer tokenLedgerCache.mu.Unlock()
	if !force && time.Since(tokenLedgerCache.at) < usageRollupTTL && tokenLedgerCache.at != (time.Time{}) {
		return tokenLedgerCache.roll, nil
	}
	ids, err := session.ListAll(layout)
	if err != nil {
		return store.UsageRollup{}, err
	}
	roll, err := store.AggregateUsage(layout, ids, func(id string) store.SessionTags {
		s, err := session.Load(layout, id)
		if err != nil {
			return store.SessionTags{}
		}
		return store.SessionTags{ProjectID: s.Meta.ProjectID, UserID: s.Meta.UserID}
	})
	if err != nil {
		return store.UsageRollup{}, err
	}
	tokenLedgerCache.at = time.Now()
	tokenLedgerCache.roll = roll
	return roll, nil
}

// UsageTotalsDTO mirrors store.UsageTotals plus the derived numbers a UI
// would otherwise recompute in three places.
type UsageTotalsDTO struct {
	Input      int     `json:"input"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Output     int     `json:"output"`
	Total      int     `json:"total"`
	CostUSD    float64 `json:"cost_usd"`
	// CacheHitPct is cache reads as a share of all input tokens — the
	// single most actionable number here, because a collapsing hit rate
	// is what turns a cheap session expensive.
	CacheHitPct float64 `json:"cache_hit_pct"`
}

func usageTotalsDTO(t store.UsageTotals) UsageTotalsDTO {
	in := t.Input + t.CacheRead + t.CacheWrite
	d := UsageTotalsDTO{
		Input:      t.Input,
		CacheRead:  t.CacheRead,
		CacheWrite: t.CacheWrite,
		Output:     t.Output,
		Total:      in + t.Output,
		CostUSD:    t.CostUSD,
	}
	if in > 0 {
		d.CacheHitPct = float64(t.CacheRead) / float64(in) * 100
	}
	return d
}

// UsageReportDTO is the whole-fleet answer: totals, and the same totals
// sliced by provider, project and user.
type UsageReportDTO struct {
	Totals   UsageTotalsDTO `json:"totals"`
	Turns    int            `json:"turns"`
	Sessions int            `json:"sessions"`

	ByProvider []UsageSliceDTO `json:"by_provider"`
	ByProject  []UsageSliceDTO `json:"by_project"`
	ByUser     []UsageSliceDTO `json:"by_user"`
}

// UsageSliceDTO is one row of a breakdown, sorted by cost then tokens so
// the expensive row is always first.
type UsageSliceDTO struct {
	Key      string         `json:"key"`
	Label    string         `json:"label,omitempty"`
	Totals   UsageTotalsDTO `json:"totals"`
	Sessions int            `json:"sessions,omitempty"`
	// Share is this row's percentage of the report total, so a bar can be
	// drawn without the client summing the list first.
	Share float64 `json:"share"`
}

// usageSlices turns a keyed map into sorted rows. `label` names the row
// for a reader — a project title, a person's name — and may be nil when
// the key already reads as a name (the provider key does).
func usageSlices(m map[string]store.UsageTotals, grand int, label func(string) string) []UsageSliceDTO {
	out := make([]UsageSliceDTO, 0, len(m))
	for k, v := range m {
		d := usageTotalsDTO(v)
		row := UsageSliceDTO{Key: k, Totals: d}
		if label != nil {
			row.Label = label(k)
		}
		if grand > 0 {
			row.Share = float64(d.Total) / float64(grand) * 100
		}
		out = append(out, row)
	}
	sortUsageSlices(out)
	return out
}

// projectLabel names a project the way the sidebar does. A spend row is
// read by a person deciding where the money went, and a UUID answers
// nothing; a project that has since been deleted still has spend against
// it, so it keeps its id rather than disappearing from the report.
func projectLabel(layout config.Layout, id string) string {
	if id == "" {
		return ""
	}
	if p, err := project.Load(layout, id); err == nil && p.Meta.Name != "" {
		return p.Meta.Name
	}
	return ""
}

// userLabel resolves a wick user id to a name (falling back to the email
// inside channelOwnerNames). Empty when the user is gone — the row then
// shows its id, which is still the truth about who spent it.
func userLabel(names map[string]string, id string) string {
	if n := names[id]; n != "" && n != id {
		return n
	}
	return ""
}

// sortUsageSlices orders by cost, then by tokens, then by key — cost
// first because that is the question being asked, key last so the order
// is stable when two rows tie (a list that reshuffles between polls is
// unreadable).
func sortUsageSlices(rows []UsageSliceDTO) {
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			less := b.Totals.CostUSD > a.Totals.CostUSD ||
				(b.Totals.CostUSD == a.Totals.CostUSD && b.Totals.Total > a.Totals.Total) ||
				(b.Totals.CostUSD == a.Totals.CostUSD && b.Totals.Total == a.Totals.Total && b.Key < a.Key)
			if !less {
				break
			}
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
}

// apiUsageReport serves GET /api/providers/usage — the fleet-wide report.
func apiUsageReport(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if !requireProviderMenu(c) {
		return
	}
	layout := globalLayout
	roll, err := loadUsageRollup(layout, c.Query("refresh") == "1")
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	grand := usageTotalsDTO(roll.Totals).Total
	names := channelOwnerNames() // cached; one query for the whole report
	rep := UsageReportDTO{
		Totals:     usageTotalsDTO(roll.Totals),
		Turns:      roll.Turns,
		Sessions:   roll.Sessions,
		ByProvider: usageSlices(roll.ByProvider, grand, nil),
		ByProject:  usageSlices(roll.ByProject, grand, func(id string) string { return projectLabel(layout, id) }),
		ByUser:     usageSlices(roll.ByUser, grand, func(id string) string { return userLabel(names, id) }),
	}
	for i, row := range rep.ByProvider {
		rep.ByProvider[i].Sessions = len(roll.ProviderSessions[row.Key])
	}
	c.JSON(http.StatusOK, rep)
}

// ProviderUsageDetailDTO answers "where is provider A used, and what did
// it cost" for one provider.
type ProviderUsageDetailDTO struct {
	Provider string         `json:"provider"`
	Totals   UsageTotalsDTO `json:"totals"`
	Sessions []string       `json:"sessions"`
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
	roll, err := loadUsageRollup(globalLayout, false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, ProviderUsageDetailDTO{
		Provider: key,
		Totals:   usageTotalsDTO(roll.ByProvider[key]),
		Sessions: roll.ProviderSessions[key],
	})
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
	Turns  int            `json:"turns"`
	Totals UsageTotalsDTO `json:"totals"`
	// Providers lists every provider this session used, newest reading
	// first for the active one.
	Providers []SessionContextProviderDTO `json:"providers"`
	// Trend is the recent per-turn context level for the active provider,
	// oldest first — the rise and fall the ring alone cannot show.
	Trend []int `json:"trend,omitempty"`
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
	Provider string         `json:"provider"`
	Model    string         `json:"model,omitempty"`
	Used     int            `json:"used"`
	Window   int            `json:"window"`
	Pct      float64        `json:"pct"`
	Turns    int            `json:"turns"`
	Totals   UsageTotalsDTO `json:"totals"`
	LastAt   string         `json:"last_at,omitempty"`
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

	out := SessionContextDTO{SessionID: id, Turns: su.Turns, Totals: usageTotalsDTO(su.Totals)}
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
			Totals:   usageTotalsDTO(p.UsageTotals),
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
			out.Trend = trendOf(p.Series)
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

func trendOf(series []store.UsagePoint) []int {
	if len(series) == 0 {
		return nil
	}
	start := 0
	if len(series) > contextTrendMax {
		start = len(series) - contextTrendMax
	}
	out := make([]int, 0, len(series)-start)
	for _, pt := range series[start:] {
		out = append(out, pt.ContextUsed)
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
