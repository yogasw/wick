package pool

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
)

// TestRecoverTurnSpawnsAlone is the counterpart to
// TestSystemTurnDoesNotSpawnAlone: a recover notice (source "recover",
// role "system") has NO user turn following it — the agent just died and
// nobody is typing. If the non-user guard in send() swallows it, the
// notice sits buffered until a human happens to message the session, and
// crash auto-restart never fires. The recover source must spawn on its
// own, exactly once, with the notice in the prompt.
func TestRecoverTurnSpawnsAlone(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`,
	}}}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	p.recoverFromExit("S1", "default", provider.ExitError, "")
	waitFor(t, func() bool { return sp.procCount() == 1 && p.Active() == 0 }, 3*time.Second)

	if n := sp.procCount(); n != 1 {
		t.Fatalf("expected exactly 1 spawn from the recover turn, got %d", n)
	}
	stdin := sp.procAt(0).recordedStdin()
	if !contains(stdin, "stopped unexpectedly") {
		t.Fatalf("spawned prompt missing the crash notice: %q", stdin)
	}
}

// A host-OOM kill arrives as a retryable ExitError, but the agent must
// still be told the cause was memory: the generic "unexpected exit,
// carry on" notice tells it to repeat the exact allocation that got it
// killed. The exit's ReasonDetail (HostOOMDetail) must reach the notice.
func TestRecoverNoticeCarriesHostOOMDetail(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`,
	}}}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	p.recoverFromExit("S1", "default", provider.ExitError, provider.HostOOMDetail(1<<30))
	waitFor(t, func() bool { return sp.procCount() == 1 && p.Active() == 0 }, 3*time.Second)

	if n := sp.procCount(); n != 1 {
		t.Fatalf("expected exactly 1 spawn, got %d", n)
	}
	stdin := sp.procAt(0).recordedStdin()
	if !contains(stdin, "machine ran out of memory") {
		t.Fatalf("notice does not name the memory cause: %q", stdin)
	}
}

// An OOM exit is not retried, but the notice must still reach the agent —
// which means it must spawn (--resume) and deliver the oomNotice on its
// own, with no user turn coming.
func TestRecoverOOMNoticeSpawnsAlone(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"ok"}`,
	}}}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	p.recoverFromExit("S1", "default", provider.ExitOOM, "used 2.0 GB, over its 1024 MB limit.")
	waitFor(t, func() bool { return sp.procCount() == 1 && p.Active() == 0 }, 3*time.Second)

	if n := sp.procCount(); n != 1 {
		t.Fatalf("expected exactly 1 spawn from the OOM notice, got %d", n)
	}
	stdin := sp.procAt(0).recordedStdin()
	if !contains(stdin, "too much memory") {
		t.Fatalf("spawned prompt missing the OOM notice: %q", stdin)
	}
	if !contains(stdin, "over its 1024 MB limit") {
		t.Fatalf("OOM notice missing the measured detail: %q", stdin)
	}
}

// exitReasonString is the pool's copy of the provider's reason names —
// the labels differ on purpose ("idle" vs "idle_ttl" are persisted in
// spawn logs), but coverage must not: a reason the provider can name and
// the pool logs as "unknown" is how ExitOOM shipped mislabeled.
func TestExitReasonStringCoversEveryReason(t *testing.T) {
	for r := provider.ExitReason(0); r < 32; r++ {
		if provider.ExitReasonName(r) == "unknown" {
			continue
		}
		if got := exitReasonString(r); got == "unknown" {
			t.Errorf("exitReasonString(%d) = \"unknown\", but the provider names it %q",
				r, provider.ExitReasonName(r))
		}
	}
}

// Non-recover system turns (channel origin context, reap notices) must
// keep the buffered-no-spawn behavior even after the recover exception.
func TestReapSystemTurnStillDoesNotSpawn(t *testing.T) {
	sp := &scriptedSpawner{}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	if err := p.Send(context.Background(), "S1", "default", "reap", "system", "[system] connectors reaped"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if n := sp.procCount(); n != 0 {
		t.Fatalf("reap system turn spawned the agent (%d procs); it must stay buffered", n)
	}
}

// exitReasonString must know every reason exitReasonName knows — ExitOOM
// was added there but not here, so every memory kill logged as "unknown"
// in Recent Spawns while the notice told the operator to go read it.
func TestExitReasonStringOOM(t *testing.T) {
	if got := exitReasonString(provider.ExitOOM); got != "oom" {
		t.Fatalf("exitReasonString(ExitOOM) = %q, want %q", got, "oom")
	}
}

// crashingSpawner is a CLI that cannot start: every spawn produces a
// process that emits nothing and exits non-zero immediately — codex with a
// config.toml it can no longer parse, a missing binary, a bad model id.
//
// It is the fake the crash-loop tests need, because the loop is driven by
// the RESTART dying the same way the original did. A spawner whose process
// exits cleanly ends the streak at the first restart and proves nothing.
type crashingSpawner struct {
	mu    sync.Mutex
	procs int
}

func (s *crashingSpawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	s.mu.Lock()
	s.procs++
	pid := 80000 + s.procs
	s.mu.Unlock()
	pr, pw := io.Pipe()
	_ = pw.Close() // stdout EOF straight away: nothing was ever produced
	return &crashingProc{stdoutR: pr, pid: pid}, nil
}

func (s *crashingSpawner) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.procs
}

type crashingProc struct {
	stdoutR *io.PipeReader
	pid     int
}

func (p *crashingProc) Stdout() io.Reader     { return p.stdoutR }
func (p *crashingProc) Stdin() io.WriteCloser { return nopStdin{} }
func (p *crashingProc) Wait() error           { return errors.New("exit status 1") }
func (p *crashingProc) Pid() int              { return p.pid }
func (p *crashingProc) Binary() string        { return "codex" }
func (p *crashingProc) Argv() []string        { return nil }
func (p *crashingProc) Env() []string         { return nil }
func (p *crashingProc) Kill() error           { return p.stdoutR.Close() }
func (p *crashingProc) StderrTail() string {
	return "error: failed to parse config.toml"
}

type nopStdin struct{}

func (nopStdin) Write(b []byte) (int, error) { return len(b), nil }
func (nopStdin) Close() error                { return nil }

// newCrashPool is newPool with a spawner that cannot produce a working
// process, so one Send is enough to start a real crash loop.
func newCrashPool(t *testing.T, sp *crashingSpawner) (*Pool, config.Layout) {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	factory := &ClaudeFactory{Layout: layout, Spawner: sp}
	p := New(PoolConfig{
		MaxConcurrent: 2,
		IdleTimeout:   500 * time.Millisecond,
		Layout:        layout,
		Factory:       factory,
	})
	// The exit hook is the loop: a dead process lands in HandleExit, which
	// decides whether to start another one. Without it there is no loop to
	// bound and these tests would pass on any code.
	factory.OnExit = p.HandleExit
	t.Cleanup(p.Stop)
	return p, layout
}

// settle waits until the spawn count stops moving, then returns it. A
// bounded loop cannot be measured by waiting for an exact number — that
// passes just as happily while the loop is still running.
func settle(t *testing.T, sp *crashingSpawner, quiet, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last, stableSince := -1, time.Now()
	for time.Now().Before(deadline) {
		n := sp.count()
		if n != last {
			last, stableSince = n, time.Now()
		} else if time.Since(stableSince) >= quiet {
			return n
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("spawning never settled: still at %d after %s — the crash loop is unbounded", sp.count(), timeout)
	return 0
}

// The crash loop, in one test. A provider whose process cannot start —
// codex with a config.toml it can no longer parse — dies again the instant
// it is restarted, and every death re-enters recoverFromExit. The restart
// budget was supposed to bound that, but the give-up branch announced its
// verdict through Send, and Send spawns: "stop restarting" was itself a
// restart. Production showed 673 spawns in one session, every one of them
// logged as "giving up", and the only way out was changing provider.
//
// So the budget has to bound SPAWNS, not just the restarts the pool admits
// to: one for the message that started it, then maxRespawnAttempts.
func TestGiveUpStopsSpawning(t *testing.T) {
	sp := &crashingSpawner{}
	p, layout := newCrashPool(t, sp)
	setupSession(t, layout, "S1")

	if err := p.Send(context.Background(), "S1", "default", "slack", "user", "halo"); err != nil {
		t.Fatal(err)
	}

	if n := settle(t, sp, 700*time.Millisecond, 20*time.Second); n != 1+maxRespawnAttempts {
		t.Fatalf("crash loop spawned %d processes, want %d (the first send plus %d restarts)",
			n, 1+maxRespawnAttempts, maxRespawnAttempts)
	}
}

// Giving up must still say so. The notice is buffered rather than sent
// (buffering is what keeps it from spawning), so it also goes out through
// OnSpawnError — that wiring is what puts it in front of the person in the
// originating Slack thread, who is the only one who can fix the cause.
// Once, though: the process keeps dying after the halt, and an
// announcement per death is the same flood in a different channel.
func TestGiveUpAnnouncesOnceWithoutSpawning(t *testing.T) {
	sp := &crashingSpawner{}
	p, layout := newCrashPool(t, sp)
	setupSession(t, layout, "S1")

	var mu sync.Mutex
	var announced []string
	p.cfg.OnSpawnError = func(ev SpawnErrorEvent) {
		mu.Lock()
		defer mu.Unlock()
		announced = append(announced, ev.Message)
	}

	if err := p.Send(context.Background(), "S1", "default", "slack", "user", "halo"); err != nil {
		t.Fatal(err)
	}
	settle(t, sp, 700*time.Millisecond, 20*time.Second)

	mu.Lock()
	defer mu.Unlock()
	var halts []string
	for _, m := range announced {
		if contains(m, "NOT restarted") {
			halts = append(halts, m)
		}
	}
	if len(halts) != 1 {
		t.Fatalf("halt announced %d times, want exactly 1: %v", len(halts), halts)
	}
	if !contains(halts[0], "config") {
		t.Fatalf("halt announcement %q does not point at the usual cause", halts[0])
	}
}

// A person messaging the session is the intervention the halt notice asked
// for — they have seen it and may have fixed the provider config. The next
// message must get a full budget again, or a session that gave up once
// would sit dead for the rest of the ten-minute window even after the fix.
func TestUserMessageRestoresTheRestartBudget(t *testing.T) {
	sp := &crashingSpawner{}
	p, layout := newCrashPool(t, sp)
	setupSession(t, layout, "S1")

	if err := p.Send(context.Background(), "S1", "default", "slack", "user", "halo"); err != nil {
		t.Fatal(err)
	}
	first := settle(t, sp, 700*time.Millisecond, 20*time.Second)

	if err := p.Send(context.Background(), "S1", "default", "slack", "user", "sudah kuperbaiki, lanjut"); err != nil {
		t.Fatal(err)
	}
	second := settle(t, sp, 700*time.Millisecond, 20*time.Second)

	if got := second - first; got != 1+maxRespawnAttempts {
		t.Fatalf("after the user message the budget allowed %d spawns, want %d", got, 1+maxRespawnAttempts)
	}
}
