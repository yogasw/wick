package pool

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/state"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

var keyA = provider.HealthKey(provider.Instance{Type: provider.TypeClaude, Name: "a"})

// returnEnv is failoverEnv with the session already on stand-in `on`
// (home claude/a, which returns home unless homeCfg says otherwise) and
// claude/a limited for the next hour.
func returnEnv(t *testing.T, on string, turns map[string][][]string, homeCfg *provider.FailoverConfig) (*Pool, config.Layout, *turnSpawner) {
	t.Helper()
	p, layout, sp, _ := failoverEnv(t, true, turns, false)
	if homeCfg == nil {
		homeCfg = &provider.FailoverConfig{
			Enabled: true, ReturnHome: true, Triggers: provider.FailoverTriggers,
			Chain: []provider.FailoverTarget{{Instance: "claude/b"}, {Instance: "claude/c"}},
		}
	}
	saveInstance(t, provider.Instance{Type: provider.TypeClaude, Name: "a", Failover: homeCfg})
	if err := session.SetAgentProvider(layout, "S", "default", on); err != nil {
		t.Fatal(err)
	}
	if err := session.SetFailoverState(layout, "S", "default", session.FailoverState{HomeProvider: "claude/a"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	provider.MarkUnhealthy(keyA, provider.Health{Kind: event.ErrLimit, Since: now.Add(-time.Hour), Until: now.Add(time.Hour)})
	return p, layout, sp
}

// limitLifted makes claude/a's limit one whose Until has passed.
func limitLifted() {
	now := time.Now()
	provider.ResetHealth(keyA)
	provider.MarkUnhealthy(keyA, provider.Health{Kind: event.ErrLimit, Since: now.Add(-2 * time.Hour), Until: now.Add(-time.Second)})
}

func sendMsg(t *testing.T, p *Pool, text string) {
	t.Helper()
	if err := p.Send(context.Background(), "S", "default", "ui", "user", text); err != nil {
		t.Fatal(err)
	}
}

func waitLifecycle(t *testing.T, p *Pool, want state.Lifecycle) {
	t.Helper()
	waitFor(t, func() bool {
		p.mu.Lock()
		e, ok := p.active[sessionKey("S", "default")]
		p.mu.Unlock()
		if !ok || e.state == nil {
			return false
		}
		if want == state.LifecycleIdle {
			return entryIdle(e)
		}
		return e.state.Lifecycle() == want
	}, 5*time.Second)
}

func turnKinds(t *testing.T, layout config.Layout) []store.ConversationTurn {
	t.Helper()
	var out []store.ConversationTurn
	err := storage.ReadJSONL(layout.SessionConversation("S"), func(line []byte) bool {
		var tr store.ConversationTurn
		if json.Unmarshal(line, &tr) == nil && tr.Kind != "" {
			out = append(out, tr)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func lastReturn(t *testing.T, layout config.Layout) store.ConversationTurn {
	t.Helper()
	ks := turnKinds(t, layout)
	if len(ks) == 0 || ks[len(ks)-1].Kind != store.KindProviderReturn {
		t.Fatalf("no return notice last: %+v", ks)
	}
	return ks[len(ks)-1]
}

// The home's Until passed: the next message runs on the home, the idle
// stand-in process is stopped, and a return notice is written.
func TestFailoverReturnAfterUntil(t *testing.T) {
	p, layout, sp := returnEnv(t, "claude/b", map[string][][]string{
		"a": {foOKLines("on a")},
		"b": {foOKLines("on b")},
	}, nil)
	var replies []string
	p.cfg.OnFailoverReply = func(_, _ string, text string) { replies = append(replies, text) }
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 5*time.Second)
	waitLifecycle(t, p, state.LifecycleIdle)
	if a := agentNow(t, layout); a.Provider != "claude/b" {
		t.Fatalf("returned while the home was limited: %q", a.Provider)
	}
	p.mu.Lock()
	old := p.active[sessionKey("S", "default")].agent
	p.mu.Unlock()

	limitLifted()
	sendMsg(t, p, "m2")
	waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
	if got := sp.sent("a")[0]; got != "m2" {
		t.Fatalf("a got %q", got)
	}
	if n := len(sp.sent("b")); n != 1 {
		t.Fatalf("b was sent %d messages, want 1", n)
	}
	a := agentNow(t, layout)
	if a.Provider != "claude/a" || a.Failover == nil || a.Failover.HomeProvider != "claude/a" {
		t.Fatalf("agent = %+v", a)
	}
	if !old.ExitReported() {
		t.Fatal("the stand-in process still runs")
	}
	if r := lastReturn(t, layout); !strings.HasPrefix(r.Events[0].Text, "Back to home claude/a") {
		t.Fatalf("notice = %q", r.Events[0].Text)
	}
	if len(replies) != 1 || !strings.HasPrefix(replies[0], "Back to home claude/a") {
		t.Fatalf("replies = %q", replies)
	}
	if got := userTurns(t, layout); len(got) != 2 {
		t.Fatalf("user turns = %q", got)
	}
}

// A usage reading under 100% taken after the limit clears it before its
// Until: someone reset it by hand.
func TestFailoverReturnUsageClearsEarly(t *testing.T) {
	p, layout, sp := returnEnv(t, "claude/b", map[string][][]string{
		"a": {foOKLines("on a")},
		"b": {foOKLines("never")},
	}, nil)
	provider.UsageDiskStore(provider.UsageDisk{Key: keyA, ObservedAt: time.Now(), Windows: []provider.UsageDiskWindow{{Key: "five_hour", Utilization: 30}}})
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
	if spawnCount(sp, "b") != 0 {
		t.Fatal("the stand-in was spawned")
	}
	if a := agentNow(t, layout); a.Provider != "claude/a" {
		t.Fatalf("provider = %q", a.Provider)
	}
}

// No fresh usage file for a limited home: a refresh is asked for and the
// message stays on the stand-in without waiting for it.
func TestFailoverReturnStaleUsageRefreshes(t *testing.T) {
	p, layout, sp := returnEnv(t, "claude/b", map[string][][]string{
		"b": {foOKLines("on b")},
	}, nil)
	asked := make(chan string, 4)
	provider.SetUsageRefreshHook(func(key string) { asked <- key })
	t.Cleanup(func() { provider.SetUsageRefreshHook(nil) })
	sendMsg(t, p, "m1")
	select {
	case k := <-asked:
		if k != keyA {
			t.Fatalf("refresh asked for %q", k)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no refresh asked for")
	}
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 5*time.Second)
	if a := agentNow(t, layout); a.Provider != "claude/b" {
		t.Fatalf("provider = %q", a.Provider)
	}
}

// The home is still limited but a target above the current one is not:
// the session moves up to it, the home stays.
func TestFailoverReturnUpTheChain(t *testing.T) {
	p, layout, sp := returnEnv(t, "claude/c", map[string][][]string{
		"b": {foOKLines("on b")},
	}, nil)
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 5*time.Second)
	a := agentNow(t, layout)
	if a.Provider != "claude/b" || a.Failover == nil || a.Failover.HomeProvider != "claude/a" {
		t.Fatalf("agent = %+v", a)
	}
	if r := lastReturn(t, layout); !strings.HasPrefix(r.Events[0].Text, "Moved back up the failover chain of claude/a to claude/b") {
		t.Fatalf("notice = %q", r.Events[0].Text)
	}
	if spawnCount(sp, "c") != 0 || spawnCount(sp, "a") != 0 {
		t.Fatal("spawned off the picked target")
	}
}

// Return home off: the session stays on the stand-in even though the
// home is healthy.
func TestFailoverReturnOff(t *testing.T) {
	cfg := provider.FailoverConfig{Enabled: true, Triggers: provider.FailoverTriggers, Chain: []provider.FailoverTarget{{Instance: "claude/b"}}}
	p, layout, sp := returnEnv(t, "claude/b", map[string][][]string{
		"b": {foOKLines("on b")},
	}, &cfg)
	provider.ResetHealth(keyA)
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 5*time.Second)
	if a := agentNow(t, layout); a.Provider != "claude/b" {
		t.Fatalf("provider = %q", a.Provider)
	}
	if spawnCount(sp, "a") != 0 {
		t.Fatal("the home was spawned")
	}
}

// The home's failover was switched off while the session was away: it
// still goes back to the home (ReturnHome on), but never up the chain to
// another target.
func TestFailoverReturnFailoverOffHomeOnly(t *testing.T) {
	cfg := provider.FailoverConfig{ReturnHome: true, Triggers: provider.FailoverTriggers,
		Chain: []provider.FailoverTarget{{Instance: "claude/b"}, {Instance: "claude/c"}}}
	p, layout, sp := returnEnv(t, "claude/c", map[string][][]string{
		"a": {foOKLines("on a")},
		"c": {foOKLines("on c")},
	}, &cfg)
	// Home limited, b healthy: no move to b.
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("c")) == 1 }, 5*time.Second)
	waitLifecycle(t, p, state.LifecycleIdle)
	if a := agentNow(t, layout); a.Provider != "claude/c" || spawnCount(sp, "b") != 0 {
		t.Fatalf("moved up with failover off: %q", a.Provider)
	}
	provider.ResetHealth(keyA)
	sendMsg(t, p, "m2")
	waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
	if a := agentNow(t, layout); a.Provider != "claude/a" {
		t.Fatalf("provider = %q, want the home", a.Provider)
	}
}

// The current stand-in went bad and a target below it is healthy: a turn
// boundary never moves the session down the chain.
func TestFailoverReturnNeverDown(t *testing.T) {
	p, layout, sp := returnEnv(t, "claude/b", map[string][][]string{
		"b": {foOKLines("on b")},
	}, nil)
	provider.MarkUnhealthy(provider.HealthKey(provider.Instance{Type: provider.TypeClaude, Name: "b"}),
		provider.Health{Kind: event.ErrLimit, Until: time.Now().Add(time.Hour)})
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 5*time.Second)
	if a := agentNow(t, layout); a.Provider != "claude/b" || spawnCount(sp, "c") != 0 {
		t.Fatalf("moved down to %q", a.Provider)
	}
	if ks := turnKinds(t, layout); len(ks) != 0 {
		t.Fatalf("notices = %+v", ks)
	}
}

// A message reaching the stand-in right after the return was decided
// (a sub-agent result, here) keeps its turn: the return is called off,
// the message is answered, and the next message after it goes home.
func TestFailoverReturnRaceNonUserMessage(t *testing.T) {
	p, layout, sp := returnEnv(t, "claude/b", map[string][][]string{
		"a": {foOKLines("on a")},
		"b": {
			foOKLines("on b"),
			// The result's turn is still running when m2 arrives.
			{`{"type":"assistant","message":{"content":[{"type":"text","text":"reading"}]}}`},
			foOKLines("m2 on b"),
		},
	}, nil)
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 5*time.Second)
	waitLifecycle(t, p, state.LifecycleIdle)
	provider.ResetHealth(keyA)
	once := false
	p.foStage = func(stage string) {
		if stage == "return-picked" && !once {
			once = true
			if err := p.Send(context.Background(), "S", "default", "agent", "system", "sub-agent result"); err != nil {
				t.Errorf("system send: %v", err)
			}
		}
	}
	sendMsg(t, p, "m2")
	waitFor(t, func() bool { return len(sp.sent("b")) == 3 }, 5*time.Second)
	if got := sp.sent("b"); got[1] != "sub-agent result" || got[2] != "m2" {
		t.Fatalf("b got %q", got)
	}
	if a := agentNow(t, layout); a.Provider != "claude/b" || spawnCount(sp, "a") != 0 {
		t.Fatalf("returned over a running turn: %q", a.Provider)
	}
	// The lifecycle may still read Idle from m1's turn before "reading"
	// is parsed: wait for the answer that closes the turn instead.
	waitFor(t, func() bool { return assistantSaid(t, layout, "m2 on b") }, 5*time.Second)
	waitLifecycle(t, p, state.LifecycleIdle)
	sendMsg(t, p, "m3")
	waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
	if got := sp.sent("a")[0]; got != "m3" {
		t.Fatalf("a got %q", got)
	}
}

// Two messages at once. Either the return wins (one home process gets
// both) or one message reaches the stand-in first and the return is
// called off (the stand-in gets both). Never: a lost or doubled message,
// a second home process, an orphaned entry.
func TestFailoverReturnConcurrentUserSends(t *testing.T) {
	p, layout, sp := returnEnv(t, "claude/b", map[string][][]string{
		"a": {foOKLines("one"), foOKLines("two")},
		"b": {foOKLines("on b"), foOKLines("b one"), foOKLines("b two")},
	}, nil)
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 5*time.Second)
	waitLifecycle(t, p, state.LifecycleIdle)
	provider.ResetHealth(keyA)
	var wg sync.WaitGroup
	for _, m := range []string{"m2", "m3"} {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			if err := p.Send(context.Background(), "S", "default", "ui", "user", m); err != nil {
				t.Errorf("send %s: %v", m, err)
			}
		}(m)
	}
	wg.Wait()
	count := func(m string) int {
		all := strings.Join(append(sp.sent("a"), sp.sent("b")[1:]...), "\n")
		return strings.Count(all, m)
	}
	waitFor(t, func() bool { return count("m2") >= 1 && count("m3") >= 1 }, 5*time.Second)
	time.Sleep(200 * time.Millisecond)
	if count("m2") != 1 || count("m3") != 1 {
		t.Fatalf("a got %q, b got %q: a message lost or doubled", sp.sent("a"), sp.sent("b"))
	}
	if n := spawnCount(sp, "a"); n > 1 {
		t.Fatalf("home spawned %d times", n)
	}
	want := "claude/b"
	if spawnCount(sp, "a") == 1 {
		want = "claude/a"
		if len(sp.sent("b")) != 1 {
			t.Fatalf("returned, yet b got %q", sp.sent("b"))
		}
	}
	if a := agentNow(t, layout); a.Provider != want {
		t.Fatalf("provider = %q, want %q", a.Provider, want)
	}
	p.mu.Lock()
	e, ok := p.active[sessionKey("S", "default")]
	p.mu.Unlock()
	if !ok || e.agent.ExitReported() {
		t.Fatal("no live process in the pool")
	}
	returns := 0
	for _, tr := range turnKinds(t, layout) {
		if tr.Kind == store.KindProviderReturn {
			returns++
		}
	}
	if (want == "claude/a") != (returns == 1) || returns > 1 {
		t.Fatalf("%d return notices on %s", returns, want)
	}
}

// A message sent mid-turn goes to the running stand-in; the return waits
// for the next message after the turn ends.
func TestFailoverReturnWaitsForTheTurn(t *testing.T) {
	p, layout, sp := returnEnv(t, "claude/b", map[string][][]string{
		"a": {foOKLines("on a")},
		"b": {
			{`{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}`},
			foOKLines("done on b"),
		},
	}, nil)
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 5*time.Second)
	waitLifecycle(t, p, state.LifecycleWorking)
	provider.ResetHealth(keyA)
	sendMsg(t, p, "m2")
	waitFor(t, func() bool { return len(sp.sent("b")) == 2 }, 5*time.Second)
	if spawnCount(sp, "a") != 0 {
		t.Fatal("the running turn was cut short")
	}
	if a := agentNow(t, layout); a.Provider != "claude/b" {
		t.Fatalf("provider = %q mid-turn", a.Provider)
	}
	waitLifecycle(t, p, state.LifecycleIdle)
	sendMsg(t, p, "m3")
	waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
	if got := sp.sent("a")[0]; got != "m3" {
		t.Fatalf("a got %q", got)
	}
}

// A person switching between the pick and the return switch wins; the
// idle stand-in still stops and the message goes to their pick.
func TestFailoverReturnManualSwitchWins(t *testing.T) {
	p, layout, sp := returnEnv(t, "claude/b", map[string][][]string{
		"b": {foOKLines("on b")},
		"c": {foOKLines("on c")},
	}, nil)
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 5*time.Second)
	waitLifecycle(t, p, state.LifecycleIdle)
	provider.ResetHealth(keyA)
	p.foStage = func(stage string) {
		if stage == "return-picked" {
			if err := provider.Switch(layout, p, "S", "default", "claude/c", provider.SwitchOptions{}); err != nil {
				t.Errorf("manual switch: %v", err)
			}
		}
	}
	sendMsg(t, p, "m2")
	waitFor(t, func() bool { return len(sp.sent("c")) == 1 }, 5*time.Second)
	if got := sp.sent("c")[0]; got != "m2" {
		t.Fatalf("c got %q", got)
	}
	a := agentNow(t, layout)
	if a.Provider != "claude/c" || a.Failover == nil || a.Failover.HomeProvider != "claude/c" {
		t.Fatalf("agent = %+v, want the manual pick as provider and home", a)
	}
	if spawnCount(sp, "a") != 0 || len(sp.sent("b")) != 1 {
		t.Fatal("the return went through after all")
	}
}

// The home fails again right after the return: the turn fails over as
// usual, without waiting.
func TestFailoverReturnHomeFailsAgain(t *testing.T) {
	p, layout, sp := returnEnv(t, "claude/b", map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foOKLines("on b again")},
	}, nil)
	provider.ResetHealth(keyA)
	sendMsg(t, p, "m1")
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 10*time.Second)
	if got := sp.sent("a"); len(got) != 1 || got[0] != "m1" {
		t.Fatalf("a got %q", got)
	}
	if got := sp.sent("b")[0]; got != "m1" {
		t.Fatalf("b got %q, want the original message", got)
	}
	a := agentNow(t, layout)
	if a.Provider != "claude/b" || a.Failover == nil || a.Failover.HomeProvider != "claude/a" {
		t.Fatalf("agent = %+v", a)
	}
	kinds := []string{}
	for _, tr := range turnKinds(t, layout) {
		kinds = append(kinds, tr.Kind)
	}
	if len(kinds) != 2 || kinds[0] != store.KindProviderReturn || kinds[1] != store.KindProviderFailover {
		t.Fatalf("kinds = %v", kinds)
	}
}

// No failover state, or the session already home: nothing to do.
func TestFailoverReturnNothingToReturnFrom(t *testing.T) {
	p, layout, _, _ := failoverEnv(t, true, nil, false)
	if p.maybeReturnHome("S", "default") {
		t.Fatal("returned without a failover state")
	}
	if err := session.SetFailoverState(layout, "S", "default", session.FailoverState{HomeProvider: "claude/a"}); err != nil {
		t.Fatal(err)
	}
	if p.maybeReturnHome("S", "default") {
		t.Fatal("returned while home")
	}
	if ks := turnKinds(t, layout); len(ks) != 0 {
		t.Fatalf("notices = %+v", ks)
	}
}

// assistantSaid reports whether an assistant turn containing text was
// recorded.
func assistantSaid(t *testing.T, layout config.Layout, text string) bool {
	t.Helper()
	found := false
	_ = storage.ReadJSONL(layout.SessionConversation("S"), func(line []byte) bool {
		var tr store.ConversationTurn
		if json.Unmarshal(line, &tr) == nil && tr.Role == "assistant" && strings.Contains(tr.Text, text) {
			found = true
		}
		return true
	})
	return found
}

// spawnOrQueue during a move spawns nothing: the message waits in the
// buffer and goes out when the move ends, on the instance it moved to.
func TestFailoverReturnSpawnWaitsForTheMove(t *testing.T) {
	p, _, sp, _ := failoverEnv(t, true, map[string][][]string{
		"a": {foOKLines("on a")},
	}, false)
	key := sessionKey("S", "default")
	if !p.setFailingOver(key, nil) {
		t.Fatal("setFailingOver refused")
	}
	buf, err := p.bufferFor("S")
	if err != nil {
		t.Fatal(err)
	}
	if err := buf.Append("held"); err != nil {
		t.Fatal(err)
	}
	if err := p.spawnOrQueue(context.Background(), "S", "default", "ui"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := spawnCount(sp, "a"); n != 0 {
		t.Fatalf("spawned %d times while moving", n)
	}
	p.endFailingOver(context.Background(), "S", "default", true)
	waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
	if got := sp.sent("a")[0]; got != "held" {
		t.Fatalf("a got %q", got)
	}
}
