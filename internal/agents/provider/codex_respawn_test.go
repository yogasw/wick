package provider

// Integration tests for the codex RespawnOnSend flow.
//
// Codex is one-shot per invocation: each Send() must kill the current
// process and spawn a new one with the message as InitialMessage. These
// tests verify that contract using the fakeSpawner without needing a
// real codex binary.

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

// codexLines returns canned codex --json JSONL output matching actual codex 0.129 wire format.
func codexLines(threadID, text string) []string {
	lines := []string{}
	if threadID != "" {
		lines = append(lines, `{"type":"thread.started","thread_id":"`+threadID+`"}`)
	}
	lines = append(lines, `{"type":"turn.started"}`)
	if text != "" {
		lines = append(lines, `{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"`+text+`"}}`)
	}
	lines = append(lines, `{"type":"turn.completed","usage":{}}`)
	return lines
}

// newCodexAgent builds an Agent wired for codex (RespawnOnSend=true,
// CodexParser) backed by the given fakeSpawner.
func newCodexAgent(t *testing.T, sp *fakeSpawner) *Agent {
	t.Helper()
	st := state.New(nil)
	return New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   500 * time.Millisecond,
		ParserFactory: func() event.Parser { return event.NewCodexParser() },
		Spawner:       sp,
		State:         st,
		SendMode:      SendRespawnQueue,
	})
}

// TestCodexRespawnOnSend_FirstMessage verifies that the first Send()
// after Start triggers a spawn with InitialMessage set as positional arg.
func TestCodexRespawnOnSend_FirstMessage(t *testing.T) {
	sp := &fakeSpawner{Lines: [][]string{
		codexLines("sess-1", "hello back"),
	}}
	a := newCodexAgent(t, sp)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := a.Send("hello codex"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Start() defers spawn for RespawnOnSend providers, so proc[0] is the
	// first respawn (carrying the prompt), not an empty Start spawn.
	waitFor(t, func() bool { return sp.callsSnapshot() >= 1 }, time.Second)
	proc := sp.procAt(0)
	if proc == nil {
		t.Fatal("respawn not recorded")
	}

	if got := proc.opt.InitialMessage; got != "hello codex" {
		t.Errorf("InitialMessage = %q, want %q", got, "hello codex")
	}
	if got := proc.opt.ResumeID; got != "" {
		t.Errorf("first Send should have empty ResumeID, got %q", got)
	}
}

// TestCodexRespawnOnSend_SessionIDCaptured verifies that session_started
// event captures the session ID and it is forwarded as ResumeID on the
// second spawn.
func TestCodexRespawnOnSend_SessionIDCaptured(t *testing.T) {
	sp := &fakeSpawner{Lines: [][]string{
		codexLines("sess-abc", "first reply"),  // proc[0]: first Send respawn
		codexLines("sess-abc", "second reply"), // proc[1]: second Send respawn
	}}
	collected := collectingHook{}
	st := state.New(nil)
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   500 * time.Millisecond,
		ParserFactory: func() event.Parser { return event.NewCodexParser() },
		Spawner:       sp,
		State:         st,
		OnEvent:       collected.add,
		SendMode:      SendRespawnQueue,
	})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// First turn.
	if err := a.Send("first"); err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	// Wait for the first Send respawn to exit so session_id is captured.
	// proc[0] = first Send respawn (Start defers spawn for codex).
	waitFor(t, func() bool { return sp.callsSnapshot() >= 1 && !a.Running() }, time.Second)

	if got := a.ResumeID(); got != "sess-abc" {
		t.Fatalf("ResumeID after first turn = %q, want sess-abc", got)
	}

	// Second turn — should respawn with resume id.
	if err := a.Send("second"); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	// proc[1] = second Send respawn
	waitFor(t, func() bool { return sp.callsSnapshot() >= 2 }, time.Second)

	proc2 := sp.procAt(1)
	if proc2 == nil {
		t.Fatal("second spawn not recorded")
	}
	if got := proc2.opt.ResumeID; got != "sess-abc" {
		t.Errorf("second spawn ResumeID = %q, want sess-abc", got)
	}
	if got := proc2.opt.InitialMessage; got != "second" {
		t.Errorf("second spawn InitialMessage = %q, want second", got)
	}
}

// TestCodexRespawnOnSend_NoStdinWrite verifies that Send() does NOT write
// to stdin (codex reads prompt as positional arg, not stdin).
func TestCodexRespawnOnSend_NoStdinWrite(t *testing.T) {
	sp := &fakeSpawner{Lines: [][]string{
		codexLines("sess-1", "ok"),
	}}
	a := newCodexAgent(t, sp)

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Send("do something"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// proc[0] = Send respawn (Start defers spawn for codex)
	waitFor(t, func() bool { return sp.callsSnapshot() >= 1 }, time.Second)
	proc := sp.procAt(0)
	if proc == nil {
		t.Fatal("no respawn")
	}

	raw, _ := proc.recordedStdin()
	if len(raw) > 0 {
		t.Errorf("stdin should be empty for codex, got: %q", raw)
	}
}

// TestCodexRespawnOnSend_EventsDelivered verifies that TextDelta events
// from the codex JSON stream are delivered via OnEvent.
func TestCodexRespawnOnSend_EventsDelivered(t *testing.T) {
	sp := &fakeSpawner{Lines: [][]string{
		codexLines("sess-1", "pong"), // proc[0]: respawn from Send() (Start defers)
	}}
	collected := collectingHook{}
	st := state.New(nil)
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   500 * time.Millisecond,
		ParserFactory: func() event.Parser { return event.NewCodexParser() },
		Spawner:       sp,
		State:         st,
		OnEvent:       collected.add,
		SendMode:      SendRespawnQueue,
	})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Send("ping"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// proc[0] = Send respawn — wait for it to finish
	waitFor(t, func() bool { return sp.callsSnapshot() >= 1 }, time.Second)
	waitFor(t, func() bool { return !a.Running() }, time.Second)

	types := collected.types()
	has := func(et event.EventType) bool {
		for _, tt := range types {
			if tt == et {
				return true
			}
		}
		return false
	}
	if !has(event.SessionStart) {
		t.Errorf("missing SessionStart in events: %v", types)
	}
	if !has(event.TextDelta) {
		t.Errorf("missing TextDelta in events: %v", types)
	}
}

// TestCodexRespawnKillsHungSpawn reproduces the real codex bug: the
// initial Start() spawn blocks (codex exec with no prompt waits on
// stdin) and its stdout never EOFs after Kill (Windows). The first
// Send() must respawn without deadlocking at the kill-current-process
// step. Spawn #1 is stubborn (hangs); spawn #2 is a normal fake.
func TestCodexRespawnKillsHungSpawn(t *testing.T) {
	sp := &mixedSpawner{
		second: &fakeSpawner{Lines: [][]string{
			codexLines("sess-1", "hi"),
		}},
	}
	st := state.New(nil)
	a := New(Options{
		Workspace:     t.TempDir(),
		IdleTimeout:   10 * time.Second,
		ParserFactory: func() event.Parser { return event.NewCodexParser() },
		Spawner:       sp,
		State:         st,
		SendMode:      SendRespawnQueue,
	})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- a.Send("first prompt") }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Send returned error: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Send() deadlocked respawning over a hung spawn (codex stuck-spawning bug)")
	}
}

// mixedSpawner returns a stubborn (hanging) process on the first Spawn
// and delegates to `second` for all subsequent spawns.
type mixedSpawner struct {
	mu     sync.Mutex
	calls  int
	second *fakeSpawner
}

func (s *mixedSpawner) Spawn(ctx context.Context, opt SpawnOptions) (Process, error) {
	s.mu.Lock()
	n := s.calls
	s.calls++
	s.mu.Unlock()
	if n == 0 {
		pr, pw := io.Pipe()
		return &stubbornProcess{
			stdoutR: pr, stdoutW: pw,
			stdinBuf: &bytes.Buffer{},
			done:     make(chan struct{}),
			pid:      93000,
		}, nil
	}
	return s.second.Spawn(ctx, opt)
}

// fakeSpawner helpers used here but defined in fake_spawner_test.go.
// Expose callsSnapshot and procAt as package-level helpers.
func (s *fakeSpawner) callsSnapshot() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Calls
}
func (s *fakeSpawner) procAt(i int) *fakeProcess {
	s.mu.Lock()
	defer s.mu.Unlock()
	if i < 0 || i >= len(s.Procs) {
		return nil
	}
	return s.Procs[i]
}
func (s *fakeSpawner) procCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Procs)
}
