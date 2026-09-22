// Package usagereport builds the token ledger read model: what each
// provider burned, which projects and people it was burned for, and
// where a given provider is actually used.
//
// It lives here, rather than inside the providers page that first needed
// it, because the same ledger is read from two places that have nothing
// else in common — the Svelte providers UI and the server-rendered admin
// analytics page. Two renderers over one report is fine; two reports
// that "should" agree is how a host ends up with a $29.08 on one page
// and a $31.40 on another, with nobody able to say which is wrong.
//
// Everything here is derived from the per-session usage.json files. The
// roll-up walks one small file per session, so it is cached per window:
// an analytics view that is a few seconds stale costs nobody anything,
// while re-walking thousands of sessions on every poll shows up as disk
// churn on a small box.
package usagereport

import (
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
)

// CacheTTL is how long a computed roll-up is reused. Short enough that a
// finished turn shows up while the user is still looking at the page,
// long enough that a dashboard polling every second does not walk the
// session tree every second.
const CacheTTL = 15 * time.Second

// Totals mirrors store.UsageTotals plus the two derived numbers every
// renderer would otherwise recompute for itself.
type Totals struct {
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

// TotalsOf derives the presentation totals from the stored flows.
func TotalsOf(t store.UsageTotals) Totals {
	in := t.Input + t.CacheRead + t.CacheWrite
	d := Totals{
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

// Slice is one row of a breakdown, sorted by cost then tokens so the
// expensive row is always first.
type Slice struct {
	Key      string `json:"key"`
	Label    string `json:"label,omitempty"`
	Totals   Totals `json:"totals"`
	Sessions int    `json:"sessions,omitempty"`
	// Turns is how many turns this row ran. Cost answers "what is
	// expensive"; turns answer "what do we actually use" — and on a flat
	// -rate plan, where every cost is zero, turns are the only answer
	// there is.
	Turns int `json:"turns,omitempty"`
	// Group is the row this one hangs under: for a model row, the
	// provider it was reached through. The same model id through two
	// providers is two rows, and without this the UI cannot say why.
	Group string `json:"group,omitempty"`
	// Share is this row's percentage of the report total, so a bar can be
	// drawn without the client summing the list first.
	Share float64 `json:"share"`
}

// Report is the whole-fleet answer for one window.
type Report struct {
	Window      string   `json:"window"`
	WindowLabel string   `json:"window_label"`
	Since       string   `json:"since,omitempty"`
	Until       string   `json:"until,omitempty"`
	Windows     []Option `json:"windows"`

	Totals   Totals `json:"totals"`
	Turns    int    `json:"turns"`
	Sessions int    `json:"sessions"`

	ByProvider []Slice `json:"by_provider"`
	// ByModel goes one level below the provider: a provider is a door,
	// and which model answered behind it is what decides both the bill
	// and whether the answer was any good. Rows are (provider, model)
	// pairs — see store.ModelRowKey.
	ByModel   []Slice `json:"by_model"`
	ByProject []Slice `json:"by_project"`
	ByUser    []Slice `json:"by_user"`

	// Partial says the window could not be reconstructed in full for
	// every session — see store.UsageRollup.Partial. Carried into the
	// DTO so the UI can say "at least" instead of implying a total.
	Partial bool   `json:"partial,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Option is a selectable window as a client should render it.
type Option struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// SessionUse is one line of "where is this provider used": which
// conversation, whose, under what project, and what it spent there.
type SessionUse struct {
	ID          string `json:"id"`
	Label       string `json:"label,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	UserID      string `json:"user_id,omitempty"`
	UserName    string `json:"user_name,omitempty"`
	LastAt      string `json:"last_at,omitempty"`
	Turns       int    `json:"turns,omitempty"`
	Totals      Totals `json:"totals"`
}

// ProviderReport answers "what did THIS provider cost, and where was it
// used" for one provider key ("claude/default").
type ProviderReport struct {
	Provider    string   `json:"provider"`
	Window      string   `json:"window"`
	WindowLabel string   `json:"window_label"`
	Since       string   `json:"since,omitempty"`
	Windows     []Option `json:"windows"`

	Totals Totals `json:"totals"`
	Turns  int    `json:"turns"`
	// ByModel is this provider's own spend split by model — the same
	// rows the fleet report carries, narrowed to this one door.
	ByModel  []Slice      `json:"by_model,omitempty"`
	Sessions []SessionUse `json:"sessions"`

	Partial bool   `json:"partial,omitempty"`
	Note    string `json:"note,omitempty"`
}

// partialNote is printed verbatim by every surface, so the caveat is
// worded once. It matters: a windowed figure is rebuilt from a capped
// per-turn trail, and a long-running session can have lost its oldest
// points.
const partialNote = "Some sessions ran more turns than their per-turn trail keeps, " +
	"so figures for this range are a floor, not a total. All time is exact."

// Scope narrows the ledger the way a page's filter bar does: to certain
// channels, or to specific bots within them. Empty means everything.
//
// It exists so a page with one filter bar has ONE answer below it. A
// token figure that ignored the channel filter would sit next to a chart
// that honoured it, and the two would disagree without either being
// wrong — the worst kind of dashboard bug, because nothing looks broken.
type Scope struct {
	Channels []string
	// Instances narrows to specific bots ("slack:<owner id>"). It only
	// means anything when the Builder's Tags resolver fills
	// SessionTags.Instance — the default one cannot, so a caller using it
	// must leave this empty rather than get an empty report that reads
	// like a real answer.
	Instances []string
}

// Empty reports whether this scope narrows anything.
func (sc Scope) Empty() bool { return len(sc.Channels) == 0 && len(sc.Instances) == 0 }

// Keep is the predicate the roll-up applies per session.
func (sc Scope) Keep(t store.SessionTags) bool {
	if len(sc.Channels) > 0 && !contains(sc.Channels, t.Channel) {
		return false
	}
	if len(sc.Instances) > 0 && !contains(sc.Instances, t.Instance) {
		return false
	}
	return true
}

// Key identifies the scope in the cache, so two different filters cannot
// be served each other's answer.
func (sc Scope) Key() string {
	if sc.Empty() {
		return ""
	}
	return "|ch:" + strings.Join(sc.Channels, ",") + "|in:" + strings.Join(sc.Instances, ",")
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Builder computes and caches reports for one host.
//
// UserName resolves a wick user id to a name; it comes from the caller
// because the mapping lives in the database and this package must stay
// free of it. Nil is fine — rows then show the id, which is still the
// truth about who spent it.
type Builder struct {
	Layout   config.Layout
	UserName func(id string) string
	// Tags resolves a session id to what the roll-up groups and filters
	// by. Optional: the default reads the session from disk. A caller
	// that already keeps a cache of session facts (the admin analytics
	// page does) should supply it and save the second walk — same data,
	// read once.
	Tags func(id string) store.SessionTags

	mu    sync.Mutex
	cache map[string]cacheEntry
	now   func() time.Time
}

type cacheEntry struct {
	at   time.Time
	roll store.UsageRollup
}

// New returns a builder over the given layout.
func New(layout config.Layout, userName func(id string) string) *Builder {
	return &Builder{Layout: layout, UserName: userName, cache: map[string]cacheEntry{}, now: time.Now}
}

// Rollup returns the cached roll-up for a window, recomputing when stale
// or when force is set (the explicit Refresh button, never a poll).
func (b *Builder) Rollup(w Window, sc Scope, force bool) (store.UsageRollup, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cache == nil {
		b.cache = map[string]cacheEntry{}
	}
	if b.now == nil {
		b.now = time.Now
	}
	key := w.CacheKey() + sc.Key()
	if e, ok := b.cache[key]; ok && !force && b.now().Sub(e.at) < CacheTTL {
		return e.roll, nil
	}
	ids, err := session.ListAll(b.Layout)
	if err != nil {
		return store.UsageRollup{}, err
	}
	tags := b.Tags
	if tags == nil {
		tags = b.loadTags
	}
	roll, err := store.AggregateUsageFiltered(b.Layout, ids, tags, store.UsageFilter{
		Since: w.Since, Until: w.Until, Keep: sc.Keep,
	})
	if err != nil {
		return store.UsageRollup{}, err
	}
	b.cache[key] = cacheEntry{at: b.now(), roll: roll}
	return roll, nil
}

// Report builds the fleet-wide report for a window.
func (b *Builder) Report(w Window, sc Scope, force bool) (Report, error) {
	roll, err := b.Rollup(w, sc, force)
	if err != nil {
		return Report{}, err
	}
	grand := TotalsOf(roll.Totals).Total
	rep := Report{
		Window:      w.Key,
		WindowLabel: w.Label,
		Windows:     options(b.clock()),
		Totals:      TotalsOf(roll.Totals),
		Turns:       roll.Turns,
		Sessions:    roll.Sessions,
		ByProvider:  Slices(roll.ByProvider, grand, nil),
		ByModel:     ModelSlices(roll, grand, ""),
		ByProject:   Slices(roll.ByProject, grand, func(id string) string { return b.projectName(id) }),
		ByUser:      Slices(roll.ByUser, grand, b.userName),
		Partial:     roll.Partial,
	}
	if !w.Since.IsZero() {
		rep.Since = w.Since.UTC().Format(time.RFC3339)
	}
	if !w.Until.IsZero() {
		rep.Until = w.Until.UTC().Format(time.RFC3339)
	}
	if roll.Partial {
		rep.Note = partialNote
	}
	for i, row := range rep.ByProvider {
		rep.ByProvider[i].Sessions = len(roll.ProviderSessions[row.Key])
		// Turns as well as sessions: the model rows below carry a turn
		// count, and a provider row without one cannot be compared to the
		// models sitting under it.
		turns := 0
		for _, use := range roll.ProviderSessions[row.Key] {
			turns += use.Turns
		}
		rep.ByProvider[i].Turns = turns
	}
	return rep, nil
}

// Provider builds the one-provider report, naming every session it was
// used in. A bare session id told a reader nothing — whose work it was
// and which project it belonged to is the whole question behind "where
// is this provider used".
func (b *Builder) Provider(key string, w Window, sc Scope, force bool) (ProviderReport, error) {
	roll, err := b.Rollup(w, sc, force)
	if err != nil {
		return ProviderReport{}, err
	}
	out := ProviderReport{
		Provider:    key,
		Window:      w.Key,
		WindowLabel: w.Label,
		Windows:     options(b.clock()),
		Totals:      TotalsOf(roll.ByProvider[key]),
		ByModel:     ModelSlices(roll, TotalsOf(roll.ByProvider[key]).Total, key),
		Partial:     roll.Partial,
		Sessions:    []SessionUse{},
	}
	if !w.Since.IsZero() {
		out.Since = w.Since.UTC().Format(time.RFC3339)
	}
	if roll.Partial {
		out.Note = partialNote
	}
	for _, use := range roll.ProviderSessions[key] {
		t := roll.SessionTagsByID[use.ID]
		row := SessionUse{
			ID:        use.ID,
			Label:     t.Label,
			ProjectID: t.ProjectID,
			UserID:    t.UserID,
			Turns:     use.Turns,
			Totals:    TotalsOf(use.Totals),
		}
		if t.ProjectID != "" {
			row.ProjectName = b.projectName(t.ProjectID)
		}
		if t.UserID != "" {
			row.UserName = b.userName(t.UserID)
		}
		if !use.LastAt.IsZero() {
			row.LastAt = use.LastAt.UTC().Format(time.RFC3339)
		}
		out.Turns += use.Turns
		out.Sessions = append(out.Sessions, row)
	}
	return out, nil
}

// loadTags is the default session resolver: read it from disk. The
// channel is the session's origin, the same string the analytics page
// groups by, so a filter means the same thing on both.
func (b *Builder) loadTags(id string) store.SessionTags {
	s, err := session.Load(b.Layout, id)
	if err != nil {
		return store.SessionTags{}
	}
	ch := string(s.Meta.Origin)
	if ch == "" {
		ch = "ui"
	}
	return store.SessionTags{
		ProjectID: s.Meta.ProjectID,
		UserID:    s.Meta.UserID,
		Label:     s.Meta.Label,
		Channel:   ch,
	}
}

func (b *Builder) clock() time.Time {
	if b.now == nil {
		return time.Now()
	}
	return b.now()
}

// projectName names a project the way the sidebar does. A spend row is
// read by a person deciding where the money went, and a UUID answers
// nothing; a project that has since been deleted still has spend against
// it, so it keeps its id rather than disappearing from the report.
func (b *Builder) projectName(id string) string {
	if id == "" {
		return ""
	}
	if p, err := project.Load(b.Layout, id); err == nil && p.Meta.Name != "" {
		return p.Meta.Name
	}
	return ""
}

func (b *Builder) userName(id string) string {
	if id == "" || b.UserName == nil {
		return ""
	}
	if n := b.UserName(id); n != "" && n != id {
		return n
	}
	return ""
}

func options(now time.Time) []Option {
	ws := Windows(now)
	out := make([]Option, 0, len(ws))
	for _, w := range ws {
		out = append(out, Option{Key: w.Key, Label: w.Label})
	}
	return out
}

// Slices turns a keyed map into sorted rows. `label` names the row for a
// reader — a project title, a person's name — and may be nil when the
// key already reads as a name (the provider key does).
func Slices(m map[string]store.UsageTotals, grand int, label func(string) string) []Slice {
	out := make([]Slice, 0, len(m))
	for k, v := range m {
		d := TotalsOf(v)
		row := Slice{Key: k, Totals: d}
		if label != nil {
			row.Label = label(k)
		}
		if grand > 0 {
			row.Share = float64(d.Total) / float64(grand) * 100
		}
		out = append(out, row)
	}
	SortSlices(out)
	return out
}

// SortSlices orders by cost, then by tokens, then by key — cost first
// because that is the question being asked, key last so the order is
// stable when two rows tie (a list that reshuffles between polls is
// unreadable).
func SortSlices(rows []Slice) {
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

// ModelSlices turns the roll-up's (provider, model) buckets into rows.
//
// `provider` narrows to one door, or is empty for the whole fleet. The
// row's Label is the model id — the thing anyone reading this actually
// asked about — and Group names the provider it came through, so a model
// reached through two accounts reads as two rows rather than as a
// mystery duplicate.
func ModelSlices(roll store.UsageRollup, grand int, provider string) []Slice {
	out := make([]Slice, 0, len(roll.ByModel))
	for k, v := range roll.ByModel {
		p, model := store.SplitModelRowKey(k)
		if provider != "" && p != provider {
			continue
		}
		d := TotalsOf(v)
		row := Slice{Key: k, Label: model, Group: p, Totals: d, Turns: roll.ModelTurns[k]}
		if grand > 0 {
			row.Share = float64(d.Total) / float64(grand) * 100
		}
		out = append(out, row)
	}
	SortSlices(out)
	return out
}
