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
	// with --resume on the next message. Such work gets a short grace period,
	// not the full drain: an interactive Slack session can be "in flight" for
	// hours, and waiting for it kept the old process alive that whole time.
	// Two wick processes coexisting is not free — only one of them holds
	// intake, so the browser talks to one while the agents run in the other,
	// and tableflip refuses the NEXT upgrade while a parent is still alive.
	//
	// Everything else (a workflow run mid-node, a cron job mid-write, a
	// connector call) is NOT resumable: interrupting it loses the work, so
	// the drain waits for it properly.
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

// Busy / Wait / Snapshot on the default tracker.
func Busy() []string                   { return Default.Busy() }
func BusyKind(resumable bool) []string { return Default.BusyKind(resumable) }

func WaitKind(ctx context.Context, resumable bool) []string {
	return Default.WaitKind(ctx, resumable)
}
func Snapshot() map[string]int          { return Default.Snapshot() }
func Wait(ctx context.Context) []string { return Default.Wait(ctx) }
func Total() int                        { return Default.Total() }
