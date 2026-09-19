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
	Output      int       `json:"output,omitempty"`
	CostUSD     float64   `json:"cost_usd,omitempty"`
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
	// session ids per provider, newest first.
	ProviderSessions map[string][]string `json:"provider_sessions"`
}

// AggregateUsage walks every session ledger and rolls it up by provider,
// project and user.
//
// tags resolves a session id to its project/user; a session it cannot
// resolve still counts toward the provider totals, because "which
// provider burned this" is knowable from the ledger alone and dropping
// the row would quietly understate the bill.
func AggregateUsage(layout config.Layout, sessionIDs []string, tags func(sessionID string) SessionTags) (UsageRollup, error) {
	out := UsageRollup{
		ByProvider:       map[string]UsageTotals{},
		ByProject:        map[string]UsageTotals{},
		ByUser:           map[string]UsageTotals{},
		ProviderSessions: map[string][]string{},
	}
	for _, id := range sessionIDs {
		su, err := LoadSessionUsage(layout, id)
		if err != nil {
			return out, err
		}
		if len(su.Providers) == 0 {
			continue
		}
		out.Sessions++
		out.Turns += su.Turns
		out.Totals.Add(su.Totals)

		var t SessionTags
		if tags != nil {
			t = tags(id)
		}
		for name, p := range su.Providers {
			agg := out.ByProvider[name]
			agg.Add(p.UsageTotals)
			out.ByProvider[name] = agg
			out.ProviderSessions[name] = append(out.ProviderSessions[name], id)

			if t.ProjectID != "" {
				v := out.ByProject[t.ProjectID]
				v.Add(p.UsageTotals)
				out.ByProject[t.ProjectID] = v
			}
			if t.UserID != "" {
				v := out.ByUser[t.UserID]
				v.Add(p.UsageTotals)
				out.ByUser[t.UserID] = v
			}
		}
	}
	for name := range out.ProviderSessions {
		sort.Strings(out.ProviderSessions[name])
	}
	return out, nil
}
