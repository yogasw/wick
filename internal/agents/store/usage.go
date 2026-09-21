package store

import (
	"os"
	"sort"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/storage"
)

// usage.go keeps the token ledger of a session in ONE file —
// <SessionDir>/usage.json — rewritten after each turn rather than
// stamped onto every message.
//
// Why one file instead of a field per turn: the questions people
// actually ask are aggregate ones ("what did project A spend", "where is
// provider A being used", "how much is this user costing"), and
// answering those from conversation.jsonl means parsing every message of
// every session. A small per-session file answers them by reading one
// document per session, and it stays small because the per-turn detail
// collapses into totals as it arrives.
//
// Why keyed by provider: a session can switch providers mid-conversation,
// and a total that blends claude tokens with codex tokens is not a
// number anyone can act on — the prices differ, the windows differ, and
// "which one did we actually use here" is itself the question. Each
// provider keeps its own bucket, and the file-level Totals are only ever
// a convenience sum of the token counts.
//
// The Series is what survives of the per-turn view: a bounded trail of
// small points so the rise and fall is still visible without keeping a
// record per message. It is capped, and the oldest points fall off
// first — a session that runs for days keeps its totals exact and its
// curve recent.

// UsageSeriesMax caps how many per-turn points a session keeps. At ~40
// bytes a point this is a few tens of KB in the worst case, which is
// cheap enough to rewrite every turn and small enough to load in a UI.
const UsageSeriesMax = 500

// SessionUsage is the whole ledger for one session.
type SessionUsage struct {
	SessionID string `json:"session_id"`
	// Providers is keyed by the provider snapshot ("type/name") that was
	// active when the turn ran — the same string a turn records.
	Providers map[string]*ProviderUsage `json:"providers"`
	// Totals sums the token counters across providers. Deliberately
	// carries no context level: "how full is the window" belongs to one
	// provider's conversation, not to a blend of several.
	Totals    UsageTotals `json:"totals"`
	Turns     int         `json:"turns"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// UsageTotals is the additive part of token accounting — flows, not
// levels. Every field here is safe to sum across turns, sessions and
// providers.
type UsageTotals struct {
	Input      int     `json:"input"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Output     int     `json:"output"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
}

// Add folds another set of flows into this one.
func (t *UsageTotals) Add(o UsageTotals) {
	t.Input += o.Input
	t.CacheRead += o.CacheRead
	t.CacheWrite += o.CacheWrite
	t.Output += o.Output
	t.CostUSD += o.CostUSD
}

// ProviderUsage is one provider's bucket inside a session.
type ProviderUsage struct {
	UsageTotals
	Turns int `json:"turns"`
	// Model is the last model id the vendor billed under this provider.
	Model string `json:"model,omitempty"`
	// ContextUsed / ContextWindow are the LAST reading, not a sum — how
	// full the window was when this provider last answered. Window is 0
	// when the CLI does not report a limit (codex does not).
	ContextUsed   int `json:"context_used,omitempty"`
	ContextWindow int `json:"context_window,omitempty"`

	FirstAt time.Time    `json:"first_at"`
	LastAt  time.Time    `json:"last_at"`
	Series  []UsagePoint `json:"series,omitempty"`
}

// UsagePoint is one turn, kept small on purpose — this is the shape that
// repeats hundreds of times per session.
type UsagePoint struct {
	At          time.Time `json:"at"`
	ContextUsed int       `json:"context_used,omitempty"`
	Input       int       `json:"input,omitempty"`
	CacheRead   int       `json:"cache_read,omitempty"`
	// CacheWrite is here so a windowed sum ("what did today cost")
	// reconstructs the same figure the all-time totals carry. Points
	// written before this field existed simply have none, which makes a
	// window over old turns understate cache writes rather than lie
	// about which turns are in it.
	CacheWrite int     `json:"cache_write,omitempty"`
	Output     int     `json:"output,omitempty"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
}

// recordUsage folds one turn's reading into the session ledger.
//
// Best-effort by design: a failure here must never break the turn that
// produced it, so the caller logs nothing and carries on. Losing one
// point from an analytics trail is not worth failing a conversation.
func (s *Store) recordUsage(u *event.TokenUsage, at time.Time) error {
	if u == nil {
		return nil
	}
	path := s.layout.SessionUsage(s.sessionID)
	su, err := loadUsageFile(path)
	if err != nil {
		return err
	}
	su.SessionID = s.sessionID

	key := s.provider
	if key == "" {
		key = "unknown"
	}
	p := su.Providers[key]
	if p == nil {
		p = &ProviderUsage{FirstAt: at}
		su.Providers[key] = p
	}

	flows := UsageTotals{
		Input:      u.Input,
		CacheRead:  u.CacheRead,
		CacheWrite: u.CacheWrite,
		Output:     u.Output,
		CostUSD:    u.CostUSD,
	}
	p.UsageTotals.Add(flows)
	p.Turns++
	p.LastAt = at
	if u.Model != "" {
		p.Model = u.Model
	}
	// Levels replace, never accumulate.
	if u.ContextUsed > 0 {
		p.ContextUsed = u.ContextUsed
	}
	if u.Window > 0 {
		p.ContextWindow = u.Window
	}
	// A turn whose level could not be read (codex with no rollout to
	// consult) carries the last known one into the series. Plotting the
	// zero instead would draw a cliff to nothing and back — a fall that
	// never happened, in the one chart people read for falls.
	level := u.ContextUsed
	if level <= 0 {
		level = p.ContextUsed
	}
	p.Series = append(p.Series, UsagePoint{
		At:          at,
		ContextUsed: level,
		Input:       u.Input,
		CacheRead:   u.CacheRead,
		CacheWrite:  u.CacheWrite,
		Output:      u.Output,
		CostUSD:     u.CostUSD,
	})
	if n := len(p.Series); n > UsageSeriesMax {
		p.Series = append(p.Series[:0], p.Series[n-UsageSeriesMax:]...)
	}

	su.Totals.Add(flows)
	su.Turns++
	su.UpdatedAt = at
	return storage.WriteJSON(path, su)
}

// recordCompaction drops the recorded context level to what survived a
// compaction.
//
// Without this the meter keeps showing the pre-compaction number until
// the next turn happens to finish — so the transcript says "94.4k →
// 6.8k" while the ring beside it still reads 94k, and the one moment the
// number matters most is the one moment it is wrong. Only the LEVEL
// moves: compaction spends tokens rather than refunding them, and those
// are reported by the turn that paid for them.
func (s *Store) recordCompaction(info *event.CompactionInfo, at time.Time) error {
	if info == nil || info.PostTokens <= 0 {
		return nil
	}
	path := s.layout.SessionUsage(s.sessionID)
	su, err := loadUsageFile(path)
	if err != nil {
		return err
	}
	su.SessionID = s.sessionID
	key := s.provider
	if key == "" {
		key = "unknown"
	}
	p := su.Providers[key]
	if p == nil {
		// A compaction before this provider ever reported usage: record
		// the level anyway, so the meter starts from the truth.
		p = &ProviderUsage{FirstAt: at}
		su.Providers[key] = p
	}
	p.ContextUsed = info.PostTokens
	p.LastAt = at
	// A point on the series too, so the curve shows the cliff instead of
	// jumping silently between two turns.
	p.Series = append(p.Series, UsagePoint{At: at, ContextUsed: info.PostTokens})
	if n := len(p.Series); n > UsageSeriesMax {
		p.Series = append(p.Series[:0], p.Series[n-UsageSeriesMax:]...)
	}
	su.UpdatedAt = at
	return storage.WriteJSON(path, su)
}

// loadUsageFile reads a ledger, returning an empty one when the file does
// not exist yet. A corrupt file is also treated as empty rather than
// fatal: the ledger is derived data, and refusing to record anything more
// because an old write was truncated would turn a cosmetic problem into a
// permanent one.
func loadUsageFile(path string) (*SessionUsage, error) {
	su := &SessionUsage{Providers: map[string]*ProviderUsage{}}
	err := storage.ReadJSON(path, su)
	switch {
	case err == nil:
	case os.IsNotExist(err):
	default:
		su = &SessionUsage{Providers: map[string]*ProviderUsage{}}
	}
	if su.Providers == nil {
		su.Providers = map[string]*ProviderUsage{}
	}
	return su, nil
}

// LoadSessionUsage reads one session's ledger. Returns an empty ledger
// (not an error) for a session that has never recorded usage — a session
// that only ever ran on a provider which reports nothing is a normal
// state, not a failure.
func LoadSessionUsage(layout config.Layout, sessionID string) (*SessionUsage, error) {
	su, err := loadUsageFile(layout.SessionUsage(sessionID))
	if err != nil {
		return nil, err
	}
	su.SessionID = sessionID
	return su, nil
}

// SessionTags are the session attributes a roll-up groups by. They live
// in session meta.json, which this package must not import (the session
// package already depends on store), so the caller supplies them.
type SessionTags struct {
	ProjectID string
	UserID    string
	// Label is the session's own title, so a ledger row can name the
	// conversation instead of printing eight characters of uuid.
	Label string
	// Channel and Instance are where the conversation came from ("slack",
	// "ui") and which bot of that channel. They exist so a page that
	// filters by channel can filter its token figures by the same thing —
	// a page where the chart moves and the cost does not is reporting two
	// different slices side by side.
	Channel  string
	Instance string
}

// UsageFilter bounds a roll-up: in time, and by which sessions count at
// all. Keep is applied BEFORE anything is added, so every figure in the
// result describes the same slice.
type UsageFilter struct {
	Since time.Time
	Until time.Time
	Keep  func(SessionTags) bool
}

// SessionUse is one session's line in a provider's ledger: how much it
// spent there and when it last did. Ids alone answered "where is this
// provider used" and nothing else — not which of them is costing
// anything, not which are still alive.
type SessionUse struct {
	ID     string      `json:"id"`
	Totals UsageTotals `json:"totals"`
	Turns  int         `json:"turns"`
	LastAt time.Time   `json:"last_at"`
}

// UsageRollup answers the aggregate questions across sessions.
type UsageRollup struct {
	Totals   UsageTotals `json:"totals"`
	Turns    int         `json:"turns"`
	Sessions int         `json:"sessions"`

	ByProvider map[string]UsageTotals `json:"by_provider"`
	ByProject  map[string]UsageTotals `json:"by_project"`
	ByUser     map[string]UsageTotals `json:"by_user"`
	// ProviderSessions answers "where is provider A actually used" —
	// sessions per provider, most recently used first.
	ProviderSessions map[string][]SessionUse `json:"provider_sessions"`
	// SessionTagsByID keeps the project/user of every session that made
	// it into the roll-up, so a caller can name a row without loading
	// each session a second time.
	SessionTagsByID map[string]SessionTags `json:"session_tags,omitempty"`

	// Since / Until bound the roll-up; zero means unbounded on that end.
	Since time.Time `json:"since,omitempty"`
	Until time.Time `json:"until,omitempty"`
	// Partial reports that at least one session could not answer the
	// window fully, because the per-turn trail it would be summed from
	// had already dropped its oldest points. The figures are then a
	// floor, not a total — and a report that does not say so is a report
	// somebody will quote as exact.
	Partial bool `json:"partial,omitempty"`
	// PartialSessions counts them, so the note can be specific.
	PartialSessions int `json:"partial_sessions,omitempty"`
}

// TotalsSince sums this provider's flows from `since` onwards.
//
// All-time is exact: the bucket's own counters. A window has to be
// rebuilt from the per-turn trail, which is capped — so this also
// reports whether the trail actually reaches back to `since`. It is the
// difference between "this provider cost $4 today" and "at least $4",
// and only the caller knows which of those it may print.
func (p *ProviderUsage) TotalsSince(since time.Time) (totals UsageTotals, turns int, complete bool) {
	return p.TotalsBetween(since, time.Time{})
}

// TotalsBetween is TotalsSince with an upper bound as well, so a custom
// date range ("1-7 Sep") means the same thing here as on the page that
// asked for it. A zero `until` is "up to now".
func (p *ProviderUsage) TotalsBetween(since, until time.Time) (totals UsageTotals, turns int, complete bool) {
	if p == nil {
		return UsageTotals{}, 0, true
	}
	if since.IsZero() && until.IsZero() {
		return p.UsageTotals, p.Turns, true
	}
	// Nothing at all since the cut-off: complete, and empty.
	if !since.IsZero() && p.LastAt.Before(since) {
		return UsageTotals{}, 0, true
	}
	// A bucket recorded before the trail existed can only answer "some
	// of this is in the window", which is not an answer.
	if len(p.Series) == 0 {
		return UsageTotals{}, 0, p.Turns == 0
	}
	for _, pt := range p.Series {
		if !since.IsZero() && pt.At.Before(since) {
			continue
		}
		if !until.IsZero() && pt.At.After(until) {
			continue
		}
		totals.Add(UsageTotals{
			Input:      pt.Input,
			CacheRead:  pt.CacheRead,
			CacheWrite: pt.CacheWrite,
			Output:     pt.Output,
			CostUSD:    pt.CostUSD,
		})
		// Compaction writes a level-only point; counting it as a turn
		// would inflate the turn count of every compacted session.
		if pt.Input > 0 || pt.CacheRead > 0 || pt.CacheWrite > 0 || pt.Output > 0 {
			turns++
		}
	}
	// The trail is full AND starts after the cut-off: older turns fell
	// off the end, so the window is missing whatever they held.
	complete = !(len(p.Series) >= UsageSeriesMax && !since.IsZero() && p.Series[0].At.After(since))
	return totals, turns, complete
}

// AggregateUsage walks every session ledger and rolls it up by provider,
// project and user, over all time.
//
// tags resolves a session id to its project/user; a session it cannot
// resolve still counts toward the provider totals, because "which
// provider burned this" is knowable from the ledger alone and dropping
// the row would quietly understate the bill.
func AggregateUsage(layout config.Layout, sessionIDs []string, tags func(sessionID string) SessionTags) (UsageRollup, error) {
	return AggregateUsageSince(layout, sessionIDs, tags, time.Time{})
}

// AggregateUsageSince is AggregateUsage bounded in time: only what was
// spent at or after `since` counts. A zero `since` is all time and stays
// exact; a window is reconstructed from each session's per-turn trail
// (see ProviderUsage.TotalsSince), and a session whose trail cannot
// reach back that far marks the result Partial rather than silently
// under-reporting.
func AggregateUsageSince(layout config.Layout, sessionIDs []string, tags func(sessionID string) SessionTags, since time.Time) (UsageRollup, error) {
	return AggregateUsageBetween(layout, sessionIDs, tags, since, time.Time{})
}

// AggregateUsageBetween is AggregateUsageSince with an upper bound too.
func AggregateUsageBetween(layout config.Layout, sessionIDs []string, tags func(sessionID string) SessionTags, since, until time.Time) (UsageRollup, error) {
	return AggregateUsageFiltered(layout, sessionIDs, tags, UsageFilter{Since: since, Until: until})
}

// AggregateUsageFiltered is the full form: a time range plus a predicate
// over each session's tags.
func AggregateUsageFiltered(layout config.Layout, sessionIDs []string, tags func(sessionID string) SessionTags, f UsageFilter) (UsageRollup, error) {
	since, until := f.Since, f.Until
	out := UsageRollup{
		ByProvider:       map[string]UsageTotals{},
		ByProject:        map[string]UsageTotals{},
		ByUser:           map[string]UsageTotals{},
		ProviderSessions: map[string][]SessionUse{},
		SessionTagsByID:  map[string]SessionTags{},
		Since:            since,
		Until:            until,
	}
	for _, id := range sessionIDs {
		su, err := LoadSessionUsage(layout, id)
		if err != nil {
			return out, err
		}
		if len(su.Providers) == 0 {
			continue
		}

		var t SessionTags
		if tags != nil {
			t = tags(id)
		}
		if f.Keep != nil && !f.Keep(t) {
			continue
		}

		counted := false
		for name, p := range su.Providers {
			flows, turns, complete := p.TotalsBetween(since, until)
			if !complete {
				out.Partial = true
			}
			// A session that spent nothing inside the window is not in
			// the window — listing it would put a row of zeroes under
			// "Used in" for every session that ever touched the provider.
			if flows == (UsageTotals{}) && turns == 0 {
				continue
			}
			if !counted {
				out.Sessions++
				counted = true
				if !complete {
					out.PartialSessions++
				}
			}
			out.Turns += turns
			out.Totals.Add(flows)

			agg := out.ByProvider[name]
			agg.Add(flows)
			out.ByProvider[name] = agg
			out.ProviderSessions[name] = append(out.ProviderSessions[name], SessionUse{
				ID: id, Totals: flows, Turns: turns, LastAt: p.LastAt,
			})

			if t.ProjectID != "" {
				v := out.ByProject[t.ProjectID]
				v.Add(flows)
				out.ByProject[t.ProjectID] = v
			}
			if t.UserID != "" {
				v := out.ByUser[t.UserID]
				v.Add(flows)
				out.ByUser[t.UserID] = v
			}
		}
		if counted {
			out.SessionTagsByID[id] = t
		}
	}
	// Newest first: "where is this provider used" is asked about what is
	// running now, and a list sorted by id answers a question nobody has.
	for name := range out.ProviderSessions {
		rows := out.ProviderSessions[name]
		sort.SliceStable(rows, func(i, j int) bool {
			if !rows[i].LastAt.Equal(rows[j].LastAt) {
				return rows[i].LastAt.After(rows[j].LastAt)
			}
			return rows[i].ID < rows[j].ID
		})
		out.ProviderSessions[name] = rows
	}
	return out, nil
}
