package provider

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

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
