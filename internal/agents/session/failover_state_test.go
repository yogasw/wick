package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFailoverStateRoundTrip(t *testing.T) {
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{ID: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := AddAgent(layout, "s1", "main", "claude/claude"); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	want := FailoverState{
		HomeProvider: "claude/claude",
		HomeModelID:  "opus",
		Tried:        []string{"claude/claude"},
		MsgKey:       "abc",
		Hops:         1,
		Since:        since,
	}
	if err := SetFailoverState(layout, "s1", "main", want); err != nil {
		t.Fatal(err)
	}
	sess, err := Load(layout, "s1")
	if err != nil {
		t.Fatal(err)
	}
	got := sess.Agents[0].Failover
	if got == nil || got.HomeProvider != want.HomeProvider || got.HomeModelID != want.HomeModelID ||
		got.MsgKey != want.MsgKey || got.Hops != 1 || !got.Since.Equal(since) ||
		len(got.Tried) != 1 || got.Tried[0] != "claude/claude" {
		t.Fatalf("failover = %+v, want %+v", got, want)
	}
	if !got.AwayFromHome() {
		t.Fatal("AwayFromHome = false for a run with a hop")
	}

	if err := ClearFailoverState(layout, "s1", "main"); err != nil {
		t.Fatal(err)
	}
	sess, _ = Load(layout, "s1")
	if sess.Agents[0].Failover != nil {
		t.Fatalf("failover after clear = %+v, want nil", sess.Agents[0].Failover)
	}
	raw, _ := os.ReadFile(layout.SessionAgents("s1"))
	if strings.Contains(string(raw), "failover") {
		t.Fatalf("cleared state still on disk: %s", raw)
	}

	// Unknown agent: no-op, no new entry.
	if err := SetFailoverState(layout, "s1", "ghost", want); err != nil {
		t.Fatal(err)
	}
	if sess, _ = Load(layout, "s1"); len(sess.Agents) != 1 {
		t.Fatalf("agents = %d, want 1", len(sess.Agents))
	}
	if err := SetFailoverState(layout, "s1", "", want); err != ErrNoAgentName {
		t.Fatalf("blank name err = %v, want ErrNoAgentName", err)
	}
}

// An agents.json written before failover existed still loads, with no state.
func TestFailoverStateLegacyAgentsJSON(t *testing.T) {
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{ID: "old"}); err != nil {
		t.Fatal(err)
	}
	legacy := `[{"name":"main","provider":"codex/codex","cli_session_id":"x1","status":"idle","created_at":"2026-01-02T03:04:05Z","model_id":"gpt-5"}]`
	if err := os.WriteFile(layout.SessionAgents("old"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	sess, err := Load(layout, "old")
	if err != nil {
		t.Fatal(err)
	}
	a := sess.Agents[0]
	if a.Provider != "codex/codex" || a.CLISessionID != "x1" || a.ModelID != "gpt-5" || a.Failover != nil {
		t.Fatalf("legacy entry = %+v", a)
	}
	var st *FailoverState
	if st.AwayFromHome() {
		t.Fatal("nil state reports away from home")
	}
}

func TestFailoverStateRunFor(t *testing.T) {
	now := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	since := now.Add(-time.Minute)
	st := &FailoverState{HomeProvider: "claude/a", HomeModelID: "opus", MsgKey: "k1", Tried: []string{"claude/a"}, Hops: 1, Since: since}

	same := st.RunFor("k1", now)
	if same.Hops != 1 || len(same.Tried) != 1 || !same.Since.Equal(since) || same.MsgKey != "k1" {
		t.Fatalf("same run = %+v", same)
	}
	same.Tried[0] = "mutated"
	if st.Tried[0] != "claude/a" {
		t.Fatal("RunFor shares Tried with the stored state")
	}

	for _, key := range []string{"k2", ""} {
		fresh := st.RunFor(key, now)
		want := FailoverState{HomeProvider: "claude/a", HomeModelID: "opus", MsgKey: key, Since: now}
		if fresh.HomeProvider != want.HomeProvider || fresh.HomeModelID != want.HomeModelID || fresh.MsgKey != key ||
			fresh.Hops != 0 || fresh.Tried != nil || !fresh.Since.Equal(now) {
			t.Fatalf("RunFor(%q) = %+v, want %+v", key, fresh, want)
		}
	}
	// An empty stored key never matches an empty request either.
	empty := &FailoverState{HomeProvider: "claude/a", Hops: 2}
	if got := empty.RunFor("", now); got.Hops != 0 {
		t.Fatalf("empty key reused a run: %+v", got)
	}
	var none *FailoverState
	if got := none.RunFor("k", now); got.HomeProvider != "" || got.MsgKey != "k" {
		t.Fatalf("nil RunFor = %+v", got)
	}

	st.EndRun()
	if st.HomeProvider != "claude/a" || st.HomeModelID != "opus" || st.MsgKey != "" || st.Tried != nil || st.Hops != 0 || !st.Since.IsZero() {
		t.Fatalf("EndRun = %+v", st)
	}
}

func TestEndFailoverRunKeepsHome(t *testing.T) {
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{ID: "e1"}); err != nil {
		t.Fatal(err)
	}
	_ = AddAgent(layout, "e1", "main", "codex/b")
	_ = SetFailoverState(layout, "e1", "main", FailoverState{HomeProvider: "claude/a", MsgKey: "k", Tried: []string{"claude/a"}, Hops: 1, Since: time.Now()})
	if err := EndFailoverRun(layout, "e1", "main"); err != nil {
		t.Fatal(err)
	}
	sess, _ := Load(layout, "e1")
	if got := sess.Agents[0].Failover; got == nil || got.HomeProvider != "claude/a" || got.Tried != nil || got.Hops != 0 || got.MsgKey != "" || !got.Since.IsZero() {
		t.Fatalf("after EndFailoverRun: %+v", got)
	}
}

// M3: repointing an entry outside a switch (workflow node, repair) makes
// the new provider the home and forgets the run, like a manual switch.
func TestSetAgentProviderResetsHome(t *testing.T) {
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{ID: "w1"}); err != nil {
		t.Fatal(err)
	}
	_ = AddAgent(layout, "w1", "main", "codex/b")
	_ = SetModelID(layout, "w1", "main", "gpt-5")
	_ = SetFailoverState(layout, "w1", "main", FailoverState{HomeProvider: "claude/a", MsgKey: "k", Tried: []string{"claude/a"}, Hops: 1})
	if err := SetAgentProvider(layout, "w1", "main", "omp/x"); err != nil {
		t.Fatal(err)
	}
	sess, _ := Load(layout, "w1")
	a := sess.Agents[0]
	if a.Provider != "omp/x" || a.Failover == nil || a.Failover.HomeProvider != "omp/x" || a.Failover.HomeModelID != "gpt-5" ||
		a.Failover.Hops != 0 || a.Failover.Tried != nil || a.Failover.MsgKey != "" {
		t.Fatalf("after SetAgentProvider: %+v / %+v", a, a.Failover)
	}
}

// M4: concurrent writers of one session's agents.json never lose each
// other's update.
func TestUpdateAgentsConcurrentWritersKeepEveryUpdate(t *testing.T) {
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{ID: "c1"}); err != nil {
		t.Fatal(err)
	}
	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := SetMaxTurns(layout, "c1", fmt.Sprintf("agent-%02d", i), i+1); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	sess, err := Load(layout, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Agents) != n {
		t.Fatalf("agents = %d, want %d (lost updates)", len(sess.Agents), n)
	}
	// A refusal inside fn writes nothing and surfaces as is.
	boom := errors.New("boom")
	if err := UpdateAgents(layout, "c1", func(a []AgentEntry) ([]AgentEntry, bool, error) { return nil, true, boom }); err != boom {
		t.Fatalf("err = %v, want boom", err)
	}
	if sess, _ = Load(layout, "c1"); len(sess.Agents) != n {
		t.Fatal("a failed update wrote")
	}
}
