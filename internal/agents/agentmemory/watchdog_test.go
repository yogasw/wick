package agentmemory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Watchdog tests (PLAN §25).
//
// Nothing here sleeps and nothing here spawns: the clock is a value the test
// moves and the daemon is a double that records what was asked of it. A
// supervision loop tested by waiting out real intervals is a test nobody runs
// and therefore a watchdog nobody trusts.

// fakeTarget is a daemon the test drives by hand.
type fakeTarget struct {
	mu sync.Mutex

	id         string
	supervised bool
	managed    bool
	alive      bool
	healthy    bool
	opStopped  bool

	// startErr, when set, fails every start — the "cannot start at all"
	// case the give-up exists for.
	startErr error

	starts   int
	restarts int
}

func (f *fakeTarget) ID() string { return f.id }

func (f *fakeTarget) Supervised() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.supervised
}

func (f *fakeTarget) Managed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.managed
}

func (f *fakeTarget) Alive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive
}

func (f *fakeTarget) Healthy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.healthy
}

func (f *fakeTarget) OperatorStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opStopped
}

// Start brings the daemon up unless the test said it cannot come up.
func (f *fakeTarget) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	if f.startErr != nil {
		return f.startErr
	}
	f.managed, f.alive, f.healthy = true, true, true
	return nil
}

func (f *fakeTarget) Restart(ctx context.Context) error {
	f.mu.Lock()
	f.restarts++
	f.mu.Unlock()
	return f.Start(ctx)
}

func (f *fakeTarget) set(fn func(*fakeTarget)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeTarget) counts() (starts, restarts int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.restarts
}

// testClock is the fake clock. Time only moves when a test moves it.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *testClock {
	return &testClock{t: time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newTestWatchdog wires a watchdog over one fake daemon and a fake clock.
func newTestWatchdog(t *fakeTarget, c *testClock) *Watchdog {
	return &Watchdog{
		states:    map[string]*WatchdogState{},
		targets:   func() []watchTarget { return []watchTarget{t} },
		now:       c.now,
		upgrading: func() bool { return false },
	}
}

// tick runs n passes, moving the clock by the real interval between them —
// which is what a test of a 30-second loop can do in microseconds.
func tick(w *Watchdog, c *testClock, n int) {
	for i := 0; i < n; i++ {
		w.Tick(context.Background())
		c.advance(watchInterval)
	}
}

// TestWatchdogStartsADeadDaemon: wick holds a process record and the process
// is gone. That is a start, and it is recorded as "dead" rather than as a
// generic restart — a daemon that keeps exiting is a different bug from one
// that keeps wedging (PLAN §25.2).
func TestWatchdogStartsADeadDaemon(t *testing.T) {
	f := &fakeTarget{id: "mem", supervised: true, managed: true, alive: false, healthy: false}
	c := newClock()
	w := newTestWatchdog(f, c)

	tick(w, c, 1)

	// Restart, not start: wick still holds the record of the process that
	// died, and a plain start would answer "already running" to it. The
	// call made is the assertion — see TestStaleProcessRecordNeedsRestart.
	starts, restarts := f.counts()
	if restarts != 1 || starts != 1 {
		t.Fatalf("starts=%d restarts=%d, want one restart (which performs one start)", starts, restarts)
	}
	st := w.State("mem")
	if st.LastReason != ReasonDead || st.Restarts != 1 {
		t.Fatalf("state %+v", st)
	}
	if st.HungRestarts != 0 {
		t.Fatalf("a dead daemon must not be counted as a hung one: %+v", st)
	}
}

// TestWatchdogRestartsAHungDaemon: the process is alive and answers nothing.
// It is NOT restarted on the first silent probe — a daemon busy with a
// compaction would be interrupted by exactly that — but it is once the grace
// window has passed.
func TestWatchdogRestartsAHungDaemon(t *testing.T) {
	f := &fakeTarget{id: "mem", supervised: true, managed: true, alive: true, healthy: false}
	c := newClock()
	w := newTestWatchdog(f, c)

	// First pass starts the hung timer and does nothing else.
	tick(w, c, 1)
	if starts, restarts := f.counts(); starts != 0 || restarts != 0 {
		t.Fatalf("acted inside the grace window: starts=%d restarts=%d", starts, restarts)
	}

	// Still inside the window.
	c.advance(hungGrace / 3)
	tick(w, c, 1)
	if _, restarts := f.counts(); restarts != 0 {
		t.Fatalf("restarted before the grace window elapsed")
	}

	// Past it.
	c.advance(hungGrace)
	tick(w, c, 1)
	_, restarts := f.counts()
	if restarts != 1 {
		t.Fatalf("restarts=%d, want 1 once the daemon has been silent past the grace window", restarts)
	}
	st := w.State("mem")
	if st.LastReason != ReasonHung || st.HungRestarts != 1 || st.Restarts != 1 {
		t.Fatalf("state %+v", st)
	}
}

// TestWatchdogStartsWhatShouldBeOn: autostart says on, nothing is running at
// all — no process record, nothing answering.
func TestWatchdogStartsWhatShouldBeOn(t *testing.T) {
	f := &fakeTarget{id: "mem", supervised: true}
	c := newClock()
	w := newTestWatchdog(f, c)

	tick(w, c, 1)

	if starts, _ := f.counts(); starts != 1 {
		t.Fatalf("starts=%d, want 1", starts)
	}
	if st := w.State("mem"); st.LastReason != ReasonOff {
		t.Fatalf("reason %q, want %q", st.LastReason, ReasonOff)
	}
}

// TestWatchdogLeavesAHealthyDaemonAlone is the case that matters most: the
// watchdog's most likely failure is not missing a dead daemon, it is
// restarting a working one.
func TestWatchdogLeavesAHealthyDaemonAlone(t *testing.T) {
	f := &fakeTarget{id: "mem", supervised: true, managed: true, alive: true, healthy: true}
	c := newClock()
	w := newTestWatchdog(f, c)

	tick(w, c, 20)

	if starts, restarts := f.counts(); starts != 0 || restarts != 0 {
		t.Fatalf("touched a healthy daemon: starts=%d restarts=%d", starts, restarts)
	}
	st := w.State("mem")
	if st.Restarts != 0 || st.LastReason != "" || st.GaveUp {
		t.Fatalf("state %+v", st)
	}
	if !st.Watching {
		t.Fatal("a supervised daemon must report that it is being watched")
	}
}

// TestWatchdogRespectsAnOperatorStop: if the Stop button did not mean it, it
// would be a lie — the daemon would be back within half a minute and nothing
// on the screen would say why (PLAN §25.3 guard 1).
func TestWatchdogRespectsAnOperatorStop(t *testing.T) {
	f := &fakeTarget{id: "mem", supervised: true, opStopped: true}
	c := newClock()
	w := newTestWatchdog(f, c)

	tick(w, c, 10)

	if starts, restarts := f.counts(); starts != 0 || restarts != 0 {
		t.Fatalf("undid an operator's Stop: starts=%d restarts=%d", starts, restarts)
	}
	if st := w.State("mem"); !st.StoppedByOperator {
		t.Fatalf("the reason nothing is happening must be stated: %+v", st)
	}

	// An explicit start clears the marker, and supervision resumes.
	f.set(func(ft *fakeTarget) { ft.opStopped = false })
	tick(w, c, 1)
	if starts, _ := f.counts(); starts != 1 {
		t.Fatalf("starts=%d, want supervision to resume after the marker cleared", starts)
	}
}

// TestWatchdogBacksOffThenGivesUp: a daemon that cannot start — missing
// binary, port permanently taken, corrupt store — must not be respawned
// forever on a 2-vCPU host, and the stopping must be VISIBLE rather than
// silent (PLAN §25.3 guard 2).
func TestWatchdogBacksOffThenGivesUp(t *testing.T) {
	boom := errors.New("ai-memory not installed")
	f := &fakeTarget{id: "mem", supervised: true, startErr: boom}
	c := newClock()
	w := newTestWatchdog(f, c)

	// Attempt 1 fails and arms the first backoff.
	w.Tick(context.Background())
	st := w.State("mem")
	if st.ConsecutiveFailures != 1 || st.LastError != boom.Error() {
		t.Fatalf("after one failure: %+v", st)
	}
	wantNext := c.now().Add(watchBackoffMin).UnixMilli()
	if st.NextAttemptMS != wantNext {
		t.Fatalf("next attempt %d, want %d (one backoffMin away)", st.NextAttemptMS, wantNext)
	}

	// A pass inside the backoff must not attempt anything.
	c.advance(watchBackoffMin / 2)
	w.Tick(context.Background())
	if starts, _ := f.counts(); starts != 1 {
		t.Fatalf("attempted inside the backoff: starts=%d", starts)
	}

	// Each further failure doubles the wait, until the give-up.
	var waits []time.Duration
	for i := 2; i <= watchGiveUpAfter; i++ {
		c.advance(watchBackoffMax) // well past whatever the backoff is
		before := c.now()
		w.Tick(context.Background())
		st = w.State("mem")
		if st.NextAttemptMS != 0 {
			waits = append(waits, time.UnixMilli(st.NextAttemptMS).Sub(before))
		}
	}

	starts, _ := f.counts()
	if starts != watchGiveUpAfter {
		t.Fatalf("starts=%d, want exactly %d attempts before giving up", starts, watchGiveUpAfter)
	}
	// Guard against a vacuous pass: there must be growth to compare.
	if len(waits) < 2 {
		t.Fatalf("expected several armed backoffs to compare, got %v", waits)
	}
	for i := 1; i < len(waits); i++ {
		if waits[i] <= waits[i-1] && waits[i-1] < watchBackoffMax {
			t.Fatalf("backoff did not grow: %v", waits)
		}
	}
	st = w.State("mem")
	if !st.GaveUp {
		t.Fatalf("still retrying after %d failures: %+v", watchGiveUpAfter, st)
	}
	if st.GaveUpReason == "" || st.LastError != boom.Error() {
		t.Fatalf("giving up must say why: %+v", st)
	}
	if st.NextAttemptMS != 0 {
		t.Fatalf("a watchdog that gave up must not be counting down: %+v", st)
	}

	// And it stays given up, however long the loop runs.
	tick(w, c, 50)
	if starts, _ := f.counts(); starts != watchGiveUpAfter {
		t.Fatalf("kept retrying after giving up: starts=%d", starts)
	}
}

// TestWatchdogForgivesARecoveredDaemon: a daemon someone fixed by hand is
// back under normal supervision, because the watchdog's opinion is about now.
func TestWatchdogForgivesARecoveredDaemon(t *testing.T) {
	f := &fakeTarget{id: "mem", supervised: true, startErr: errors.New("port taken")}
	c := newClock()
	w := newTestWatchdog(f, c)

	for i := 0; i < watchGiveUpAfter; i++ {
		c.advance(watchBackoffMax)
		w.Tick(context.Background())
	}
	if st := w.State("mem"); !st.GaveUp {
		t.Fatalf("expected a give-up first: %+v", st)
	}

	// Someone frees the port and starts it.
	f.set(func(ft *fakeTarget) {
		ft.startErr = nil
		ft.managed, ft.alive, ft.healthy = true, true, true
	})
	tick(w, c, 1)

	st := w.State("mem")
	if st.GaveUp || st.ConsecutiveFailures != 0 || st.LastError != "" {
		t.Fatalf("a healthy daemon must clear the give-up: %+v", st)
	}
	// The record of what happened survives — that is the point of counting.
	if st.Restarts != 0 {
		t.Fatalf("no successful intervention happened, so nothing should be counted: %+v", st)
	}
}

// TestWatchdogQuietDuringUpgrade: during a graceful handover the successor
// adopts the running daemon and this process is about to exit. Acting here
// would thrash on every reload (PLAN §25.3 guard 5).
func TestWatchdogQuietDuringUpgrade(t *testing.T) {
	f := &fakeTarget{id: "mem", supervised: true, managed: true, alive: false, healthy: false}
	c := newClock()
	w := newTestWatchdog(f, c)
	upgrading := true
	w.upgrading = func() bool { return upgrading }

	tick(w, c, 10)
	if starts, restarts := f.counts(); starts != 0 || restarts != 0 {
		t.Fatalf("acted during a handover: starts=%d restarts=%d", starts, restarts)
	}
	if st := w.State("mem"); st.LastReason != "" || st.ConsecutiveFailures != 0 {
		t.Fatalf("a handover pass must not even keep books: %+v", st)
	}

	// Once the handover is over, supervision resumes.
	upgrading = false
	tick(w, c, 1)
	if starts, _ := f.counts(); starts != 1 {
		t.Fatalf("starts=%d, want supervision to resume after the handover", starts)
	}
}

// TestWatchdogIgnoresAnUnsupervisedBackend: with nothing depending on the
// daemon there is nothing to keep alive, and the watchdog says so rather than
// starting one anyway.
func TestWatchdogIgnoresAnUnsupervisedBackend(t *testing.T) {
	f := &fakeTarget{id: "mem", supervised: false}
	c := newClock()
	w := newTestWatchdog(f, c)

	tick(w, c, 5)

	if starts, _ := f.counts(); starts != 0 {
		t.Fatalf("started a daemon nothing depends on: starts=%d", starts)
	}
	if st := w.State("mem"); st.Watching {
		t.Fatalf("state %+v, want watching=false", st)
	}
}

// TestWatchdogAdoptedDaemonIsLeftAlone: a healthy daemon wick did not spawn
// still counts as running. Starting a second one would take another port and
// split the store's readers across two processes.
func TestWatchdogAdoptedDaemonIsLeftAlone(t *testing.T) {
	f := &fakeTarget{id: "mem", supervised: true, managed: false, healthy: true}
	c := newClock()
	w := newTestWatchdog(f, c)

	tick(w, c, 5)

	if starts, _ := f.counts(); starts != 0 {
		t.Fatalf("spawned a second daemon next to an adopted one: starts=%d", starts)
	}
}

// TestBackoffGrowsAndIsCapped pins the arithmetic the loop depends on.
func TestBackoffGrowsAndIsCapped(t *testing.T) {
	if got := backoffFor(1); got != watchBackoffMin {
		t.Fatalf("first backoff %v, want %v", got, watchBackoffMin)
	}
	if got := backoffFor(2); got != 2*watchBackoffMin {
		t.Fatalf("second backoff %v, want %v", got, 2*watchBackoffMin)
	}
	if got := backoffFor(50); got != watchBackoffMax {
		t.Fatalf("backoff %v after many failures, want the cap %v", got, watchBackoffMax)
	}
}

// TestWatchdogStartStopIsIdempotent: the loop is started at boot and stopped
// at shutdown, and neither must be able to leave a second loop behind or
// block on a watchdog that was never started.
func TestWatchdogStartStopIsIdempotent(t *testing.T) {
	f := &fakeTarget{id: "mem", supervised: true, managed: true, alive: true, healthy: true}
	w := newTestWatchdog(f, newClock())

	w.Stop() // never started
	w.Start()
	w.Start() // second call must not start a second loop
	w.Stop()
	w.Stop()
}

// TestOperatorStopMarkerRoundTrip drives the REAL manager rather than the
// double, because the marker is only worth anything if the Stop button sets
// it and a start clears it — two call sites, one flag (PLAN §25.3 guard 1).
func TestOperatorStopMarkerRoundTrip(t *testing.T) {
	m := newManager(Descriptor{ID: "marker-mem", DisplayName: "marker-mem", BinName: "marker-mem", PrefPort: 41400, HealthPath: "/healthz"})

	if m.OperatorStopped() {
		t.Fatal("a daemon nobody stopped must not look operator-stopped")
	}
	m.StopByOperator()
	if !m.OperatorStopped() {
		t.Fatal("the Stop button must record the intent")
	}
	// A start that FAILS (no such binary) still clears it: the person asked
	// for the daemon to run, and the watchdog should be supervising again
	// whatever the outcome of that one attempt.
	_ = m.StartAndWait(context.Background())
	if m.OperatorStopped() {
		t.Fatal("an explicit start must clear the marker")
	}
	// A stop that is not a person's — shutdown, or the watchdog's own
	// restart — must not pin the daemon down.
	m.StopProcess()
	if m.OperatorStopped() {
		t.Fatal("StopProcess must not be mistaken for an operator's Stop")
	}
}

// TestChildAliveTracksTheProcess: dead and hung are told apart by this one
// method, so it is pinned against a real (short-lived) child rather than
// assumed.
func TestChildAliveTracksTheProcess(t *testing.T) {
	m := newManager(Descriptor{ID: "alive-mem", DisplayName: "alive-mem", BinName: "true", PrefPort: 41500})
	if m.ChildAlive() {
		t.Fatal("no process was ever spawned")
	}
	m.desc.Launch = func(LaunchOptions) ([]string, []string) { return nil, nil }
	if err := m.start(); err != nil {
		t.Skipf("no /usr/bin/true on this host: %v", err)
	}
	// `true` exits immediately; the reaper closes the channel, and that is
	// what the watchdog reads as "dead" rather than "hung".
	deadline := time.Now().Add(2 * time.Second)
	for m.ChildAlive() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if m.ChildAlive() {
		t.Fatal("a child that exited still reports as alive — dead would be read as hung")
	}
	if !m.spawnedHere() {
		t.Fatal("wick still holds the process record, which is what makes this 'dead' and not 'off'")
	}
	m.stop()
}


// TestStaleProcessRecordNeedsRestart pins the trap the dead path is written
// around, against the real manager: after the child exits, wick still holds
// the record, and start() answers "already running" without spawning
// anything. A watchdog that called Start here would count a success and
// leave the daemon down — the exact silent failure it exists to prevent.
func TestStaleProcessRecordNeedsRestart(t *testing.T) {
	m := newManager(Descriptor{ID: "stale-mem", DisplayName: "stale-mem", BinName: "true", PrefPort: 41600})
	m.desc.Launch = func(LaunchOptions) ([]string, []string) { return nil, nil }
	if err := m.start(); err != nil {
		t.Skipf("no /usr/bin/true on this host: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for m.ChildAlive() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if m.ChildAlive() {
		t.Skip("the short-lived child did not exit in time")
	}

	first := m.PID()
	// A plain start is a no-op on the stale record — this is the trap.
	if err := m.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if m.PID() != first {
		t.Fatalf("start unexpectedly respawned; the dead path could then use it")
	}
	if m.ChildAlive() {
		t.Fatal("start revived a dead process record — the trap this test describes is gone, so revisit the dead path")
	}

	// Restart drops the record and spawns for real.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = m.Restart(ctx) // `true` never answers /healthz, so readiness fails — the spawn is the point
	if m.PID() == first || m.PID() == 0 {
		t.Fatalf("restart did not spawn a new process: pid %d (was %d)", m.PID(), first)
	}
	m.stop()
}
