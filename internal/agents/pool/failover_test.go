package pool

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/yogasw/wick/internal/agents/event"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

const (
	limitText = "You've hit your session limit · resets 4:40pm (Asia/Jakarta)"
)

func foLimitLines(before string) []string {
	var out []string
	if before != "" {
		out = append(out, `{"type":"assistant","message":{"content":[{"type":"text","text":"`+before+`"}]}}`)
	}
	return append(out,
		`{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"`+limitText+`"}]}}`,
		`{"type":"result","subtype":"success","is_error":true,"result":"`+limitText+`"}`,
	)
}

func foOKLines(text string) []string {
	return []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"` + text + `"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"` + text + `"}`,
	}
}

// turnSpawner plays one scripted turn per message written to stdin, per
// instance name, and records what each instance was sent. Its processes
// live until killed, like claude's.
type turnSpawner struct {
	mu     sync.Mutex
	turns  map[string][][]string
	got    map[string][]string
	spawns map[string]int
	// failStart: instances whose spawn fails, as a missing binary would.
	failStart map[string]bool
}

func (s *turnSpawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	name := ""
	if opt.Instance != nil {
		name = opt.Instance.Name
	}
	s.mu.Lock()
	s.spawns[name]++
	fail := s.failStart[name]
	s.mu.Unlock()
	if fail {
		return nil, errors.New("binary not found")
	}
	pr, pw := io.Pipe()
	return &turnProc{s: s, name: name, r: pr, w: pw, done: make(chan struct{})}, nil
}

func (s *turnSpawner) sent(name string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got[name]...)
}

type turnProc struct {
	s    *turnSpawner
	name string
	r    *io.PipeReader
	w    *io.PipeWriter
	done chan struct{}
	once sync.Once
}

func (p *turnProc) Stdout() io.Reader     { return p.r }
func (p *turnProc) Stdin() io.WriteCloser { return turnStdin{p} }
func (p *turnProc) Wait() error           { <-p.done; return nil }
func (p *turnProc) Pid() int              { return 0 }
func (p *turnProc) Binary() string        { return "" }
func (p *turnProc) Argv() []string        { return nil }
func (p *turnProc) Env() []string         { return nil }
func (p *turnProc) Kill() error {
	p.once.Do(func() { _ = p.w.Close(); close(p.done) })
	return nil
}

type turnStdin struct{ p *turnProc }

func (in turnStdin) Close() error { return nil }
func (in turnStdin) Write(b []byte) (int, error) {
	var env struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	_ = json.Unmarshal(b, &env)
	s := in.p.s
	s.mu.Lock()
	s.got[in.p.name] = append(s.got[in.p.name], env.Message.Content)
	var lines []string
	if q := s.turns[in.p.name]; len(q) > 0 {
		lines, s.turns[in.p.name] = q[0], q[1:]
	}
	s.mu.Unlock()
	go func() {
		for _, l := range lines {
			if _, err := in.p.w.Write([]byte(l + "\n")); err != nil {
				return
			}
		}
	}()
	return len(b), nil
}

// failoverEnv is a pool over a temp provider registry: claude/a (home,
// chain b → c when chain is set), claude/b and claude/c.
func failoverEnv(t *testing.T, enabled bool, turns map[string][][]string, remote bool) (*Pool, config.Layout, *turnSpawner, *evRec) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	prev := provider.AppName()
	provider.SetAppName("wick-failover-" + strings.ReplaceAll(t.Name(), "/", "-"))
	restoreHealth := provider.SetHealthDirForTest(t.TempDir())
	restoreUsage := provider.SetUsageDiskDirForTest(t.TempDir())
	t.Cleanup(func() {
		provider.WaitBackgroundRescans()
		restoreHealth()
		restoreUsage()
		provider.SetAppName(prev)
	})
	for _, n := range []string{"b", "c"} {
		if err := provider.Save(provider.Instance{Type: provider.TypeClaude, Name: n}); err != nil {
			t.Fatal(err)
		}
		provider.WaitBackgroundRescans()
	}
	if err := provider.Save(provider.Instance{Type: provider.TypeClaude, Name: "a", Failover: &provider.FailoverConfig{
		Enabled:  enabled,
		Triggers: provider.FailoverTriggers,
		Chain:    []provider.FailoverTarget{{Instance: "claude/b"}, {Instance: "claude/c"}},
	}}); err != nil {
		t.Fatal(err)
	}
	provider.WaitBackgroundRescans()

	sp := &turnSpawner{turns: turns, got: map[string][]string{}, spawns: map[string]int{}}
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	factory := &ClaudeFactory{Layout: layout, Spawner: sp}
	if remote {
		factory.Spawner = nil
		factory.RemoteSpawnerLoader = func(string) (provider.Spawner, bool) { return sp, true }
	}
	p := New(PoolConfig{MaxConcurrent: 4, IdleTimeout: 5 * time.Second, Layout: layout, Factory: factory})
	rec := &evRec{}
	factory.OnEvent = func(sid, name string, ev event.AgentEvent) { rec.add(ev.Type) }
	// Mirrors the server's OnExit: no synthetic Done for a process a
	// failover stopped.
	factory.OnExit = func(sid, name string, r provider.ExitReason, d string) {
		if p.HandleExitTurnEnds(sid, name, r, d) {
			rec.add(event.Done)
		}
	}
	p.cfg.OnFailoverAbort = func(sid, name string, evs []event.AgentEvent) {
		for _, ev := range evs {
			rec.add(ev.Type)
		}
	}
	t.Cleanup(p.Stop)
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{ID: "S", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	if err := session.AddAgent(layout, "S", "default", "claude/a"); err != nil {
		t.Fatal(err)
	}
	return p, layout, sp, rec
}

func agentNow(t *testing.T, layout config.Layout) session.AgentEntry {
	t.Helper()
	s, err := session.Load(layout, "S")
	if err != nil || len(s.Agents) == 0 {
		t.Fatalf("load: %v", err)
	}
	return s.Agents[0]
}

func userTurns(t *testing.T, layout config.Layout) []string {
	t.Helper()
	var out []string
	err := storage.ReadJSONL(layout.SessionConversation("S"), func(line []byte) bool {
		var tr store.ConversationTurn
		if json.Unmarshal(line, &tr) == nil && tr.Role == "user" {
			out = append(out, tr.Text)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFailoverBeforeOutputResendsOriginal(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foOKLines("done on b")},
	}, false)
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 10*time.Second)
	if got := sp.sent("b")[0]; got != "hello" {
		t.Fatalf("b got %q, want the original message", got)
	}
	a := agentNow(t, layout)
	if a.Provider != "claude/b" || a.Failover == nil || a.Failover.HomeProvider != "claude/a" {
		t.Fatalf("agent = %+v", a)
	}
	if got := userTurns(t, layout); len(got) != 1 || got[0] != "hello" {
		t.Fatalf("user turns = %q, want one", got)
	}
	if _, bad := provider.HealthOf(provider.HealthKey(provider.Instance{Type: provider.TypeClaude, Name: "a"})); !bad {
		t.Fatal("claude/a not marked unhealthy")
	}
	// b's turn succeeded: the run is over, the home stays.
	waitFor(t, func() bool {
		f := agentNow(t, layout).Failover
		return f != nil && f.MsgKey == "" && len(f.Tried) == 0 && f.HomeProvider == "claude/a"
	}, 5*time.Second)
	if p.FailingOver("S", "default") {
		t.Fatal("still failing over")
	}
}

func TestFailoverAfterOutputSendsContinue(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("half an answer")},
		"b": {foOKLines("rest")},
	}, false)
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 10*time.Second)
	got := sp.sent("b")[0]
	if !strings.HasPrefix(got, "[wick]") || strings.Contains(got, limitText) {
		t.Fatalf("b got %q, want a continue message without the limit text", got)
	}
	if got := userTurns(t, layout); len(got) != 1 {
		t.Fatalf("user turns = %q", got)
	}
}

func TestFailoverSecondHopFollowsHomeChain(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foLimitLines("")},
		"c": {foOKLines("done on c")},
	}, false)
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sp.sent("c")) == 1 }, 15*time.Second)
	if got := sp.sent("c")[0]; got != "hello" {
		t.Fatalf("c got %q", got)
	}
	if n := len(sp.sent("a")); n != 1 {
		t.Fatalf("a was sent %d messages, want 1 (no A→B→A)", n)
	}
	if a := agentNow(t, layout); a.Provider != "claude/c" || a.Failover.HomeProvider != "claude/a" {
		t.Fatalf("agent = %+v", a)
	}
	if got := userTurns(t, layout); len(got) != 1 {
		t.Fatalf("user turns = %q", got)
	}
}

func TestFailoverOffOrRemoteKeepsTheError(t *testing.T) {
	for _, tc := range []struct {
		name            string
		enabled, remote bool
	}{{"disabled", false, false}, {"remote", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			p, layout, sp, _ := failoverEnv(t, tc.enabled, map[string][][]string{
				"a": {foLimitLines("")},
				"b": {foOKLines("never")},
			}, tc.remote)
			if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
			time.Sleep(300 * time.Millisecond)
			if n := len(sp.sent("b")); n != 0 {
				t.Fatalf("b was sent %d messages", n)
			}
			if a := agentNow(t, layout); a.Provider != "claude/a" {
				t.Fatalf("provider moved to %q", a.Provider)
			}
		})
	}
}

// evRec records the event types consumers (delegation, channels, the
// turn observer) would see, synthetic exit Dones included.
type evRec struct {
	mu  sync.Mutex
	evs []event.EventType
}

func (r *evRec) add(t event.EventType) {
	r.mu.Lock()
	r.evs = append(r.evs, t)
	r.mu.Unlock()
}

func (r *evRec) count(t event.EventType) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, e := range r.evs {
		if e == t {
			n++
		}
	}
	return n
}

// A consumer waiting on the turn (delegation, a Team turn, a Slack reply)
// sees no Error and exactly one Done: the target's.
func TestFailoverHidesTheErrorAndTheOldExit(t *testing.T) {
	p, _, sp, rec := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foOKLines("done on b")},
	}, false)
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return rec.count(event.Done) >= 1 && len(sp.sent("b")) == 1 }, 10*time.Second)
	time.Sleep(200 * time.Millisecond)
	if n := rec.count(event.Error); n != 0 {
		t.Fatalf("consumers saw %d Error events", n)
	}
	if n := rec.count(event.Done); n != 1 {
		t.Fatalf("consumers saw %d Done events, want 1", n)
	}
	if rec.count(event.Warning) != 1 {
		t.Fatal("no failover notice (Warning)")
	}
}

// A message sent while the agent fails over waits and goes out after; a
// second failure report in the meantime starts no second failover.
func TestFailoverHoldsMessagesSentMeanwhile(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, true, map[string][][]string{
		"a": {foOKLines("ok")},
	}, false)
	key := sessionKey("S", "default")
	if !p.setFailingOver(key, nil) {
		t.Fatal("setFailingOver")
	}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "two"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	sp.mu.Lock()
	spawned := sp.spawns["a"]
	sp.mu.Unlock()
	if spawned != 0 {
		t.Fatal("spawned during failover")
	}
	if v := p.decideFailover(provider.FailoverRequest{SessionID: "S", AgentName: "default", Instance: "claude/a", Kind: event.ErrLimit, Text: "two"}); v.Rerun {
		t.Fatal("second failover while one runs")
	}
	if got := userTurns(t, layout); len(got) != 1 || got[0] != "two" {
		t.Fatalf("user turns = %q", got)
	}
	p.endFailingOver(context.Background(), "S", "default", true)
	waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
	if got := sp.sent("a")[0]; got != "two" {
		t.Fatalf("a got %q", got)
	}
}

// A good turn clears a failure recorded before its message and ends the
// run; a failure recorded after the message stays.
func TestFailoverTurnOKMarksHealthy(t *testing.T) {
	p, layout, _, _ := failoverEnv(t, true, nil, false)
	key := provider.HealthKey(provider.Instance{Type: provider.TypeClaude, Name: "a"})
	sent := time.Now()
	provider.MarkUnhealthy(key, provider.Health{Kind: event.ErrLimit, Since: sent.Add(-time.Minute)})
	if err := session.SetFailoverState(layout, "S", "default", session.FailoverState{HomeProvider: "claude/a", MsgKey: "k", Tried: []string{"claude/b"}, Hops: 1}); err != nil {
		t.Fatal(err)
	}
	// No run in memory (failover idle): agents.json is not touched.
	p.turnOK("S", "default", "claude/a", sent)
	if _, bad := provider.HealthOf(key); bad {
		t.Fatal("still unhealthy")
	}
	if f := agentNow(t, layout).Failover; f == nil || f.MsgKey != "k" {
		t.Fatalf("turnOK without a run rewrote the state: %+v", f)
	}
	p.failoverRunFor(sessionKey("S", "default"))
	p.turnOK("S", "default", "claude/a", sent)
	f := agentNow(t, layout).Failover
	if f == nil || f.HomeProvider != "claude/a" || f.MsgKey != "" || f.Hops != 0 || len(f.Tried) != 0 {
		t.Fatalf("failover state = %+v", f)
	}
	provider.MarkUnhealthy(key, provider.Health{Kind: event.ErrLimit, Since: sent.Add(time.Minute)})
	p.turnOK("S", "default", "claude/a", sent)
	if _, bad := provider.HealthOf(key); !bad {
		t.Fatal("a newer failure was cleared")
	}
}

// errLines is a claude turn that fails with msg as its result.
func errLines(msg string) []string {
	return []string{`{"type":"result","subtype":"success","is_error":true,"result":"` + msg + `"}`}
}

func saveInstance(t *testing.T, ins provider.Instance) {
	t.Helper()
	if err := provider.Save(ins); err != nil {
		t.Fatal(err)
	}
	provider.WaitBackgroundRescans()
}

func homeA(triggers ...string) provider.Instance {
	return provider.Instance{Type: provider.TypeClaude, Name: "a", Failover: &provider.FailoverConfig{
		Enabled: true, Triggers: triggers,
		Chain: []provider.FailoverTarget{{Instance: "claude/b"}, {Instance: "claude/c"}},
	}}
}

func spawnCount(sp *turnSpawner, name string) int {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.spawns[name]
}

// Older agents.json entries hold a bare type ("claude"): the failover must
// still recognise the agent as being on claude/claude and move it.
func TestFailoverBareProviderKeyMoves(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, true, map[string][][]string{
		"claude": {foLimitLines("")},
		"b":      {foOKLines("done on b")},
	}, false)
	saveInstance(t, provider.Instance{Type: provider.TypeClaude, Name: "claude", Failover: &provider.FailoverConfig{
		Enabled: true, Triggers: provider.FailoverTriggers, Chain: []provider.FailoverTarget{{Instance: "claude/b"}},
	}})
	if err := session.SetAgentProvider(layout, "S", "default", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 10*time.Second)
	if a := agentNow(t, layout); a.Provider != "claude/b" {
		t.Fatalf("provider = %q, want claude/b", a.Provider)
	}
	if n := len(sp.sent("claude")); n != 1 {
		t.Fatalf("claude was sent %d messages, want 1 (no re-send to the failed account)", n)
	}
}

// Auth and unavailable errors fail over like a limit; a kind the home's
// triggers leave out keeps its error.
func TestFailoverKinds(t *testing.T) {
	for _, tc := range []struct {
		name, msg string
		triggers  []string
		moves     bool
	}{
		{"auth", "Invalid API key · Please run /login", provider.FailoverTriggers, true},
		{"unavailable", "API Error: 529 Overloaded", provider.FailoverTriggers, true},
		{"auth off", "Invalid API key · Please run /login", []string{provider.FailoverTriggerLimit}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, layout, sp, rec := failoverEnv(t, true, map[string][][]string{
				"a": {errLines(tc.msg)},
				"b": {foOKLines("done on b")},
			}, false)
			saveInstance(t, homeA(tc.triggers...))
			if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
				t.Fatal(err)
			}
			if tc.moves {
				waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 10*time.Second)
				if got := sp.sent("b")[0]; got != "hello" {
					t.Fatalf("b got %q", got)
				}
				return
			}
			waitFor(t, func() bool { return rec.count(event.Error) == 1 }, 5*time.Second)
			if n := len(sp.sent("b")); n != 0 || agentNow(t, layout).Provider != "claude/a" {
				t.Fatalf("moved anyway (b sent %d)", n)
			}
		})
	}
}

// The switch refused after the old process stopped: the original Error
// reaches the consumers, nothing is spawned on the failed account, and the
// agent is no longer failing over.
func TestFailoverSwitchFailurePublishesTheError(t *testing.T) {
	p, layout, sp, rec := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foOKLines("never")},
	}, false)
	p.foStage = func(stage string) {
		if stage == "stopped" {
			saveInstance(t, provider.Instance{Type: provider.TypeClaude, Name: "b", Disabled: true})
		}
	}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return rec.count(event.Error) == 1 && rec.count(event.Done) == 1 }, 10*time.Second)
	time.Sleep(200 * time.Millisecond)
	if p.FailingOver("S", "default") {
		t.Fatal("still failing over")
	}
	if n := spawnCount(sp, "a"); n != 1 {
		t.Fatalf("claude/a spawned %d times, want 1", n)
	}
	if n := spawnCount(sp, "b"); n != 0 {
		t.Fatalf("claude/b spawned %d times", n)
	}
	if a := agentNow(t, layout); a.Provider != "claude/a" {
		t.Fatalf("provider = %q", a.Provider)
	}
}

// A person switching while the old process goes down wins: the turn goes
// to their pick, the failover target is never spawned.
func TestFailoverManualSwitchMeanwhileWins(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foOKLines("never")},
		"c": {foOKLines("done on c")},
	}, false)
	p.foStage = func(stage string) {
		if stage == "stopped" {
			if err := provider.Switch(layout, p, "S", "default", "claude/c", provider.SwitchOptions{}); err != nil {
				t.Errorf("manual switch: %v", err)
			}
		}
	}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sp.sent("c")) == 1 }, 10*time.Second)
	if got := sp.sent("c")[0]; got != "hello" {
		t.Fatalf("c got %q", got)
	}
	time.Sleep(200 * time.Millisecond)
	if n := spawnCount(sp, "b"); n != 0 {
		t.Fatalf("failover target spawned %d times", n)
	}
	a := agentNow(t, layout)
	if a.Provider != "claude/c" || a.Failover == nil || a.Failover.HomeProvider != "claude/c" {
		t.Fatalf("agent = %+v, want the manual pick as provider and home", a)
	}
	p.mu.Lock()
	_, alive := p.active[sessionKey("S", "default")]
	p.mu.Unlock()
	if !alive {
		t.Fatal("the manual switch killed the process the turn went to")
	}
}

// Stopping the session while it fails over cancels the re-send.
func TestFailoverKillCancelsResend(t *testing.T) {
	p, _, sp, rec := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foOKLines("never")},
	}, false)
	p.foStage = func(stage string) {
		if stage == "stopped" {
			if err := p.KillAgent("S", "default"); err != nil {
				t.Errorf("kill: %v", err)
			}
		}
	}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return rec.count(event.Done) == 1 }, 10*time.Second)
	time.Sleep(200 * time.Millisecond)
	if n := spawnCount(sp, "b"); n != 0 {
		t.Fatalf("re-sent after the kill (%d spawns)", n)
	}
	if rec.count(event.Error) != 0 || p.FailingOver("S", "default") {
		t.Fatal("kill path published an Error or left the flag")
	}
}

// Two messages in one claude turn (the second typed mid-turn) both reach
// the target when the turn fails before answering.
func TestFailoverResendsEveryMessageOfTheTurn(t *testing.T) {
	p, layout, sp, _ := failoverEnv(t, true, map[string][][]string{
		"a": {nil, foLimitLines("")},
		"b": {foOKLines("done on b")},
	}, false)
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "one"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sp.sent("a")) == 1 }, 5*time.Second)
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "two"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return len(sp.sent("b")) == 1 }, 10*time.Second)
	if got := sp.sent("b")[0]; got != "one\n\ntwo" {
		t.Fatalf("b got %q, want both messages", got)
	}
	if got := userTurns(t, layout); len(got) != 2 {
		t.Fatalf("user turns = %q", got)
	}
}

// An exit of the retired process arriving after its replacement spawned
// neither releases the replacement nor publishes a turn end.
func TestFailoverLateExitIsIgnored(t *testing.T) {
	p, _, sp, rec := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foOKLines("done on b")},
	}, false)
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return rec.count(event.Done) == 1 && len(sp.sent("b")) == 1 }, 10*time.Second)
	key := sessionKey("S", "default")
	p.mu.Lock()
	target := p.active[key]
	p.mu.Unlock()
	if target == nil {
		t.Fatal("target not active")
	}
	// The retired process's exit, late.
	p.foMu.Lock()
	p.foRetired[key] = provider.New(provider.Options{})
	p.foMu.Unlock()
	if p.HandleExitTurnEnds("S", "default", provider.ExitStopped, "") {
		t.Fatal("late exit not recognised")
	}
	p.mu.Lock()
	still := p.active[key]
	p.mu.Unlock()
	if still != target {
		t.Fatal("late exit released the replacement")
	}
	if n := rec.count(event.Done); n != 1 {
		t.Fatalf("Done events = %d", n)
	}
}

// A stop landing after runFailover's last cancel check but before the
// re-send still cancels it: nothing is spawned, the turn ends with Done.
func TestFailoverKillRightBeforeResend(t *testing.T) {
	p, _, sp, rec := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foOKLines("never")},
	}, false)
	p.foStage = func(stage string) {
		if stage == "resending" {
			if err := p.KillAgent("S", "default"); err != nil {
				t.Errorf("kill: %v", err)
			}
		}
	}
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return rec.count(event.Done) == 1 }, 10*time.Second)
	time.Sleep(200 * time.Millisecond)
	if n := spawnCount(sp, "b"); n != 0 {
		t.Fatalf("re-sent after the kill (%d spawns)", n)
	}
	if rec.count(event.Error) != 0 || p.FailingOver("S", "default") {
		t.Fatal("cancel path published an Error or left the flag")
	}
}

// A retired record left behind is dropped at the replacement's good turn,
// so the replacement's own exit later still ends its turn.
func TestFailoverStaleRetiredDoesNotSwallowExit(t *testing.T) {
	p, _, sp, rec := failoverEnv(t, true, map[string][][]string{
		"a": {foLimitLines("")},
		"b": {foOKLines("done on b")},
	}, false)
	key := sessionKey("S", "default")
	if err := p.Send(context.Background(), "S", "default", "ui", "user", "hello"); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	old := p.active[key].agent
	p.mu.Unlock()
	waitFor(t, func() bool { return rec.count(event.Done) == 1 && len(sp.sent("b")) == 1 }, 10*time.Second)
	waitFor(t, old.ExitReported, 5*time.Second)
	// Its exit was consumed nowhere: the record stays behind.
	p.foMu.Lock()
	p.foRetired[key] = old
	p.foMu.Unlock()
	p.turnOK("S", "default", "claude/b", time.Now())
	p.foMu.Lock()
	_, left := p.foRetired[key]
	p.foMu.Unlock()
	if left {
		t.Fatal("turnOK kept the stale retired record")
	}
	// The replacement's own exit ends its turn as usual.
	p.foMu.Lock()
	p.foRetired[key] = old
	p.foMu.Unlock()
	p.endFailingOver(context.Background(), "S", "default", false)
	if err := p.KillAgent("S", "default"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return rec.count(event.Done) == 2 }, 5*time.Second)
}
