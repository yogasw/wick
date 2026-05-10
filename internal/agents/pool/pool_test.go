package pool

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// scriptedSpawner is the fake used by pool tests: feeds a canned
// stream-json script per spawn, captures stdin for assertions, lets
// the subprocess exit when stdout closes.
type scriptedSpawner struct {
	mu    sync.Mutex
	Lines [][]string
	Calls int
	Last  *scriptedProc
	Procs []*scriptedProc
}

func (s *scriptedSpawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.Calls
	s.Calls++
	var lines []string
	if idx < len(s.Lines) {
		lines = s.Lines[idx]
	}
	pr, pw := io.Pipe()
	proc := &scriptedProc{
		stdoutR:  pr,
		stdoutW:  pw,
		stdinBuf: &bytes.Buffer{},
		opt:      opt,
		done:     make(chan struct{}),
		pid:      70000 + idx,
	}
	s.Last = proc
	s.Procs = append(s.Procs, proc)
	go func() {
		for _, l := range lines {
			if _, err := pw.Write([]byte(l + "\n")); err != nil {
				return
			}
		}
		_ = pw.Close()
	}()
	return proc, nil
}

type scriptedProc struct {
	stdoutR  *io.PipeReader
	stdoutW  *io.PipeWriter
	stdinMu  sync.Mutex
	stdinBuf *bytes.Buffer
	opt      provider.SpawnOptions
	done     chan struct{}
	once     sync.Once
	pid      int
}

func (p *scriptedProc) Stdout() io.Reader     { return p.stdoutR }
func (p *scriptedProc) Stdin() io.WriteCloser { return &scriptedStdin{p: p} }
func (p *scriptedProc) Wait() error           { <-p.done; return nil }
func (p *scriptedProc) Pid() int              { return p.pid }
func (p *scriptedProc) Binary() string        { return "" }
func (p *scriptedProc) Argv() []string        { return nil }
func (p *scriptedProc) Kill() error {
	p.once.Do(func() {
		_ = p.stdoutR.Close()
		_ = p.stdoutW.Close()
		close(p.done)
	})
	return nil
}
func (p *scriptedProc) recordedStdin() string {
	p.stdinMu.Lock()
	defer p.stdinMu.Unlock()
	return p.stdinBuf.String()
}

type scriptedStdin struct{ p *scriptedProc }

func (s *scriptedStdin) Write(b []byte) (int, error) {
	s.p.stdinMu.Lock()
	defer s.p.stdinMu.Unlock()
	return s.p.stdinBuf.Write(b)
}
func (s *scriptedStdin) Close() error { return nil }

// setupSession creates a session + named agent on disk so the pool
// can route Send to it.
func setupSession(t *testing.T, layout config.Layout, sessionID string) {
	t.Helper()
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID:     sessionID,
		Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, sessionID, "default", "claude"); err != nil {
		t.Fatal(err)
	}
}

func newPool(t *testing.T, max int, sp *scriptedSpawner) (*Pool, config.Layout) {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	factory := &ClaudeFactory{
		Layout:  layout,
		Spawner: sp,
	}
	p := New(PoolConfig{
		MaxConcurrent: max,
		IdleTimeout:   500 * time.Millisecond,
		Layout:        layout,
		Factory:       factory,
	})
	factory.OnExit = p.HandleExit
	// Stop drains agent + queue goroutines so trailing meta.json writes
	// don't race t.TempDir cleanup on Windows (or leave the next assert
	// reading stale state).
	t.Cleanup(p.Stop)
	return p, layout
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitFor: condition never satisfied")
}

func TestSendSpawnsAndDelivers(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{
		`{"type":"system","subtype":"init","session_id":"abc"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"hi"}`,
	}}}
	p, layout := newPool(t, 2, sp)
	setupSession(t, layout, "S1")

	if err := p.Send(context.Background(), "S1", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)

	stdin := sp.Last.recordedStdin()
	if stdin == "" {
		t.Fatal("nothing written to stdin")
	}
	// Verify cli_session_id was persisted to agents.json.
	sess, _ := session.Load(layout, "S1")
	if sess.Agents[0].CLISessionID != "abc" {
		t.Fatalf("cli_session_id: %q", sess.Agents[0].CLISessionID)
	}
}

func TestQueueWhenPoolFull(t *testing.T) {
	// max=1 + slow agent so the second Send queues. The first agent
	// stays alive until idle TTL fires (no Done in script).
	sp := &scriptedSpawner{Lines: [][]string{
		{}, // first spawn: no output, hangs until idle kill
		{`{"type":"system","subtype":"init","session_id":"x"}`, `{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`, `{"type":"result","subtype":"success","is_error":false,"result":"ok"}`},
	}}
	p, layout := newPool(t, 1, sp)
	setupSession(t, layout, "A")
	setupSession(t, layout, "B")

	if err := p.Send(context.Background(), "A", "default", "ui", "user", "first"); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(context.Background(), "B", "default", "ui", "user", "second"); err != nil {
		t.Fatal(err)
	}
	if got := p.QueueLen(); got != 1 {
		t.Fatalf("queue length: got %d, want 1", got)
	}
	// Buffered message should be on disk for B.
	sessB, _ := session.Load(layout, "B")
	if len(sessB.Meta.PendingInput) != 1 || sessB.Meta.PendingInput[0] != "second" {
		t.Fatalf("pending_input: %v", sessB.Meta.PendingInput)
	}
	if sessB.Meta.Status != session.StatusQueued {
		t.Fatalf("B status: %q", sessB.Meta.Status)
	}

	// Wait for idle TTL on A to fire — pool should grant the slot to
	// B and B's spawn should complete.
	waitFor(t, func() bool {
		s, _ := session.Load(layout, "B")
		return s.Meta.Status == session.StatusIdle
	}, 5*time.Second)

	sessB, _ = session.Load(layout, "B")
	if len(sessB.Meta.PendingInput) != 0 {
		t.Fatalf("pending_input not drained: %v", sessB.Meta.PendingInput)
	}
}

func TestBufferDrainsCombined(t *testing.T) {
	// Two messages buffered before any slot grant — verify drain
	// joins them with newline.
	sp := &scriptedSpawner{Lines: [][]string{
		{}, // hold session A indefinitely
		{`{"type":"system","subtype":"init","session_id":"x"}`, `{"type":"assistant","message":{"content":[{"type":"text","text":"ok"}]}}`, `{"type":"result","subtype":"success","is_error":false,"result":"ok"}`},
	}}
	p, layout := newPool(t, 1, sp)
	setupSession(t, layout, "A")
	setupSession(t, layout, "B")

	_ = p.Send(context.Background(), "A", "default", "ui", "user", "hold")
	_ = p.Send(context.Background(), "B", "default", "ui", "user", "first")
	_ = p.Send(context.Background(), "B", "default", "ui", "user", "second")

	waitFor(t, func() bool { return sp.Calls >= 2 }, 5*time.Second)
	// After A idle-kills, B is spawned. Wait a moment for stdin write.
	time.Sleep(300 * time.Millisecond)

	// Find B's process — second spawn.
	if len(sp.Procs) < 2 {
		t.Fatalf("expected 2 spawns, got %d", len(sp.Procs))
	}
	bStdin := sp.Procs[1].recordedStdin()
	// Should contain both messages joined by \n inside a single user envelope.
	if !contains(bStdin, "first") || !contains(bStdin, "second") {
		t.Fatalf("combined stdin missing parts: %q", bStdin)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestStopShutsDownActives(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{{}}}
	p, layout := newPool(t, 1, sp)
	setupSession(t, layout, "A")
	_ = p.Send(context.Background(), "A", "default", "ui", "user", "x")
	waitFor(t, func() bool { return p.Active() == 1 }, 2*time.Second)
	p.Stop()
	waitFor(t, func() bool { return p.Active() == 0 }, 2*time.Second)
}
