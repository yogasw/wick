package pool

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
)

// sendToProcess writes text straight into the live process, the way a
// send that passed the failover hold check a moment earlier does.
func sendToProcess(t *testing.T, p *Pool, text string) {
	t.Helper()
	p.mu.Lock()
	e := p.active[sessionKey("S", "default")]
	p.mu.Unlock()
	if e == nil {
		t.Errorf("no live process for %q", text)
		return
	}
	if err := e.agent.Send(text); err != nil {
		t.Errorf("send %q: %v", text, err)
	}
}

// A message written to a claude process while the pool decides on its
// failed turn reaches the target with the turn, instead of dying with
// the process the failover stops.
func TestFailoverKeepsMessageSentWhileDeciding(t *testing.T) {
	p, _, sp, _ := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines(""), nil},
		"b": {foOKLines("done on b")},
	}, false)
	p.foStage = func(stage string) {
		if stage == "deciding" {
			sendToProcess(t, p, "two")
		}
	}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 10*time.Second)
	if got := sp.sent("b")[0]; got != "hello\ntwo" {
		t.Fatalf("b got %q, want the turn's message then the one sent meanwhile", got)
	}
}

// Messages taken off the failed process go out on it after all when the
// switch is refused, instead of being dropped with the job.
func TestFailoverSwitchFailureKeepsTakenMessages(t *testing.T) {
	p, _, sp, rec := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines(""), nil, foOKLines("ok")},
		"b": {foOKLines("never")},
	}, false)
	p.foStage = func(stage string) {
		switch stage {
		case "deciding":
			sendToProcess(t, p, "two")
		case "stopped":
			saveInstance(t, provider.Instance{Type: provider.TypeClaude, Name: "b", Disabled: true})
		}
	}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sp.sent("a")) == 3 }, 10*time.Second)
	if got := sp.sent("a")[2]; got != "two" {
		t.Fatalf("a got %q, want the taken message", got)
	}
	if rec.count(event.Error) != 1 {
		t.Fatalf("errors = %d, the original error must stand", rec.count(event.Error))
	}
	if n := spawnCount(sp, "b"); n != 0 {
		t.Fatalf("claude/b spawned %d times", n)
	}
}

// A target that cannot start is marked, the agent goes back to where the
// turn failed, and nothing of the failed turn is replayed with the next
// message.
func TestFailoverTargetStartFailureGoesBack(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("half"), foOKLines("fine")},
	}, false)
	sp.failStart = map[string]bool{"b": true}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	bKey := provider.HealthKey(provider.Instance{Type: provider.TypeClaude, Name: "b"})
	waitFor(t, func() bool {
		_, bad := provider.HealthOf(bKey)
		return bad && agentNow(t, layout).Provider == "claude/a" && !p.FailingOver("S", "default")
	}, 10*time.Second)
	if h, _ := provider.HealthOf(bKey); h.Kind != event.ErrUnavailable {
		t.Fatalf("b health = %+v", h)
	}
	if sess, _ := session.Load(layout, "S"); len(sess.Meta.PendingInput) != 0 {
		t.Fatalf("buffer kept %q", sess.Meta.PendingInput)
	}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "next"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sp.sent("a")) == 2 }, 10*time.Second)
	if got := sp.sent("a")[1]; got != "next" {
		t.Fatalf("a got %q, want only the new message", got)
	}
	if n := spawnCount(sp, "b"); n != 1 {
		t.Fatalf("claude/b spawned %d times, want 1", n)
	}
}

// A re-send whose spawn failed before draining (the buffer still has the
// continue message at its head) leaves only the person's messages.
func TestResendSpawnFailedDropsTheContinue(t *testing.T) {
	p, layout, _, _ := failoverEnv(t, true, nil, false)
	if _, err := provider.ApplySwitch(layout, "S", "default", "claude/b", provider.SwitchOptions{Source: "failover", Failover: true, MsgKey: "k", ExpectFrom: "claude/a"}); err != nil {
		t.Fatal(err)
	}
	buf, err := p.bufferFor("S")
	if err != nil {
		t.Fatal(err)
	}
	if err := buf.Prepend("[wick] continue", "two"); err != nil {
		t.Fatal(err)
	}
	p.resendSpawnFailed("S", "default", resendMark{msgKey: "k", from: "claude/a", target: "claude/b", cont: "[wick] continue"}, nil, true)
	if got := buf.Lines(); len(got) != 1 || got[0] != "two" {
		t.Fatalf("buffer = %q", got)
	}
	if sess, _ := session.Load(layout, "S"); len(sess.Meta.PendingInput) != 1 {
		t.Fatalf("persisted buffer = %q", sess.Meta.PendingInput)
	}
	if a := agentNow(t, layout); a.Provider != "claude/a" || a.Failover == nil || a.Failover.HomeProvider != "claude/a" {
		t.Fatalf("agent = %+v", a)
	}
}

// A re-send whose spawn failed for a reason that is not the target's (here
// the pool closing for a restart) drops the continue message and ends the
// run, but neither marks the target down nor moves the agent back.
func TestResendSpawnFailedNotProviderKeepsTarget(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, true, nil, false)
	if _, err := provider.ApplySwitch(layout, "S", "default", "claude/b", provider.SwitchOptions{Source: "failover", Failover: true, MsgKey: "k", ExpectFrom: "claude/a"}); err != nil {
		t.Fatal(err)
	}
	buf, err := p.bufferFor("S")
	if err != nil {
		t.Fatal(err)
	}
	if err := buf.Prepend("[wick] continue", "two"); err != nil {
		t.Fatal(err)
	}
	key := sessionKey("S", "default")
	p.failoverRunFor(key)
	p.foMu.Lock()
	p.foResend = map[string]resendMark{key: {msgKey: "k", from: "claude/a", target: "claude/b", cont: "[wick] continue"}}
	p.foMu.Unlock()
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	err = p.spawn(context.Background(), "S", "default", "ui")
	p.mu.Lock()
	p.closed = false
	p.mu.Unlock()
	if err == nil || !strings.Contains(err.Error(), "pool closed") {
		t.Fatalf("spawn err = %v", err)
	}
	if _, bad := provider.HealthOf(provider.HealthKey(provider.Instance{Type: provider.TypeClaude, Name: "b"})); bad {
		t.Fatal("claude/b marked unhealthy for a pool error")
	}
	if a := agentNow(t, layout); a.Provider != "claude/b" || (a.Failover != nil && a.Failover.MsgKey != "") {
		t.Fatalf("agent = %+v", a)
	}
	if got := buf.Lines(); len(got) != 1 || got[0] != "two" {
		t.Fatalf("buffer = %q", got)
	}
	p.foMu.Lock()
	_, inRun := p.foRuns[key]
	_, marked := p.foResend[key]
	p.foMu.Unlock()
	if inRun || marked {
		t.Fatalf("run kept = %v, re-send mark kept = %v", inRun, marked)
	}
	if n := spawnCount(sp, "b"); n != 0 {
		t.Fatalf("claude/b spawned %d times", n)
	}
}

// A workflow session neither fails over nor returns home.
func TestFailoverSkipsWorkflowSessions(t *testing.T) {
	p, layout, sp, rec := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foOKLines("never")},
	}, false)
	sess, err := session.Load(layout, "S")
	if err != nil {
		t.Fatal(err)
	}
	sess.Meta.Origin = session.OriginWorkflow
	if err := session.SaveMeta(layout, "S", sess.Meta); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return rec.count(event.Error) == 1 }, 10*time.Second)
	if n := spawnCount(sp, "b"); n != 0 {
		t.Fatalf("claude/b spawned %d times", n)
	}
	if a := agentNow(t, layout); a.Provider != "claude/a" {
		t.Fatalf("provider moved to %q", a.Provider)
	}
	if v := p.decideFailover(provider.FailoverRequest{SessionID: "S", AgentName: "default", Instance: "claude/a", Kind: event.ErrLimit, Text: "x"}); v.Rerun {
		t.Fatal("decideFailover moved a workflow session")
	}

	// On a stand-in with a healthy home: a workflow session stays, any
	// other returns.
	p.Stop()
	standIn := func() {
		t.Helper()
		if err := session.UpdateAgents(layout, "S", func(agents []session.AgentEntry) ([]session.AgentEntry, bool, error) {
			agents[0].Provider = "claude/b"
			agents[0].Failover = &session.FailoverState{HomeProvider: "claude/a"}
			return agents, true, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	saveInstance(t, provider.Instance{Type: provider.TypeClaude, Name: "a", Failover: &provider.FailoverConfig{
		Enabled: true, ReturnHome: true, Triggers: provider.FailoverTriggers,
		Chain: []provider.FailoverTarget{{Instance: "claude/b"}, {Instance: "claude/c"}},
	}})
	provider.MarkHealthy(provider.HealthKey(provider.Instance{Type: provider.TypeClaude, Name: "a"}))
	p2 := New(PoolConfig{MaxConcurrent: 4, IdleTimeout: 5 * time.Second, Layout: layout, Factory: p.cfg.Factory})
	t.Cleanup(p2.Stop)
	standIn()
	if p2.maybeReturnHome("S", "default") {
		t.Fatal("a workflow session returned home")
	}
	sess.Meta.Origin = session.OriginUI
	if err := session.SaveMeta(layout, "S", sess.Meta); err != nil {
		t.Fatal(err)
	}
	standIn()
	if !p2.maybeReturnHome("S", "default") {
		t.Fatal("control: a UI session did not return home")
	}
}

// Messages held during the first hop reach the second hop's target when
// the first target fails too before answering.
func TestFailoverSecondHopKeepsHeldMessages(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foLimitLines("")},
		"c": {foOKLines("done on c")},
	}, false)
	var once sync.Once
	p.foStage = func(stage string) {
		if stage == "stopped" {
			once.Do(func() {
				if err := p.Send(context.Background(), "S", "default", "ui", "user", "two"); err != nil {
					t.Errorf("send: %v", err)
				}
			})
		}
	}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sp.sent("c")) == 1 }, 15*time.Second)
	got := sp.sent("c")[0]
	if !strings.HasPrefix(got, "hello") || !strings.Contains(got, "two") {
		t.Fatalf("c got %q, want the turn and the held message", got)
	}
	if sp.sent("b")[0] != "hello\ntwo" {
		t.Fatalf("b got %q", sp.sent("b")[0])
	}
	if a := agentNow(t, layout); a.Provider != "claude/c" {
		t.Fatalf("provider = %q", a.Provider)
	}
}

// A failover that ends without its re-send leaves no run behind for the
// next message to inherit.
func TestAbortedFailoverLeavesNoRun(t *testing.T) {
	p, _, _, _ := failoverEnv(t, true, nil, false)
	key := sessionKey("S", "default")
	run := p.failoverRunFor(key)
	run.msgKey = "old"
	p.foMu.Lock()
	p.foResend = map[string]resendMark{key: {msgKey: "old"}}
	p.foMu.Unlock()
	p.abortFailover("S", "default")
	p.foMu.Lock()
	_, inRun := p.foRuns[key]
	_, marked := p.foResend[key]
	p.foMu.Unlock()
	if inRun || marked {
		t.Fatalf("run = %v, re-send mark = %v after an abort", inRun, marked)
	}
}
