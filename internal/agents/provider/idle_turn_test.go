package provider

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/state"
)

// newTurnIdleRig is newIdleKillRig with a mid-turn ceiling above the idle
// window.
func newTurnIdleRig(t *testing.T, idle, turnIdle time.Duration) *idleKillRig {
	t.Helper()
	pr, pw := makePipePair()
	exited := make(chan ExitReason, 4)
	a := New(Options{
		Workspace:       t.TempDir(),
		IdleTimeout:     idle,
		TurnIdleTimeout: turnIdle,
		ParserFactory:   func() event.Parser { return event.NewClaudeParser() },
		Spawner:         &keepAliveSpawner{stdoutR: pr, stdoutW: pw},
		State:           state.New(nil),
		OnExit:          func(r ExitReason, _ string) { exited <- r },
	})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Stop() })
	return &idleKillRig{a: a, pw: pw, exited: exited}
}

func (r *idleKillRig) expectAlive(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case reason := <-r.exited:
		t.Fatalf("killed (%v) inside the mid-turn ceiling", reason)
	case <-time.After(d):
	}
}

// A model thinking over a long context says nothing on stdout: an open
// turn is not killed after IdleTimeout, only after TurnIdleTimeout.
func TestAgentIdleSparesOpenTurnUntilCeiling(t *testing.T) {
	r := newTurnIdleRig(t, 50*time.Millisecond, 500*time.Millisecond)
	if err := r.a.Send("think hard"); err != nil {
		t.Fatal(err)
	}
	r.expectAlive(t, 300*time.Millisecond) // six idle windows
	if got := r.waitExit(t); got != ExitIdle {
		t.Fatalf("exit reason %v, want ExitIdle", got)
	}
}

// Output mid-turn restarts the ceiling: it is a silence limit, not a turn
// length limit.
func TestAgentIdleCeilingCountsFromLastOutput(t *testing.T) {
	r := newTurnIdleRig(t, 30*time.Millisecond, 300*time.Millisecond)
	if err := r.a.Send("go"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		time.Sleep(150 * time.Millisecond)
		r.say(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"still here"}]}}`)
	}
	r.expectAlive(t, 150*time.Millisecond)
	if got := r.waitExit(t); got != ExitIdle {
		t.Fatalf("exit reason %v, want ExitIdle", got)
	}
}

// Between turns IdleTimeout applies as before: a finished agent is reaped
// on the short window.
func TestAgentIdleBetweenTurnsKeepsShortWindow(t *testing.T) {
	r := newTurnIdleRig(t, 50*time.Millisecond, 10*time.Second)
	if err := r.a.Send("hi"); err != nil {
		t.Fatal(err)
	}
	r.say(t, `{"type":"result","subtype":"success","is_error":false,"result":"hi"}`)
	if got := r.waitExit(t); got != ExitIdle {
		t.Fatalf("exit reason %v, want ExitIdle", got)
	}
}

// A turn that launched a background job ends at once; the job's report
// comes later as a turn of its own. The silence in between is the job
// running, so the ceiling applies — and once the report turn ends with
// nothing new launched, the short window is back.
func TestAgentIdleSparesPendingBackgroundTask(t *testing.T) {
	r := newTurnIdleRig(t, 50*time.Millisecond, 400*time.Millisecond)
	if err := r.a.Send("run the build in the background"); err != nil {
		t.Fatal(err)
	}
	r.say(t, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"make","run_in_background":true}}]}}`)
	r.say(t, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"started bg1"}]}}`)
	r.say(t, `{"type":"result","subtype":"success","is_error":false,"result":"started"}`)
	r.expectAlive(t, 250*time.Millisecond)
	if r.a.StopIfIdle() {
		t.Fatal("StopIfIdle reaped an agent whose background task is pending")
	}
	// The CLI reports the finished job on its own.
	r.say(t, `{"type":"assistant","message":{"content":[{"type":"text","text":"build done"}]}}`)
	r.say(t, `{"type":"result","subtype":"success","is_error":false,"result":"build done"}`)
	start := time.Now()
	if got := r.waitExit(t); got != ExitIdle {
		t.Fatalf("exit reason %v, want ExitIdle", got)
	}
	if el := time.Since(start); el > 300*time.Millisecond {
		t.Fatalf("reaped after %v; the background task had reported, want the short window", el)
	}
}

func TestLaunchesBackground(t *testing.T) {
	cases := []struct {
		ev   event.AgentEvent
		want bool
	}{
		{event.AgentEvent{ToolName: "Bash", ToolInput: `{"command":"make","run_in_background":true}`}, true},
		{event.AgentEvent{ToolName: "Bash", ToolInput: `{"command":"make","run_in_background":false}`}, false},
		{event.AgentEvent{ToolName: "Bash", ToolInput: `{"command":"echo run_in_background"}`}, false},
		{event.AgentEvent{ToolName: "Agent", ToolInput: `{"prompt":"x","run_in_background":true}`}, true},
		{event.AgentEvent{ToolName: "Monitor", ToolInput: `{}`}, true},
		{event.AgentEvent{ToolName: "Read", ToolInput: `{"file_path":"/x"}`}, false},
	}
	for _, c := range cases {
		if got := launchesBackground(c.ev); got != c.want {
			t.Errorf("%s %s: got %v, want %v", c.ev.ToolName, c.ev.ToolInput, got, c.want)
		}
	}
}
