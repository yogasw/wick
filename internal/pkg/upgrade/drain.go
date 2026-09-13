package upgrade

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// A wick process runs a lot of work in the background: agent turns in the
// pool, workflow runs, cron jobs, scheduled messages, sub-agent delegations.
// Draining means waiting for all of it — and the only way that stays true as
// features are added is if every new subsystem declares itself here instead of
// the drain path hard-coding a list it will silently fall behind.
//
// Contract for anything that owns background work:
//
//	upgrade.Register("workflow runs", eng.ActiveRuns)
//
// InFlight must be cheap (read a counter under a mutex) and must return 0 once
// the subsystem has nothing outstanding. Optionally add Detail to name WHAT is
// still busy, which is what turns "1 still running" into a log line an
// operator can act on.
type Work struct {
	// Name is what the operator sees in the drain log. Short, lowercase.
	Name string
	// InFlight returns how many units of work are running right now.
	InFlight func() int
	// Detail optionally identifies the outstanding units (session ids, run
	// ids). Called only while the subsystem is non-zero, and capped when
	// logged, so it may be moderately expensive.
	Detail func() []string
	// Resumable marks work that SURVIVES being interrupted because its state
	// is on disk and it picks up where it left off — an agent turn is resumed
	// with --resume on the next message. Everything else (a workflow run
	// mid-node, a cron job mid-write, a connector call) loses work when it is
	// cut off.
	//
	// The drain no longer treats the two differently: it waits for all of it
	// (see WaitSettled), because "resumable" still means somebody watches a
	// reply stop mid-sentence. The flag survives for reporting — BusyKind
	// separates them so a forced swap can say which work merely resumes and
	// which is actually lost.
	Resumable bool
}

// Tracker holds the registered subsystems. Use the package-level Register /
// Wait unless you are testing.
type Tracker struct {
	mu    sync.Mutex
	items []Work
}

// Default is the process-wide tracker. Registration is global on purpose: a
// subsystem should be able to opt in from wherever it is constructed, without
// every constructor growing a parameter it just forwards.
var Default = &Tracker{}

// Register adds a subsystem to the default tracker.
func Register(name string, inFlight func() int) {
	Default.Add(Work{Name: name, InFlight: inFlight})
}

// RegisterResumable adds a subsystem whose work survives interruption (see
// Work.Resumable) — it is waited for only briefly.
func RegisterResumable(name string, inFlight func() int, detail func() []string) {
	Default.Add(Work{Name: name, InFlight: inFlight, Detail: detail, Resumable: true})
}

// RegisterDetailed adds a subsystem that can also name its outstanding work.
func RegisterDetailed(name string, inFlight func() int, detail func() []string) {
	Default.Add(Work{Name: name, InFlight: inFlight, Detail: detail})
}

// Add registers one subsystem. Nil InFlight is ignored rather than panicking:
// a registration site is often optional wiring, and a nil there must not take
// the daemon down at boot.
func (t *Tracker) Add(w Work) {
	if w.Name == "" || w.InFlight == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Replace rather than append on a repeated name: registration happens in
	// constructors, and a process (or a test binary) that builds a subsystem
	// twice must not be reported as having twice the work.
	for i := range t.items {
		if t.items[i].Name == w.Name {
			t.items[i] = w
			return
		}
	}
	t.items = append(t.items, w)
}

// Snapshot reports the current in-flight count per subsystem, including zeros,
// so a caller can render a full picture.
func (t *Tracker) Snapshot() map[string]int {
	t.mu.Lock()
	items := make([]Work, len(t.items))
	copy(items, t.items)
	t.mu.Unlock()
	out := make(map[string]int, len(items))
	for _, w := range items {
		out[w.Name] += w.InFlight()
	}
	return out
}

// Busy renders the non-zero subsystems as "name=n" strings, sorted, with each
// one's Detail appended when it has any. Empty slice means fully drained.
func (t *Tracker) Busy() []string {
	return t.busy(func(Work) bool { return true })
}

// BusyKind renders only the non-zero subsystems whose Resumable flag matches.
func (t *Tracker) BusyKind(resumable bool) []string {
	return t.busy(func(w Work) bool { return w.Resumable == resumable })
}

func (t *Tracker) busy(keep func(Work) bool) []string {
	t.mu.Lock()
	items := make([]Work, len(t.items))
	copy(items, t.items)
	t.mu.Unlock()

	var out []string
	for _, w := range items {
		if !keep(w) {
			continue
		}
		n := w.InFlight()
		if n <= 0 {
			continue
		}
		entry := fmt.Sprintf("%s=%d", w.Name, n)
		if w.Detail != nil {
			if d := w.Detail(); len(d) > 0 {
				if len(d) > 5 {
					d = append(d[:5:5], "…")
				}
				entry += " (" + strings.Join(d, ", ") + ")"
			}
		}
		out = append(out, entry)
	}
	sort.Strings(out)
	return out
}

// BusyHuman renders the outstanding work the way a person would say it:
// counts and kinds, no identifiers. Busy() stays as it is for logs, where a
// session id is exactly what you want at 3am; a status panel is not that.
//
// Detail is kept only where it is a NAME someone recognises — a cron job, a
// workflow — never a session or request id, which wrap across three lines and
// tell a reader nothing they can act on.
func (t *Tracker) BusyHuman() []string {
	t.mu.Lock()
	items := make([]Work, len(t.items))
	copy(items, t.items)
	t.mu.Unlock()

	var out []string
	for _, w := range items {
		n := w.InFlight()
		if n <= 0 {
			continue
		}
		entry := humanWork(w.Name, n)
		if namedWork(w.Name) && w.Detail != nil {
			if d := w.Detail(); len(d) > 0 {
				if len(d) > 2 {
					d = append(d[:2:2], "…")
				}
				entry += " (" + strings.Join(d, ", ") + ")"
			}
		}
		out = append(out, entry)
	}
	sort.Strings(out)
	return out
}

// namedWork reports whether a subsystem's detail is a human-readable name
// rather than an id.
func namedWork(name string) bool {
	return name == "cron jobs" || name == "workflow runs"
}

func humanWork(name string, n int) string {
	plural := func(one, many string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, one)
		}
		return fmt.Sprintf("%d %s", n, many)
	}
	switch name {
	case "agent turns":
		return plural("agent still replying", "agents still replying")
	case "http requests":
		return plural("request still being handled", "requests still being handled")
	case "workflow runs":
		return plural("workflow run", "workflow runs")
	case "cron jobs":
		return plural("cron job", "cron jobs")
	case "connector ops":
		return plural("connector call", "connector calls")
	case "plugin calls":
		return plural("plugin call", "plugin calls")
	case "delegations":
		return plural("sub-agent working", "sub-agents working")
	case "scheduled messages":
		return plural("scheduled message being delivered", "scheduled messages being delivered")
	}
	return fmt.Sprintf("%d %s", n, name)
}

// Total is the sum of every subsystem's in-flight count.
func (t *Tracker) Total() int {
	n := 0
	for _, v := range t.Snapshot() {
		n += v
	}
	return n
}

// Wait blocks until every registered subsystem reports zero, or ctx is done.
// It logs what is still outstanding every 15s so a long drain is legible
// rather than a silent hang — the operator can see it is finishing a debug
// run, not stuck.
//
// Returns the still-busy descriptions: empty means a clean drain.
func (t *Tracker) Wait(ctx context.Context) []string {
	return t.wait(ctx, func(Work) bool { return true })
}

// WaitKind waits only for the subsystems whose Resumable flag matches, so a
// caller can give resumable and non-resumable work different deadlines.
func (t *Tracker) WaitKind(ctx context.Context, resumable bool) []string {
	return t.wait(ctx, func(w Work) bool { return w.Resumable == resumable })
}

func (t *Tracker) wait(ctx context.Context, keep func(Work) bool) []string {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	report := time.NewTicker(15 * time.Second)
	defer report.Stop()

	for {
		busy := t.busy(keep)
		if len(busy) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return t.busy(keep)
		case <-report.C:
			log.Info().Strs("outstanding", busy).Msg("drain: still waiting")
		case <-tick.C:
		}
	}
}

// WaitSettled blocks until EVERY registered subsystem has reported zero
// continuously for quiet, or ctx is done. It is the wait a handover uses.
//
// Two properties, and both matter:
//
//   - No deadline of its own. Whatever is running — an agent turn, a workflow
//     run mid-node, a cron job mid-write — is waited for until it is done,
//     however long that takes. A caller that genuinely must bound the wait
//     passes a ctx with a deadline and accepts that the work is cut off.
//   - The window is a SETTLE, not a grace. Work finishing rarely means work
//     ending: a tool result, a queued message, the next node of a workflow or
//     a sub-agent reporting back lands moments later. Going quiet for a
//     moment is not the same as being done, so the counter has to stay at
//     zero for quiet before this returns, and any new work restarts it.
//
// Returns the still-busy descriptions: empty means everything really settled.
func (t *Tracker) WaitSettled(ctx context.Context, quiet time.Duration) []string {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	report := time.NewTicker(15 * time.Second)
	defer report.Stop()

	var since time.Time // when the tracker last went fully quiet
	for {
		if Forced() {
			// A human looked at what was running and chose not to wait.
			return t.Busy()
		}
		busy := t.Busy()
		switch {
		case len(busy) > 0:
			since = time.Time{}
		case since.IsZero():
			since = time.Now()
		default:
			if time.Since(since) >= quiet {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return t.Busy()
		case <-report.C:
			if len(busy) > 0 {
				log.Info().Strs("outstanding", busy).Msg("drain: still waiting")
			}
		case <-tick.C:
		}
	}
}

// Busy / Wait / Snapshot on the default tracker.
func Busy() []string                   { return Default.Busy() }
func BusyHuman() []string              { return Default.BusyHuman() }
func BusyKind(resumable bool) []string { return Default.BusyKind(resumable) }

func WaitKind(ctx context.Context, resumable bool) []string {
	return Default.WaitKind(ctx, resumable)
}
func Snapshot() map[string]int          { return Default.Snapshot() }
func Wait(ctx context.Context) []string { return Default.Wait(ctx) }
func WaitSettled(ctx context.Context, quiet time.Duration) []string {
	return Default.WaitSettled(ctx, quiet)
}
func Total() int                        { return Default.Total() }
