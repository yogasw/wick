package pool

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/state"
)

// withAgentDefault wires provider.AgentDefault for one test.
func withAgentDefault(t *testing.T, f func(ctx context.Context, sessionID string) (string, string, bool)) {
	t.Helper()
	prev := provider.AgentDefault
	provider.AgentDefault = f
	t.Cleanup(func() { provider.AgentDefault = prev })
}

// An agent's default that moved after the session was made: the next
// message stops the idle process and runs on the default, model pinned.
func TestFollowDefaultSwitchesAtTurnBoundary(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, false, map[string][][]string{
		"a": {foOKLines("on a")},
		"b": {foOKLines("on b")},
	}, false)
	withAgentDefault(t, nil)
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
	waitLifecycle(t, p, state.LifecycleIdle)
	p.mu.Lock()
	old := p.active[sessionKey("S", "default")].agent
	p.mu.Unlock()

	withAgentDefault(t, func(_ context.Context, sid string) (string, string, bool) {
		return "claude/b", "opus", sid == "S"
	})
	sendMsg(t, p, "m2")
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 5*time.Second)
	if got := sp.sent("b")[0]; got != "m2" {
		t.Fatalf("b got %q", got)
	}
	if n := len(sp.sent("a")); n != 1 {
		t.Fatalf("a was sent %d messages, want 1", n)
	}
	a := agentNow(t, layout)
	if a.Provider != "claude/b" || a.ModelID != "opus" || a.Failover == nil || a.Failover.HomeProvider != "claude/b" {
		t.Fatalf("agent = %+v", a)
	}
	if !old.ExitReported() {
		t.Fatal("the process on the old provider still runs")
	}
}

// A switch persisted while a turn ran (agents.json names b, the live
// process is a's) is carried out at the next message, and a turn in
// flight is never stopped for it.
func TestFollowDefaultRestartsStaleProcess(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, false, map[string][][]string{
		"a": {foOKLines("on a")},
		"b": {foOKLines("on b")},
	}, false)
	withAgentDefault(t, nil)
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
	waitLifecycle(t, p, state.LifecycleIdle)

	if err := session.SetAgentProvider(layout, "S", "default", "claude/b"); err != nil {
		t.Fatal(err)
	}
	// A spawn in flight: nothing moves.
	key := sessionKey("S", "default")
	p.mu.Lock()
	p.spawningKeys[key] = struct{}{}
	p.mu.Unlock()
	if p.maybeFollowDefault(context.Background(), "S", "default") {
		t.Fatal("stopped while a spawn was in flight")
	}
	p.mu.Lock()
	delete(p.spawningKeys, key)
	p.mu.Unlock()

	sendMsg(t, p, "m2")
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 5*time.Second)
	if n := len(sp.sent("a")); n != 1 {
		t.Fatalf("a was sent %d messages, want 1", n)
	}
}

// Nothing to follow: the live process keeps the message, no respawn.
func TestFollowDefaultNoopKeepsProcess(t *testing.T) {
	p, _, sp, _ := failoverEnv(t, false, map[string][][]string{
		"a": {foOKLines("one"), foOKLines("two")},
	}, false)
	withAgentDefault(t, func(context.Context, string) (string, string, bool) { return "claude/a", "", true })
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
	waitLifecycle(t, p, state.LifecycleIdle)
	sendMsg(t, p, "m2")
	waitFor(t, func() bool { return len(sp.sent("a")) == 2 }, 5*time.Second)
	if n := spawnCount(sp, "a"); n != 1 {
		t.Fatalf("a spawned %d times, want 1", n)
	}
}
