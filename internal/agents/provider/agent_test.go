package provider

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

// crashSpawner produces a process that closes stdout immediately (no
// output) and whose Wait() returns an error carrying an exit code, plus
// a StderrTail — simulating a CLI that dies right after spawn (bad model
// id, config error). Exercises the reader-exit crash path that builds
// ExitDetail with exit code + stderr tail.
type crashSpawner struct {
	exitCode   int
	stderrTail string
}

func (s *crashSpawner) Spawn(ctx context.Context, opt SpawnOptions) (Process, error) {
	pr, pw := io.Pipe()
	proc := &crashProcess{
		stdoutR:    pr,
		stdoutW:    pw,
		stdinBuf:   &bytes.Buffer{},
		done:       make(chan struct{}),
		pid:        93000,
		exitCode:   s.exitCode,
		stderrTail: s.stderrTail,
	}
	// Close stdout right away so the reader drains to the wait path.
	go func() { _ = pw.Close() }()
	return proc, nil
}

type crashProcess struct {
	stdoutR    *io.PipeReader
	stdoutW    *io.PipeWriter
	stdinMu    sync.Mutex
	stdinBuf   *bytes.Buffer
	done       chan struct{}
	once       sync.Once
	pid        int
	exitCode   int
	stderrTail string
}

func (p *crashProcess) Stdout() io.Reader     { return p.stdoutR }
func (p *crashProcess) Stdin() io.WriteCloser { return &crashStdin{p: p} }
func (p *crashProcess) Pid() int              { return p.pid }
func (p *crashProcess) Binary() string        { return "" }
func (p *crashProcess) Argv() []string        { return nil }
func (p *crashProcess) Env() []string         { return nil }
func (p *crashProcess) StderrTail() string    { return p.stderrTail }

// Wait returns a non-zero-exit error so the reader classifies the exit
// as ExitError and reads the exit code off the error.
func (p *crashProcess) Wait() error { return crashExitErr{code: p.exitCode} }

func (p *crashProcess) Kill() error {
	p.once.Do(func() {
		_ = p.stdoutR.Close()
		_ = p.stdoutW.Close()
		close(p.done)
	})
	return nil
}

type crashStdin struct{ p *crashProcess }

func (f *crashStdin) Write(b []byte) (int, error) {
	f.p.stdinMu.Lock()
	defer f.p.stdinMu.Unlock()
	return f.p.stdinBuf.Write(b)
}
func (f *crashStdin) Close() error { return nil }

// crashExitErr mimics *exec.ExitError: non-nil, non-clean, with an
// ExitCode() method the reader path type-asserts.
type crashExitErr struct{ code int }

func (e crashExitErr) Error() string { return "exit status " + strconv.Itoa(e.code) }
func (e crashExitErr) ExitCode() int { return e.code }

func TestAgentCrashCarriesExitDetail(t *testing.T) {
	spawner := &crashSpawner{exitCode: 1, stderrTail: "error: unsupported wire_api\nfatal"}
	details := make(chan ExitDetail, 1)
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   5 * time.Second,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       spawner,
		State:         state.New(nil),
		OnExitDetail:  func(d ExitDetail) { details <- d },
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-details:
		if d.Reason != ExitError {
			t.Fatalf("reason = %v, want ExitError", d.Reason)
		}
		if d.ExitCode != 1 {
			t.Errorf("exit code = %d, want 1", d.ExitCode)
		}
		if !strings.Contains(d.StderrTail, "unsupported wire_api") {
			t.Errorf("stderr tail missing crash msg: %q", d.StderrTail)
		}
		if !strings.Contains(d.ReasonDetail, "unsupported wire_api") {
			t.Errorf("reason detail should quote stderr first line: %q", d.ReasonDetail)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnExitDetail did not fire")
	}
}

func TestAgentReadsStreamUpdatesState(t *testing.T) {
	spawner := &fakeSpawner{
		Lines: [][]string{{
			`{"type":"system","subtype":"init","session_id":"abc-123"}`,
			`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
			`{"type":"result","subtype":"success","is_error":false,"result":"hi"}`,
		}},
	}
	collected := collectingHook{}
	st := state.New(nil)
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   500 * time.Millisecond,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       spawner,
		State:         st,
		OnEvent:       collected.add,
	})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	waitFor(t, func() bool { return !a.Running() }, time.Second)

	got := collected.types()
	wantTypes := []event.EventType{event.SessionStart, event.TextDelta, event.Done}
	if len(got) < len(wantTypes) {
		t.Fatalf("event count: got %d (%v)", len(got), got)
	}
	for i, w := range wantTypes {
		if got[i] != w {
			t.Fatalf("event[%d]: got %v want %v", i, got[i], w)
		}
	}
	if a.ResumeID() != "abc-123" {
		t.Fatalf("resume: %q", a.ResumeID())
	}
	if st.Current() != state.Idle {
		t.Fatalf("state after Done: %v", st.Current())
	}
}

func TestAgentSendWritesUserEnvelope(t *testing.T) {
	spawner := &fakeSpawner{Lines: [][]string{{}}}
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   2 * time.Second,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       spawner,
		State:         state.New(nil),
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Send(`hello "world"`); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Stop and read what was written into stdin.
	_ = a.Stop()

	got, err := spawner.Last.recordedStdin()
	if err != nil {
		t.Fatalf("read stdin: %v", err)
	}
	gotStr := string(got)
	if !strings.Contains(gotStr, `"type":"user"`) {
		t.Fatalf("missing user envelope: %s", gotStr)
	}
	if !strings.Contains(gotStr, `"hello \"world\""`) {
		t.Fatalf("escape failed: %s", gotStr)
	}
}

func TestAgentResumeIDPassedToSpawn(t *testing.T) {
	spawner := &fakeSpawner{Lines: [][]string{{`{"type":"result","subtype":"success","is_error":false,"result":""}`}}}
	a := New(Options{
		Workspace:     t.TempDir(),
		ResumeID:      "prev-session",
		IdleTimeout:   500 * time.Millisecond,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       spawner,
		State:         state.New(nil),
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !a.Running() }, time.Second)
	if spawner.Last.opt.ResumeID != "prev-session" {
		t.Fatalf("ResumeID not forwarded: %q", spawner.Last.opt.ResumeID)
	}
}

func TestAgentIdleTTLKills(t *testing.T) {
	// Fake never emits anything — agent should kill itself after idle.
	pr, pw := makePipePair()
	spawner := &keepAliveSpawner{stdoutR: pr, stdoutW: pw}
	exitReasons := make(chan ExitReason, 1)
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   100 * time.Millisecond,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       spawner,
		State:         state.New(nil),
		OnExit:        func(r ExitReason) { exitReasons <- r },
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-exitReasons:
		if r != ExitIdle {
			t.Fatalf("exit reason: %v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idle TTL did not fire")
	}
}

// TestAgentStopDoesNotHangOnStubbornStdout reproduces the Windows bug
// where killing a subprocess does not immediately EOF its stdout pipe.
// Stop() must return within a bounded time even when the reader loop's
// scanner.Scan() is still blocking on a pipe that never closes.
func TestAgentStopDoesNotHangOnStubbornStdout(t *testing.T) {
	spawner := &stubbornSpawner{}
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   10 * time.Second,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       spawner,
		State:         state.New(nil),
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- a.Stop() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() deadlocked — subprocess stdout never EOF'd after Kill (Windows hang bug)")
	}
}

func TestAgentStopIdempotent(t *testing.T) {
	spawner := &fakeSpawner{Lines: [][]string{{}}}
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   2 * time.Second,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       spawner,
		State:         state.New(nil),
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := a.Stop(); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// helpers ---------------------------------------------------------

type collectingHook struct {
	mu     sync.Mutex
	events []event.AgentEvent
}

func (c *collectingHook) add(ev event.AgentEvent) {
	c.mu.Lock()
	c.events = append(c.events, ev)
	c.mu.Unlock()
}

func (c *collectingHook) types() []event.EventType {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]event.EventType, 0, len(c.events))
	for _, e := range c.events {
		if e.Type == event.Unknown {
			continue
		}
		out = append(out, e.Type)
	}
	return out
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
