package provider

// Integration tests for the codex RespawnOnSend flow.
//
// Codex is one-shot per invocation: each Send() must kill the current
// process and spawn a new one with the message as InitialMessage. These
// tests verify that contract using the fakeSpawner without needing a
// real codex binary.

import (
	"context"
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
		RespawnOnSend: true,
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

	// proc[0] = initial Start() spawn (no message), proc[1] = respawn from Send()
	waitFor(t, func() bool { return sp.callsSnapshot() >= 2 }, time.Second)
	proc := sp.procAt(1)
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
		codexLines("sess-abc", "first reply"),
		codexLines("sess-abc", "second reply"),
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
		RespawnOnSend: true,
	})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// First turn.
	if err := a.Send("first"); err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	// Wait for respawn from first Send to exit so session_id is captured.
	// proc[0]=Start spawn, proc[1]=first Send respawn
	waitFor(t, func() bool { return sp.callsSnapshot() >= 2 && !a.Running() }, time.Second)

	if got := a.ResumeID(); got != "sess-abc" {
		t.Fatalf("ResumeID after first turn = %q, want sess-abc", got)
	}

	// Second turn — should respawn with resume id.
	if err := a.Send("second"); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	// proc[2] = second Send respawn
	waitFor(t, func() bool { return sp.callsSnapshot() >= 3 }, time.Second)

	proc2 := sp.procAt(2)
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

	// proc[0]=Start, proc[1]=Send respawn
	waitFor(t, func() bool { return sp.callsSnapshot() >= 2 }, time.Second)
	proc := sp.procAt(1)
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
		{}, // proc[0]: initial Start() — no output
		codexLines("sess-1", "pong"), // proc[1]: respawn from Send()
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
		RespawnOnSend: true,
	})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := a.Send("ping"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// proc[0]=Start, proc[1]=Send respawn — wait for respawn to finish
	waitFor(t, func() bool { return sp.callsSnapshot() >= 2 }, time.Second)
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
