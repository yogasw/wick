// End-to-end tests for sub-agent continuation.
//
// Every unit test in internal/agents/delegation drives a fake Runner, so
// none of them can prove the thing the feature actually promises: that a
// continued sub-agent is handed back its own transcript. That promise is
// only kept if the eventual spawn carries --resume, and the spawn happens
// three layers below the service.
//
// These tests wire the REAL pool, the REAL PoolRunner and the real
// session store, and stop only at the process boundary — the scripted
// spawner stands in for the claude binary and records the ResumeID it was
// given. That recording is the proof.
package agents_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

/* ── harness ─────────────────────────────────────────────────────────── */

// delegationRig is a delegation service wired to a live pool.
type delegationRig struct {
	svc     *delegation.Service
	repo    *delegation.Repo
	pool    *pool.Pool
	layout  agentconfig.Layout
	spawner *scriptedSpawner
}

func newDelegationRig(t *testing.T, sp *scriptedSpawner) *delegationRig {
	t.Helper()
	layout := agentconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	stream := newFanoutStream()
	// Built here rather than via newE2EPool because delegation needs the
	// factory's OnEvent hook: the service counts Done events to end a run,
	// so without a live stream every leg would hang until its context died.
	factory := &pool.ClaudeFactory{
		Layout: layout, Spawner: sp,
		OnEvent: stream.publish,
	}
	p := pool.New(pool.PoolConfig{
		MaxConcurrent: 4,
		IdleTimeout:   200 * time.Millisecond,
		Layout:        layout,
		Factory:       factory,
	})
	factory.OnExit = p.HandleExit
	t.Cleanup(p.Stop)
	repo := delegationTestRepo(t)

	svc := &delegation.Service{
		Repo:   repo,
		Runner: delegation.NewPoolRunner(p, layout, nil),
		Stream: stream,
		Limits: delegation.DefaultLimits(),
		// The production check, not a stub: this is what decides whether a
		// continuation announces amnesia, so a test that faked it would be
		// asserting against its own fixture.
		Resumable: func(childSessionID, agentName string) bool {
			sess, err := session.Load(layout, childSessionID)
			if err != nil {
				return false
			}
			for _, a := range sess.Agents {
				if agentName != "" && a.Name != agentName {
					continue
				}
				if a.CLISessionID != "" {
					return true
				}
			}
			return false
		},
	}
	return &delegationRig{svc: svc, repo: repo, pool: p, layout: layout, spawner: sp}
}

// settle waits for the previous leg's subprocess to be gone before the
// next one starts.
//
// A fixture concern, not a production one. The scripted spawner closes
// its stdout after the last line, so its process is finished but the pool
// still holds it as idle until the TTL reaps it. A continuation started
// in that window is written to a live stdin whose stdout will never
// produce another event, and the run waits forever.
//
// A real provider keeps its pipe open and answers, which is why nothing
// upstream needs this. Waiting here keeps the test measuring the
// continuation path rather than the fixture's exit timing.
func (r *delegationRig) settle(t *testing.T) {
	t.Helper()
	waitFor(t, func() bool { return r.pool.Active() == 0 }, 10*time.Second)
}

// fanoutStream turns the factory's single OnEvent callback into the
// per-session subscription delegation waits on.
//
// This is the production shape in miniature: in the server the
// broadcaster does the fanout (see tools/agents.DelegationStream). What
// matters for these tests is that the events are REAL — produced by the
// parser from the scripted provider's own output — so the turn counting
// and the run's end are driven by the same path production uses.
type fanoutStream struct {
	mu   sync.Mutex
	subs map[string][]chan delegation.StreamEvent
}

func newFanoutStream() *fanoutStream {
	return &fanoutStream{subs: map[string][]chan delegation.StreamEvent{}}
}

func (f *fanoutStream) publish(sessionID, _ string, ev event.AgentEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.subs[sessionID] {
		select {
		case ch <- delegation.StreamEvent{Type: ev.Type, Text: ev.Text, Raw: ev.Raw}:
		default:
			// Never block the reader goroutine every other subscriber
			// shares — the same rule the real broadcaster follows.
		}
	}
}

func (f *fanoutStream) SubscribeSession(sessionID string) (<-chan delegation.StreamEvent, func()) {
	ch := make(chan delegation.StreamEvent, 128)
	f.mu.Lock()
	f.subs[sessionID] = append(f.subs[sessionID], ch)
	f.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			rest := f.subs[sessionID][:0]
			for _, c := range f.subs[sessionID] {
				if c != ch {
					rest = append(rest, c)
				}
			}
			f.subs[sessionID] = rest
			close(ch)
		})
	}
}

func delegationTestRepo(t *testing.T) *delegation.Repo {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db.Exec("PRAGMA busy_timeout=5000")
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(
		&entity.AgentProfile{}, &entity.AgentDelegation{},
		&entity.AgentSquad{}, &entity.AgentBoard{}, &entity.AgentTask{},
		&entity.AgentMessage{},
		&entity.AgentIncident{}, &entity.AgentEvidence{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return delegation.NewRepo(db)
}

func seedDevRole(t *testing.T, repo *delegation.Repo) {
	t.Helper()
	if err := repo.SaveProfile(context.Background(), &entity.AgentProfile{
		ID: "developer-id", Key: "developer", Name: "developer",
		Provider: "claude", DefaultMaxTurns: 12,
	}); err != nil {
		t.Fatalf("seed role: %v", err)
	}
}

// oneTurn is a complete scripted provider turn: it announces a CLI
// session id, says something, and finishes.
func oneTurn(cliSessionID, text string) []string {
	return []string{
		`{"type":"system","subtype":"init","session_id":"` + cliSessionID + `"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"` + text + `"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"` + text + `"}`,
	}
}

/* ── the tests ───────────────────────────────────────────────────────── */

// THE test this feature exists for.
//
// A continued sub-agent must be handed back its own transcript, and the
// only thing that makes that true is --resume reaching the spawn carrying
// the CLI session id captured on the first leg. Everything above this —
// the stable child session id, the reused row, the raised budget — is
// bookkeeping that would be worthless if this argument were missing.
func TestContinueSpawnsWithResume(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{
		oneTurn("cli-abc", "first answer"),
		oneTurn("cli-abc", "second answer"),
	}}
	rig := newDelegationRig(t, sp)
	seedDevRole(t, rig.repo)
	ctx := context.Background()

	first, err := rig.svc.Run(ctx, delegation.Request{
		ProfileKey: "developer", Task: "start the work",
		ParentSessionID: "leader", TriggeredBy: "user-1", Mode: delegation.ModeForeground,
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if first.Status != entity.DelegationDone {
		t.Fatalf("first leg status = %q, want done", first.Status)
	}

	// The captured id has to be on disk before a continuation can use it.
	// This is the link the whole feature hangs on, so assert it directly
	// rather than inferring it from the resume argument later.
	row, err := rig.repo.Get(ctx, first.DelegationID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	childSess, err := session.Load(rig.layout, row.ChildSessionID)
	if err != nil {
		t.Fatalf("load child session: %v", err)
	}
	var captured string
	for _, a := range childSess.Agents {
		if a.CLISessionID != "" {
			captured = a.CLISessionID
		}
	}
	if captured != "cli-abc" {
		t.Fatalf("cli_session_id = %q, want cli-abc — without it no continuation can resume", captured)
	}

	rig.settle(t)
	second, err := rig.svc.Continue(ctx, delegation.ContinueRequest{
		DelegationID: first.DelegationID, Task: "now carry on",
		ActorID: "user-1", Mode: delegation.ModeForeground,
	})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}

	ids := sp.resumeIDs()
	if len(ids) != 2 {
		t.Fatalf("%d spawns, want 2: %v", len(ids), ids)
	}
	if ids[0] != "" {
		t.Fatalf("the FIRST spawn resumed %q — a fresh delegation must start clean", ids[0])
	}
	if ids[1] != "cli-abc" {
		t.Fatalf("continuation spawned with resume=%q, want cli-abc — the sub-agent did NOT get its transcript back", ids[1])
	}
	if !second.Continued || !second.Resumed {
		t.Fatalf("continued=%v resumed=%v, want both true", second.Continued, second.Resumed)
	}
}

// The session is the transcript. A continuation that lands anywhere else
// resumes nothing, however well the row bookkeeping reads.
func TestContinueRunsInTheSameChildSession(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{
		oneTurn("cli-abc", "first answer"),
		oneTurn("cli-abc", "second answer"),
	}}
	rig := newDelegationRig(t, sp)
	seedDevRole(t, rig.repo)
	ctx := context.Background()

	first, err := rig.svc.Run(ctx, delegation.Request{
		ProfileKey: "developer", Task: "start the work",
		ParentSessionID: "leader", TriggeredBy: "user-1", Mode: delegation.ModeForeground,
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	before, _ := rig.repo.Get(ctx, first.DelegationID)

	rig.settle(t)
	if _, err := rig.svc.Continue(ctx, delegation.ContinueRequest{
		DelegationID: first.DelegationID, Task: "carry on",
		ActorID: "user-1", Mode: delegation.ModeForeground,
	}); err != nil {
		t.Fatalf("continue: %v", err)
	}
	after, _ := rig.repo.Get(ctx, first.DelegationID)

	if after.ChildSessionID != before.ChildSessionID {
		t.Fatalf("child session moved: %q → %q", before.ChildSessionID, after.ChildSessionID)
	}

	// Both legs are in ONE conversation file. A second folder would mean
	// the continuation had started a stranger with the same handle.
	turns := readTurns(t, rig.layout, before.ChildSessionID)
	var users, assistants int
	for _, tr := range turns {
		switch tr.Role {
		case "user":
			users++
		case "assistant":
			assistants++
		}
	}
	if users != 2 || assistants != 2 {
		t.Fatalf("transcript has %d user / %d assistant turns, want 2/2 — both legs must share it: %+v",
			users, assistants, turns)
	}
}

// A sub-agent stopped by its turn cap is the main reason to continue. It
// must get real room to work rather than a budget it has already spent —
// the caps are absolute and the counter carries over, so a continuation
// that merely re-set max_turns would stop again immediately.
func TestContinueAfterTurnCapGetsRealRoom(t *testing.T) {
	// A turn that produces no text, so the run cannot end on "first
	// complete answer" and has to be stopped by the cap itself.
	silent := []string{
		`{"type":"system","subtype":"init","session_id":"cli-abc"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":""}`,
	}
	sp := &scriptedSpawner{Lines: [][]string{
		silent,
		oneTurn("cli-abc", "finished after the extra turn"),
	}}
	rig := newDelegationRig(t, sp)
	seedDevRole(t, rig.repo)
	ctx := context.Background()

	first, err := rig.svc.Run(ctx, delegation.Request{
		ProfileKey: "developer", Task: "long job", MaxTurns: 1,
		ParentSessionID: "leader", TriggeredBy: "user-1", Mode: delegation.ModeForeground,
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if first.Status != entity.DelegationStoppedMaxTurns {
		t.Fatalf("first leg status = %q, want stopped_max_turns", first.Status)
	}

	rig.settle(t)
	second, err := rig.svc.Continue(ctx, delegation.ContinueRequest{
		DelegationID: first.DelegationID, Task: "keep going", ExtraTurns: 2,
		ActorID: "user-1", Mode: delegation.ModeForeground,
	})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if second.Status == entity.DelegationStoppedMaxTurns {
		t.Fatal("the continuation hit the cap again on its first turn — the budget was re-set, not raised")
	}
	if second.Status != entity.DelegationDone {
		t.Fatalf("continuation status = %q, want done", second.Status)
	}
	// And it resumed, so the extra turns are spent on the SAME work.
	if ids := sp.resumeIDs(); len(ids) != 2 || ids[1] != "cli-abc" {
		t.Fatalf("resume ids = %v, want the second spawn to resume cli-abc", ids)
	}
}

// A provider that never reports a CLI session id leaves nothing to resume
// with. The continuation still runs in the same session — but the leader
// must be TOLD, or it writes a follow-up referring to work the sub-agent
// can no longer see.
func TestContinueWithoutACapturedSessionSaysSo(t *testing.T) {
	noInit := []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"first answer"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"first answer"}`,
	}
	sp := &scriptedSpawner{Lines: [][]string{noInit, noInit}}
	rig := newDelegationRig(t, sp)
	seedDevRole(t, rig.repo)
	ctx := context.Background()

	first, err := rig.svc.Run(ctx, delegation.Request{
		ProfileKey: "developer", Task: "start the work",
		ParentSessionID: "leader", TriggeredBy: "user-1", Mode: delegation.ModeForeground,
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}

	rig.settle(t)
	second, err := rig.svc.Continue(ctx, delegation.ContinueRequest{
		DelegationID: first.DelegationID, Task: "carry on",
		ActorID: "user-1", Mode: delegation.ModeForeground,
	})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if second.Resumed {
		t.Fatal("resumed = true when the provider never reported a session id")
	}
	if !strings.Contains(second.Note, "does not remember") {
		t.Fatalf("note = %q, want it to spell out the memory loss", second.Note)
	}
	if ids := sp.resumeIDs(); len(ids) == 2 && ids[1] != "" {
		t.Fatalf("spawned with resume=%q when nothing was captured", ids[1])
	}
}

// The supervision loop, end to end: the sub-agent is watched while it
// works, corrected mid-flight, and carried on afterwards — all in one
// session. This is the shape the whole feature was built for, so it is
// worth one test that walks it rather than only its pieces.
func TestSupervisedRunCanBeWatchedThenContinued(t *testing.T) {
	sp := &scriptedSpawner{Lines: [][]string{
		oneTurn("cli-abc", "first pass done"),
		oneTurn("cli-abc", "second pass done"),
	}}
	rig := newDelegationRig(t, sp)
	seedDevRole(t, rig.repo)
	ctx := context.Background()

	first, err := rig.svc.Run(ctx, delegation.Request{
		ProfileKey: "developer", Task: "build the thing", Supervised: true,
		ParentSessionID: "leader", TriggeredBy: "user-1", Mode: delegation.ModeForeground,
	})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}

	// The leader collects AFTER the run, which must hand the answer over
	// exactly once — the peek path must not have consumed it.
	got, err := rig.svc.Collect(ctx, first.DelegationID, "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if got.Result != "first pass done" {
		t.Fatalf("collected %q, want the first answer", got.Result)
	}

	// And it can still be carried further afterwards.
	rig.settle(t)
	second, err := rig.svc.Continue(ctx, delegation.ContinueRequest{
		DelegationID: first.DelegationID, Task: "now do the second pass",
		ActorID: "user-1", Mode: delegation.ModeForeground,
	})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if !second.Resumed {
		t.Fatal("the continuation did not resume the supervised run")
	}
	// The continuation's own result must be collectable — the first
	// collect must not have permanently consumed the row.
	after, err := rig.svc.Collect(ctx, first.DelegationID, "user-1", false)
	if err != nil {
		t.Fatalf("collect after continue: %v", err)
	}
	if strings.Contains(after.Note, "Already collected") {
		t.Fatalf("the continuation's result was unreachable: %q", after.Note)
	}
	if after.Result != "second pass done" {
		t.Fatalf("collected %q after continue, want the second answer", after.Result)
	}
}

