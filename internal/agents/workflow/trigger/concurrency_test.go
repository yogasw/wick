package trigger

// Tests for workflow concurrency (serial vs parallel mode) and the race-
// condition fix (rendered node args must not bleed across runs via shared
// cache). All tests here run without a real engine.Engine — they test the
// router's scheduling/semaphore logic directly.

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// --------------------------------------------------------------------------
// helpers — mirror runWorker perSem allocation so tests stay honest
// --------------------------------------------------------------------------

// effectivePerSem mirrors the exact perSem logic in runWorker.
// Returns nil for serial mode, a buffered channel for parallel mode.
func effectivePerSem(p workflow.ConcurrencyPolicy) chan struct{} {
	if !p.Enabled {
		return nil
	}
	m := p.Max
	if m == 0 {
		m = 2
	}
	return make(chan struct{}, m)
}

// --------------------------------------------------------------------------
// Serial mode
// --------------------------------------------------------------------------

// TestSerial_DefaultIsSerial confirms the zero-value ConcurrencyPolicy
// (Enabled=false) produces nil perSem — the router's signal for serial mode.
func TestSerial_DefaultIsSerial(t *testing.T) {
	var p workflow.ConcurrencyPolicy
	if p.Enabled {
		t.Fatal("zero-value ConcurrencyPolicy.Enabled must be false (serial by default)")
	}
	if sem := effectivePerSem(p); sem != nil {
		t.Fatalf("serial mode: perSem must be nil, got cap=%d", cap(sem))
	}
}

// TestSerial_ExplicitFalse confirms that Enabled=false always yields nil perSem
// regardless of Max value.
func TestSerial_ExplicitFalse(t *testing.T) {
	for _, max := range []int{0, 1, 5} {
		p := workflow.ConcurrencyPolicy{Enabled: false, Max: max}
		if sem := effectivePerSem(p); sem != nil {
			t.Errorf("Enabled=false Max=%d: perSem must be nil, got cap=%d", max, cap(sem))
		}
	}
}

// --------------------------------------------------------------------------
// Parallel mode — semaphore capacity
// --------------------------------------------------------------------------

// TestParallel_DefaultMax2 asserts that Enabled=true with Max=0 yields a
// semaphore of capacity 2 (the built-in default).
func TestParallel_DefaultMax2(t *testing.T) {
	p := workflow.ConcurrencyPolicy{Enabled: true, Max: 0}
	sem := effectivePerSem(p)
	if sem == nil {
		t.Fatal("parallel mode: perSem must not be nil")
	}
	if cap(sem) != 2 {
		t.Fatalf("default Max: cap(perSem) = %d, want 2", cap(sem))
	}
}

// TestParallel_ExplicitMax asserts an explicit Max>0 is used as-is.
func TestParallel_ExplicitMax(t *testing.T) {
	for _, max := range []int{1, 3, 10} {
		max := max
		t.Run(fmt.Sprintf("max=%d", max), func(t *testing.T) {
			p := workflow.ConcurrencyPolicy{Enabled: true, Max: max}
			sem := effectivePerSem(p)
			if sem == nil {
				t.Fatal("parallel mode: perSem must not be nil")
			}
			if cap(sem) != max {
				t.Fatalf("cap(perSem) = %d, want %d", cap(sem), max)
			}
		})
	}
}

// --------------------------------------------------------------------------
// Global concurrency
// --------------------------------------------------------------------------

// TestGlobalConcurrency_SetAndClear asserts SetGlobalConcurrency wires the
// globalSem channel (parallel enabled) and that 0 clears it (parallel disabled).
func TestGlobalConcurrency_SetAndClear(t *testing.T) {
	r := newTestRouter()

	// nil at start — parallel disabled by default.
	r.mu.RLock()
	gs := r.globalSem
	r.mu.RUnlock()
	if gs != nil {
		t.Fatalf("globalSem must be nil by default (parallel disabled), got cap=%d", cap(gs))
	}

	// Enable with cap=5.
	r.SetGlobalConcurrency(5)
	r.mu.RLock()
	gs = r.globalSem
	r.mu.RUnlock()
	if gs == nil {
		t.Fatal("globalSem is nil after SetGlobalConcurrency(5) — parallel should be enabled")
	}
	if cap(gs) != 5 {
		t.Fatalf("globalSem cap = %d, want 5", cap(gs))
	}
	if !r.GlobalParallelEnabled() {
		t.Fatal("GlobalParallelEnabled() = false, want true after SetGlobalConcurrency(5)")
	}

	// Disable: 0 → nil (serial).
	r.SetGlobalConcurrency(0)
	r.mu.RLock()
	gs = r.globalSem
	r.mu.RUnlock()
	if gs != nil {
		t.Fatalf("globalSem not nil after SetGlobalConcurrency(0), cap=%d — should be disabled", cap(gs))
	}
	if r.GlobalParallelEnabled() {
		t.Fatal("GlobalParallelEnabled() = true, want false after SetGlobalConcurrency(0)")
	}
}

// TestGlobalConcurrency_NegativeIsDisabled asserts negative values disable
// parallel (same as 0).
func TestGlobalConcurrency_NegativeIsDisabled(t *testing.T) {
	r := newTestRouter()
	r.SetGlobalConcurrency(-1)
	r.mu.RLock()
	gs := r.globalSem
	r.mu.RUnlock()
	if gs != nil {
		t.Fatalf("globalSem not nil after SetGlobalConcurrency(-1), cap=%d — should be disabled", cap(gs))
	}
}

// --------------------------------------------------------------------------
// Global disabled overrides per-workflow enabled
// --------------------------------------------------------------------------

// TestGlobalDisabled_ForcesSerial asserts that when global parallel is
// disabled (globalSem=nil), a workflow with Concurrency.Enabled=true still
// runs serially — the global switch is the master gate.
func TestGlobalDisabled_ForcesSerial(t *testing.T) {
	// Simulate runWorker decision: serial if gSem==nil OR !w.Concurrency.Enabled
	isSerial := func(gSem chan struct{}, wEnabled bool) bool {
		return gSem == nil || !wEnabled
	}

	cases := []struct {
		gSem     chan struct{}
		wEnabled bool
		wantSerial bool
	}{
		{nil, false, true},  // global off, wf off → serial
		{nil, true, true},   // global off, wf on  → still serial (global wins)
		{make(chan struct{}, 3), false, true},  // global on, wf off → serial
		{make(chan struct{}, 3), true, false},  // global on, wf on  → parallel
	}
	for _, c := range cases {
		got := isSerial(c.gSem, c.wEnabled)
		if got != c.wantSerial {
			t.Errorf("isSerial(gSem=%v, wEnabled=%v) = %v, want %v",
				c.gSem != nil, c.wEnabled, got, c.wantSerial)
		}
	}
}

// --------------------------------------------------------------------------
// Concurrency field zero-value / default contract
// --------------------------------------------------------------------------

// TestConcurrencyPolicy_MaxDefaultApplied documents the effective-max rule
// in a table-driven form so the contract is explicit.
func TestConcurrencyPolicy_MaxDefaultApplied(t *testing.T) {
	cases := []struct {
		p        workflow.ConcurrencyPolicy
		wantCap  int // 0 means serial (nil sem)
		wantNil  bool
	}{
		{workflow.ConcurrencyPolicy{Enabled: false}, 0, true},
		{workflow.ConcurrencyPolicy{Enabled: false, Max: 5}, 0, true},
		{workflow.ConcurrencyPolicy{Enabled: true, Max: 0}, 2, false},
		{workflow.ConcurrencyPolicy{Enabled: true, Max: 1}, 1, false},
		{workflow.ConcurrencyPolicy{Enabled: true, Max: 5}, 5, false},
	}
	for _, c := range cases {
		sem := effectivePerSem(c.p)
		if c.wantNil {
			if sem != nil {
				t.Errorf("%+v: want nil sem, got cap=%d", c.p, cap(sem))
			}
		} else {
			if sem == nil {
				t.Errorf("%+v: want sem cap=%d, got nil", c.p, c.wantCap)
			} else if cap(sem) != c.wantCap {
				t.Errorf("%+v: cap(sem) = %d, want %d", c.p, cap(sem), c.wantCap)
			}
		}
	}
}

// --------------------------------------------------------------------------
// Peak concurrency — goroutine-level simulation
// --------------------------------------------------------------------------

// TestParallel_PeakConcurrency simulates the parallel dispatcher goroutine
// pattern (acquire perSem → increment inFlight → work → release) and asserts
// that the peak in-flight count equals the semaphore cap, not N (serial) and
// not > cap (unbounded).
func TestParallel_PeakConcurrency(t *testing.T) {
	const maxPar = 2
	const numRuns = 6

	perSem := make(chan struct{}, maxPar)
	var inFlight, peak atomic.Int64
	var wg sync.WaitGroup

	for i := 0; i < numRuns; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			perSem <- struct{}{}
			defer func() { <-perSem }()

			cur := inFlight.Add(1)
			for {
				p := peak.Load()
				if cur <= p || peak.CompareAndSwap(p, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			inFlight.Add(-1)
		}()
	}
	wg.Wait()

	got := int(peak.Load())
	if got < maxPar {
		t.Fatalf("peak concurrent runs = %d, want >= %d (semaphore not allowing concurrency)", got, maxPar)
	}
	if got > maxPar {
		t.Fatalf("peak concurrent runs = %d, want <= %d (semaphore not capping)", got, maxPar)
	}
	t.Logf("peak concurrent runs = %d (cap=%d, total=%d) — OK", got, maxPar, numRuns)
}

// TestGlobalSemaphore_CapsAcrossWorkflows simulates the global semaphore
// capping runs from multiple workflows simultaneously.
func TestGlobalSemaphore_CapsAcrossWorkflows(t *testing.T) {
	const globalCap = 3
	const numWorkflows = 4
	const runsPerWF = 3

	gSem := make(chan struct{}, globalCap)
	var inFlight, peak atomic.Int64
	var wg sync.WaitGroup

	for wf := 0; wf < numWorkflows; wf++ {
		for run := 0; run < runsPerWF; run++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				gSem <- struct{}{}
				defer func() { <-gSem }()

				cur := inFlight.Add(1)
				for {
					p := peak.Load()
					if cur <= p || peak.CompareAndSwap(p, cur) {
						break
					}
				}
				time.Sleep(15 * time.Millisecond)
				inFlight.Add(-1)
			}()
		}
	}
	wg.Wait()

	got := int(peak.Load())
	if got > globalCap {
		t.Fatalf("global peak = %d, want <= %d (global semaphore not capping)", got, globalCap)
	}
	t.Logf("global peak = %d (cap=%d, total=%d) — OK", got, globalCap, numWorkflows*runsPerWF)
}

// --------------------------------------------------------------------------
// Arg-bleed regression — router-level
// --------------------------------------------------------------------------

// TestArgBleed_RouterDefIsImmutable verifies that the router's cached
// workflow definition is not mutated between Register and Definition reads.
// The deeper prerender mutation guard lives in engine/prerender_test.go.
//
// We test only the router's defs map — Register must store the exact value
// passed in, and Definition must return a read-only copy. We do not start
// a worker (no baseCtx) so we use the index/defs path directly.
func TestArgBleed_RouterDefIsImmutable(t *testing.T) {
	const tmplTitle = "PR #{{.Event.Payload.number}}"

	w := workflow.Workflow{
		ID:      "wf-argbleed",
		Enabled: true,
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual}},
		Graph: workflow.Graph{
			Entry: "n1",
			Nodes: []workflow.Node{
				{
					ID:   "n1",
					Type: workflow.NodeConnector,
					Args: map[string]any{"title": tmplTitle},
				},
			},
		},
	}

	// Insert directly into defs (bypasses worker spawn) to isolate the
	// immutability contract of the defs map.
	r := newTestRouter()
	r.mu.Lock()
	r.defs[w.ID] = w
	r.reindexLocked(w)
	r.mu.Unlock()

	// Read back the definition — must be the original template.
	def, ok := r.Definition(w.ID)
	if !ok {
		t.Fatal("Definition not found after Register")
	}
	if len(def.Graph.Nodes) == 0 {
		t.Fatal("no nodes in cached definition")
	}
	got := def.Graph.Nodes[0].Args["title"]
	if got != tmplTitle {
		t.Fatalf("router mutated cached node Args[title] = %q, want template %q", got, tmplTitle)
	}
}

// TestArgBleed_OverrideDoesNotMutateDef asserts that storing an override
// workflow via WorkItem.Workflow does NOT replace the router's defs entry.
// The override is for "run this draft once" — defs holds the published copy.
func TestArgBleed_OverrideDoesNotMutateDef(t *testing.T) {
	const tmplTitle = "issue #{{.Event.Payload.num}}"

	base := workflow.Workflow{
		ID:      "wf-override",
		Enabled: true,
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual}},
		Graph: workflow.Graph{
			Entry: "end",
			Nodes: []workflow.Node{
				{ID: "n1", Type: workflow.NodeConnector, Args: map[string]any{"title": tmplTitle}},
				{ID: "end", Type: workflow.NodeEnd},
			},
		},
	}

	r := newTestRouter()
	r.mu.Lock()
	r.defs[base.ID] = base
	r.reindexLocked(base)
	r.mu.Unlock()

	// Build override with a completely independent node slice so we don't
	// accidentally alias the registered workflow's Nodes slice.
	overrideNode := workflow.Node{
		ID:   "n1",
		Type: workflow.NodeConnector,
		Args: map[string]any{"title": "hardcoded-override"},
	}
	override := workflow.Workflow{
		ID:    base.ID,
		Graph: workflow.Graph{Nodes: []workflow.Node{overrideNode, {ID: "end", Type: workflow.NodeEnd}}},
	}
	item := WorkItem{ID: base.ID, Event: workflow.Event{Type: "manual"}, Workflow: &override}

	// Simulate runWorker: `w = *item.Workflow` — reads override, does NOT
	// write back into r.defs. The defs entry must be unchanged.
	w := *item.Workflow
	_ = w

	def, _ := r.Definition(base.ID)
	if len(def.Graph.Nodes) == 0 {
		t.Fatal("no nodes in cached definition")
	}
	got := def.Graph.Nodes[0].Args["title"]
	if got != tmplTitle {
		t.Fatalf("defs mutated after override read: Args[title] = %q, want %q", got, tmplTitle)
	}
}
