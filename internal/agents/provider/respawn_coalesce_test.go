package provider

// Respawn-mode (codex / omp / opencode) queue semantics: messages sent
// while a turn runs are folded into ONE next turn, and a Stop ends the
// agent for good — no queued turn, no respawn racing the kill.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

// gatedSpawner's first process starts a turn and holds it open until
// release is closed; it then completes the turn and, unless keepOpen,
// closes stdout. Later spawns run a whole turn straight away. killDelay
// slows the first process's Kill, which widens the window a respawn
// spends tearing it down.
type gatedSpawner struct {
	release   chan struct{}
	keepOpen  bool
	killDelay time.Duration
	killing   chan struct{} // closed when the first process's Kill starts
	// start / end are the provider's wire lines around a turn; empty =
	// codex. inject, when set, makes the processes Injectors.
	start  []string
	end    []string
	inject bool

	mu    sync.Mutex
	opts  []SpawnOptions
	procs []*gatedProcess
}

func newGatedSpawner() *gatedSpawner {
	return &gatedSpawner{release: make(chan struct{}), killing: make(chan struct{})}
}

func (s *gatedSpawner) Spawn(ctx context.Context, opt SpawnOptions) (Process, error) {
	s.mu.Lock()
	idx := len(s.opts)
	s.opts = append(s.opts, opt)
	pr, pw := io.Pipe()
	p := &gatedProcess{fakeProcess: &fakeProcess{
		stdoutR: pr, stdoutW: pw, stdinBuf: &bytes.Buffer{}, opt: opt, done: make(chan struct{}),
	}}
	if idx == 0 {
		p.killDelay, p.killing = s.killDelay, s.killing
	}
	s.procs = append(s.procs, p)
	s.mu.Unlock()
	if s.inject {
		return &injectProcess{gatedProcess: p}, nil
	}

	start, end := s.start, s.end
	if start == nil {
		start = []string{`{"type":"turn.started"}`}
		end = []string{`{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"ok"}}`, `{"type":"turn.completed","usage":{}}`}
	}
	go func() {
		for _, l := range start {
			_, _ = pw.Write([]byte(l + "\n"))
		}
		if idx == 0 {
			<-s.release
		}
		for _, l := range end {
			_, _ = pw.Write([]byte(l + "\n"))
		}
		if idx == 0 && s.keepOpen {
			return
		}
		_ = pw.Close()
	}()
	return p, nil
}

func (s *gatedSpawner) spawns() []SpawnOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SpawnOptions(nil), s.opts...)
}

// gatedProcess is a fakeProcess with Pid 0 (teardown goes straight to
// Kill, no signals to a made-up pid) and an optionally slow Kill.
type gatedProcess struct {
	*fakeProcess
	killDelay time.Duration
	killing   chan struct{}
	killOnce  sync.Once
}

func (p *gatedProcess) Pid() int { return 0 }

func (p *gatedProcess) Kill() error {
	p.killOnce.Do(func() {
		if p.killing != nil {
			close(p.killing)
		}
		time.Sleep(p.killDelay)
	})
	return p.fakeProcess.Kill()
}

func newQueueAgent(t *testing.T, sp Spawner, onExit func(ExitReason, string)) *Agent {
	t.Helper()
	return New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   5 * time.Second,
		ParserFactory: func() event.Parser { return event.NewCodexParser() },
		Spawner:       sp,
		State:         state.New(nil),
		SendMode:      SendRespawnQueue,
		OnExit:        onExit,
	})
}

func TestRespawnQueue_CoalescesQueuedMessagesIntoOneTurn(t *testing.T) {
	sp := newGatedSpawner()
	a := newQueueAgent(t, sp, nil)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	if err := a.Send("[from: A]\n0"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, func() bool { return len(sp.spawns()) == 1 }, 2*time.Second)

	var want []string
	for _, m := range []string{"1", "2", "3", "4", "5", "6"} {
		msg := "[from: A]\n" + m
		want = append(want, msg)
		if err := a.Send(msg); err != nil {
			t.Fatalf("Send %s: %v", m, err)
		}
	}
	if got := a.QueuedCount(); got != 6 {
		t.Fatalf("QueuedCount = %d, want 6 while the turn runs", got)
	}
	if n := len(sp.spawns()); n != 1 {
		t.Fatalf("spawns mid-turn = %d, want 1 (queued, not spawned)", n)
	}

	close(sp.release)
	waitFor(t, func() bool { return len(sp.spawns()) == 2 }, 3*time.Second)
	// The combined turn completes on its own; nothing may follow it.
	time.Sleep(300 * time.Millisecond)
	got := sp.spawns()
	if len(got) != 2 {
		t.Fatalf("spawns = %d, want exactly 2 (first turn + one combined turn)", len(got))
	}
	if msg, exp := got[1].InitialMessage, strings.Join(want, "\n\n"); msg != exp {
		t.Errorf("combined InitialMessage = %q, want %q", msg, exp)
	}
	if n := a.QueuedCount(); n != 0 {
		t.Errorf("QueuedCount after drain = %d, want 0", n)
	}
}

func TestRespawnQueue_StopMidTurnDropsQueue(t *testing.T) {
	sp := newGatedSpawner()
	a := newQueueAgent(t, sp, nil)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = a.Send("0")
	waitFor(t, func() bool { return len(sp.spawns()) == 1 }, 2*time.Second)
	_ = a.Send("1")
	_ = a.Send("2")

	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	close(sp.release)
	time.Sleep(300 * time.Millisecond)
	if n := len(sp.spawns()); n != 1 {
		t.Fatalf("spawns after Stop = %d, want 1 — the queue must die with the agent", n)
	}
	if n := a.QueuedCount(); n != 0 {
		t.Errorf("QueuedCount after Stop = %d, want 0", n)
	}
	if err := a.Send("3"); err == nil {
		t.Error("Send after Stop succeeded; a stopped agent must not spawn")
	}
}

// A Stop that lands while the drain is still tearing the finished turn's
// process down (the lock is released for that) must not be followed by
// the queued turn's spawn.
func TestRespawnQueue_StopDuringRespawnTeardownDoesNotSpawn(t *testing.T) {
	sp := newGatedSpawner()
	sp.keepOpen = true
	sp.killDelay = 400 * time.Millisecond
	a := newQueueAgent(t, sp, nil)
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = a.Send("0")
	waitFor(t, func() bool { return len(sp.spawns()) == 1 }, 2*time.Second)
	_ = a.Send("1")

	close(sp.release)
	select {
	case <-sp.killing:
	case <-time.After(3 * time.Second):
		t.Fatal("drain never started tearing the first process down")
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if n := len(sp.spawns()); n != 1 {
		t.Fatalf("spawns = %d, want 1 — Stop during the respawn teardown resurrected the agent", n)
	}
}

// killErrProcess reports a non-clean Wait error once killed, the way a
// signalled subprocess does, so the reader can reach either exit branch.
type killErrProcess struct{ *gatedProcess }

func (p *killErrProcess) Wait() error {
	<-p.done
	return errors.New("signal: killed")
}

type killErrSpawner struct{ *gatedSpawner }

func (s killErrSpawner) Spawn(ctx context.Context, opt SpawnOptions) (Process, error) {
	p, err := s.gatedSpawner.Spawn(ctx, opt)
	if err != nil {
		return nil, err
	}
	return &killErrProcess{p.(*gatedProcess)}, nil
}

// Whichever way the reader notices a Stop — ctx.Done or the stream
// closing under it — the exit is a stop, never a crash for the pool to
// "recover" from.
func TestRespawnQueue_StopIsNeverExitError(t *testing.T) {
	for i := 0; i < 20; i++ {
		var sawError atomic.Bool
		sp := killErrSpawner{newGatedSpawner()}
		a := newQueueAgent(t, sp, func(r ExitReason, _ string) {
			if r == ExitError {
				sawError.Store(true)
			}
		})
		if err := a.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		_ = a.Send("0")
		waitFor(t, func() bool { return len(sp.spawns()) == 1 }, 2*time.Second)
		_ = a.Stop()
		close(sp.release)
		time.Sleep(20 * time.Millisecond)
		if sawError.Load() {
			t.Fatalf("iteration %d: Stop reported ExitError", i)
		}
	}
}

// injectProcess takes messages into its running turn, like an opencode
// server turn does.
type injectProcess struct {
	*gatedProcess
	mu  sync.Mutex
	got []string
}

func (p *injectProcess) Inject(text string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, text)
	return nil
}

// wireFor is each respawn provider's turn, as its parser reads it.
var respawnWire = []struct {
	name       string
	parser     func() event.Parser
	start, end []string
}{
	{"codex", func() event.Parser { return event.NewCodexParser() }, nil, nil},
	{"omp", func() event.Parser { return event.NewOMPParser("omp") },
		[]string{`{"type":"agent_start"}`},
		[]string{`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"ok"}],"stopReason":"stop"}}`, `{"type":"agent_end","messages":[]}`}},
	{"opencode-run", func() event.Parser { return event.NewOpencodeParser("opencode") },
		[]string{`{"type":"step_start","sessionID":"ses_1","part":{"type":"step-start"}}`},
		[]string{`{"type":"text","sessionID":"ses_1","part":{"type":"text","text":"ok"}}`, `{"type":"step_finish","sessionID":"ses_1","part":{"type":"step-finish","reason":"stop"}}`}},
}

func newWireAgent(sp Spawner, parser func() event.Parser, dir string) *Agent {
	return New(Options{
		Workspace:     dir,
		IdleTimeout:   5 * time.Second,
		ParserFactory: parser,
		Spawner:       sp,
		State:         state.New(nil),
		SendMode:      SendRespawnQueue,
	})
}

// Six quick messages while a turn runs → one combined turn, in order;
// a kill mid-turn → the queue is gone and nothing spawns. Per provider,
// through its real parser.
func TestRespawnQueue_PerProvider(t *testing.T) {
	for _, w := range respawnWire {
		t.Run(w.name+"/coalesce", func(t *testing.T) {
			sp := newGatedSpawner()
			sp.start, sp.end = w.start, w.end
			a := newWireAgent(sp, w.parser, t.TempDir())
			_ = a.Start(context.Background())
			defer a.Stop()
			_ = a.Send("0")
			waitFor(t, func() bool { return len(sp.spawns()) == 1 }, 2*time.Second)
			var want []string
			for i := 1; i <= 6; i++ {
				m := "[from: A]\nrevisi " + string(rune('0'+i))
				want = append(want, m)
				_ = a.Send(m)
			}
			close(sp.release)
			waitFor(t, func() bool { return len(sp.spawns()) == 2 }, 3*time.Second)
			time.Sleep(300 * time.Millisecond)
			got := sp.spawns()
			if len(got) != 2 {
				t.Fatalf("spawns = %d, want 2", len(got))
			}
			if got[1].InitialMessage != strings.Join(want, "\n\n") {
				t.Fatalf("combined = %q", got[1].InitialMessage)
			}
		})
		t.Run(w.name+"/kill", func(t *testing.T) {
			sp := newGatedSpawner()
			sp.start, sp.end = w.start, w.end
			a := newWireAgent(sp, w.parser, t.TempDir())
			_ = a.Start(context.Background())
			_ = a.Send("0")
			waitFor(t, func() bool { return len(sp.spawns()) == 1 }, 2*time.Second)
			for i := 0; i < 6; i++ {
				_ = a.Send("x")
			}
			_ = a.Stop()
			close(sp.release)
			time.Sleep(300 * time.Millisecond)
			if n := len(sp.spawns()); n != 1 || a.QueuedCount() != 0 {
				t.Fatalf("after kill: spawns=%d queued=%d, want 1 and 0", n, a.QueuedCount())
			}
		})
	}
}

// A process that can take input mid-turn gets every message there, in
// order, with no queue and no second spawn (opencode server mode).
func TestRespawnQueue_InjectsIntoRunningTurn(t *testing.T) {
	sp := newGatedSpawner()
	sp.inject = true
	a := newWireAgent(sp, func() event.Parser { return event.NewCodexParser() }, t.TempDir())
	_ = a.Start(context.Background())
	defer a.Stop()
	_ = a.Send("0")
	waitFor(t, func() bool { return len(sp.spawns()) == 1 }, 2*time.Second)
	a.mu.Lock()
	ip, _ := a.proc.(*injectProcess)
	a.mu.Unlock()
	if ip == nil {
		t.Fatal("process is not the injector")
	}
	for _, m := range []string{"1", "2", "3"} {
		if err := a.Send(m); err != nil {
			t.Fatal(err)
		}
	}
	if a.QueuedCount() != 0 {
		t.Fatalf("queued %d, want 0 — the turn takes them", a.QueuedCount())
	}
	ip.mu.Lock()
	got := strings.Join(ip.got, ",")
	ip.mu.Unlock()
	if got != "1,2,3" {
		t.Fatalf("injected = %q, want 1,2,3", got)
	}
	close(sp.release)
	time.Sleep(300 * time.Millisecond)
	if n := len(sp.spawns()); n != 1 {
		t.Fatalf("spawns = %d, want 1", n)
	}
}

// errExitSpawner's process reports an error in-band, then exits
// non-zero — opencode run on a model that does not exist.
type errExitProcess struct{ *fakeProcess }

func (p *errExitProcess) Pid() int    { return 0 }
func (p *errExitProcess) Wait() error { <-p.done; return errors.New("exit status 1") }

type errExitSpawner struct{}

func (errExitSpawner) Spawn(ctx context.Context, opt SpawnOptions) (Process, error) {
	pr, pw := io.Pipe()
	p := &errExitProcess{&fakeProcess{stdoutR: pr, stdoutW: pw, stdinBuf: &bytes.Buffer{}, opt: opt, done: make(chan struct{})}}
	go func() {
		_, _ = pw.Write([]byte(`{"type":"error","sessionID":"s","error":{"name":"ProviderModelNotFoundError","data":{"message":"Model not found"}}}` + "\n"))
		_ = pw.Close()
		p.Kill()
	}()
	return p, nil
}

func TestRespawnQueue_ExitAfterInBandErrorIsNotACrash(t *testing.T) {
	reasons := make(chan ExitReason, 4)
	a := New(Options{
		Workspace: t.TempDir(), IdleTimeout: 5 * time.Second,
		ParserFactory: func() event.Parser { return event.NewOpencodeParser("opencode") },
		Spawner:       errExitSpawner{}, State: state.New(nil), SendMode: SendRespawnQueue,
		Instance: &Instance{Type: TypeOpencode, Name: "oc"},
		OnExit:   func(r ExitReason, _ string) { reasons <- r },
	})
	_ = a.Start(context.Background())
	defer a.Stop()
	_ = a.Send("hi")
	select {
	case r := <-reasons:
		if r == ExitError {
			t.Fatal("a turn that failed in-band was reported as a crash")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no exit")
	}
}
