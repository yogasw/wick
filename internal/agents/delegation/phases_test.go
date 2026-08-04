package delegation

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

/* ── Phase 6: token budget ──────────────────────────────────────────── */

func TestParseUsageAcrossProviderShapes(t *testing.T) {
	tests := []struct {
		name         string
		raw          string
		wantIn, want int
	}{
		{"anthropic style", `{"usage":{"input_tokens":120,"output_tokens":45}}`, 120, 45},
		{"openai style", `{"usage":{"prompt_tokens":300,"completion_tokens":90}}`, 300, 90},
		{"camelCase", `{"usage":{"inputTokens":7,"outputTokens":3}}`, 7, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseUsage(tc.raw)
			if got.InputTokens != tc.wantIn || got.OutputTokens != tc.want {
				t.Fatalf("got in=%d out=%d, want in=%d out=%d",
					got.InputTokens, got.OutputTokens, tc.wantIn, tc.want)
			}
		})
	}
}

// A provider that reports nothing must read as "unknown", never as a
// zero-cost run that silently passes every budget check.
func TestParseUsageEmptyWhenProviderSaysNothing(t *testing.T) {
	for _, raw := range []string{"", `{"type":"text_delta","text":"hi"}`, "not json"} {
		if u := ParseUsage(raw); !u.Empty() {
			t.Fatalf("raw %q parsed as %+v, want empty", raw, u)
		}
	}
}

func TestEffectiveMaxTokensPrecedenceAndClamp(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxTokensPerDelegation = 1000
	p := &entity.AgentProfile{DefaultMaxTokens: 500}

	tests := []struct {
		name      string
		requested int
		profile   *entity.AgentProfile
		want      int
	}{
		{"caller wins", 200, p, 200},
		{"profile default", 0, p, 500},
		{"global ceiling", 0, &entity.AgentProfile{}, 1000},
		{"clamped, not rejected", 99999, p, 1000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveMaxTokens(tc.requested, tc.profile, lim); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRunStopsOnTokenCap(t *testing.T) {
	// Turns that report usage but stream no text, so the run cannot end
	// early on "first complete answer" and must be stopped by spend.
	usage := `{"usage":{"input_tokens":400,"output_tokens":200}}`
	stream := &scriptedStream{hold: true, events: []StreamEvent{
		{Type: event.Done, Raw: usage},
		{Type: event.Done, Raw: usage},
		{Type: event.Done, Raw: usage},
	}}
	runner := &fakeRunner{partial: "partial work"}
	s, r, _ := runService(t, stream, runner)

	req := baseReq()
	req.MaxTokens = 1000 // two turns of 600 crosses it

	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationStoppedBudget {
		t.Fatalf("status = %q, want %q", res.Status, entity.DelegationStoppedBudget)
	}
	if res.TokensUsed < 1000 {
		t.Fatalf("tokens = %d, want at least the cap", res.TokensUsed)
	}
	if k := runner.killed(); len(k) != 1 {
		t.Fatalf("kills = %v, want one — the cap must stop the process", k)
	}
	// Spend must be durable, not just reported.
	got, _ := r.Get(context.Background(), res.DelegationID)
	if got.TokensUsed < 1000 {
		t.Fatalf("persisted tokens = %d", got.TokensUsed)
	}
}

// A provider that reports no usage must still run: turn limits remain
// the universal brake, and an unreported spend must not be treated as
// having blown the budget.
func TestRunWithNoUsageReportingStillCompletes(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "answer"},
		{Type: event.Done},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})
	req := baseReq()
	req.MaxTokens = 100

	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationDone {
		t.Fatalf("status = %q, want done", res.Status)
	}
}

func TestRootTokenBudgetRefusesNewDelegations(t *testing.T) {
	r := testRepo(t)
	p := seedProfile(t, r, "researcher")
	lim := DefaultLimits()
	lim.RootTokenBudget = 1000

	d := seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)
	d.TokensUsed = 1200
	if err := r.SaveDelegationForTest(context.Background(), d); err != nil {
		t.Fatalf("seed tokens: %v", err)
	}

	err := r.Admit(context.Background(), lim, p, "root-1", 1, nil)
	ref := asRefusal(t, err)
	if ref.Reason != RefusedTokenBudget {
		t.Fatalf("reason = %q, want %q", ref.Reason, RefusedTokenBudget)
	}
	if ref.Status() != entity.DelegationStoppedBudget {
		t.Fatalf("status = %q", ref.Status())
	}
}

func TestFormatCostUSDNeverReadsAsFree(t *testing.T) {
	if got := FormatCostUSD(0.0001); got != "<$0.01" {
		t.Fatalf("got %q; a tiny non-zero cost must not render as $0.00", got)
	}
	if got := FormatCostUSD(0); got != "" {
		t.Fatalf("got %q, want empty for zero", got)
	}
}

/* ── Phase 2: async fire-and-forget ─────────────────────────────────── */

type recordingDeliverer struct {
	mu      sync.Mutex
	channel []string
	session []string
}

func (d *recordingDeliverer) DeliverToChannel(_ context.Context, _, text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.channel = append(d.channel, text)
	return nil
}
func (d *recordingDeliverer) DeliverToSession(_ context.Context, _, _, text string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.session = append(d.session, text)
	return nil
}

func (d *recordingDeliverer) channelDeliveries() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.channel...)
}

func (d *recordingDeliverer) sessionDeliveries() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.session...)
}

func TestAsyncReturnsHandleImmediatelyAndDelivers(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "the async answer"},
		{Type: event.Done},
	}}
	s, r, _ := runService(t, stream, &fakeRunner{})
	del := &recordingDeliverer{}
	s.Deliver = del

	req := baseReq()
	req.Mode = ModeAsync

	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// The handle must NOT carry a result — the leader has to be able to
	// tell "not here yet" from "the sub-agent had nothing to say".
	if res.Result != "" {
		t.Fatalf("async handle carried a result: %q", res.Result)
	}
	if res.Mode != ModeBackground || res.DelegationID == "" {
		t.Fatalf("bad handle: %+v", res)
	}
	if res.Note == "" {
		t.Fatal("async handle must tell the leader not to wait on it")
	}

	waitForStatus(t, r, res.DelegationID, entity.DelegationDone)
	// No sink asked for: the leader that dispatched it gets woken, rather
	// than the result being posted next to the conversation with nobody to
	// act on it.
	waitForDeliveries(t, del.sessionDeliveries, 1)
	if got := del.channelDeliveries(); len(got) != 0 {
		t.Fatalf("channel deliveries = %v, want none by default", got)
	}
}

// The channel sink still works when it is asked for by name: a role whose
// output is written for a HUMAN reading the thread does not need the
// leader woken to relay it.
func TestBackgroundDeliversToChannelWhenAsked(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "for the thread"},
		{Type: event.Done},
	}}
	s, r, _ := runService(t, stream, &fakeRunner{})
	del := &recordingDeliverer{}
	s.Deliver = del

	req := baseReq()
	req.Mode = ModeAsync
	req.DeliverySink = SinkChannel

	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	waitForStatus(t, r, res.DelegationID, entity.DelegationDone)
	waitForDeliveries(t, del.channelDeliveries, 1)
}

// An async delegation must survive the request that started it: the
// caller's context is cancelled the moment the tool call returns.
func TestAsyncSurvivesCallerContextCancellation(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "still finished"},
		{Type: event.Done},
	}}
	s, r, _ := runService(t, stream, &fakeRunner{})
	s.Deliver = &recordingDeliverer{}

	ctx, cancel := context.WithCancel(context.Background())
	req := baseReq()
	req.Mode = ModeAsync

	res, err := s.Run(ctx, req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	cancel() // the handler returns and its context dies

	waitForStatus(t, r, res.DelegationID, entity.DelegationDone)
	got, _ := r.Get(context.Background(), res.DelegationID)
	if got.Result != "still finished" {
		t.Fatalf("result = %q; async work must not die with the caller", got.Result)
	}
}

func TestAsyncDelegationIsDetached(t *testing.T) {
	stream := &scriptedStream{hold: true}
	s, r, _ := runService(t, stream, &fakeRunner{})
	s.Deliver = &recordingDeliverer{}

	req := baseReq()
	req.Mode = ModeAsync
	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := r.Get(context.Background(), res.DelegationID)
	if !got.Detached {
		t.Fatal("async delegation must be marked detached")
	}
}

// Killing the leader must not sweep up work that was deliberately fired
// to outlive it; an explicit Stop all still stops everything.
func TestCascadeSparesDetachedButStopAllDoesNot(t *testing.T) {
	s, r, _ := newService(t)
	mk := func(id string, detached bool) {
		d := &entity.AgentDelegation{
			ID: id, RootID: id, ParentSessionID: "leader",
			ChildSessionID: "sub-" + id, ChildAgent: "claude",
			Status: entity.DelegationRunning, TriggeredBy: "user-1",
			Detached: detached,
		}
		if err := r.Create(context.Background(), d); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mk("sync-1", false)
	mk("async-1", true)

	n, err := s.CascadeInterrupt(context.Background(), "leader", "user-1", false)
	if err != nil {
		t.Fatalf("cascade: %v", err)
	}
	if n != 1 {
		t.Fatalf("cascade stopped %d, want 1 (the detached one must survive)", n)
	}
	if got, _ := r.Get(context.Background(), "async-1"); got.Status != entity.DelegationRunning {
		t.Fatalf("detached delegation was swept up by cascade: %q", got.Status)
	}

	if _, err := s.InterruptAll(context.Background(), "leader", "user-1", false); err != nil {
		t.Fatalf("stop all: %v", err)
	}
	if got, _ := r.Get(context.Background(), "async-1"); got.Status == entity.DelegationRunning {
		t.Fatal("explicit Stop all must stop detached sub-agents too")
	}
}

func TestNormalizeModeAndSinkDefaults(t *testing.T) {
	if got := NormalizeMode("", ModeAsync); got != ModeAsync {
		t.Fatalf("profile default ignored: %q", got)
	}
	if got := NormalizeMode(ModeSync, ModeAsync); got != ModeSync {
		t.Fatalf("explicit request must win: %q", got)
	}
	if !ValidSink("") || !ValidSink(SinkChannel) || ValidSink("carrier-pigeon") {
		t.Fatal("sink validation is wrong")
	}
}

/* ── Phase 4: async collect ─────────────────────────────────────────── */

func TestCollectHandsResultOverExactlyOnce(t *testing.T) {
	s, r, _ := newService(t)
	d := &entity.AgentDelegation{
		ID: "a1", RootID: "a1", ParentSessionID: "leader", ProfileKey: "researcher",
		ChildSessionID: "sub-a1", Mode: ModeAsync,
		Status: entity.DelegationDone, Result: "async output", TriggeredBy: "user-1",
	}
	if err := r.Create(context.Background(), d); err != nil {
		t.Fatalf("seed: %v", err)
	}

	first, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if first.Result != "async output" || first.Pending {
		t.Fatalf("bad first collect: %+v", first)
	}

	second, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("second collect: %v", err)
	}
	// Acting on the same sub-agent answer twice duplicates whatever the
	// leader did with it, so a repeat must be labelled.
	if second.Note == "" || second.Note == first.Note {
		t.Fatalf("repeat collect not flagged: %+v", second)
	}
}

func TestCollectPendingDoesNotBlock(t *testing.T) {
	s, r, _ := newService(t)
	d := &entity.AgentDelegation{
		ID: "a1", RootID: "a1", ParentSessionID: "leader", ProfileKey: "researcher",
		Mode: ModeAsync, Status: entity.DelegationRunning, TriggeredBy: "user-1",
	}
	if err := r.Create(context.Background(), d); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := s.Collect(context.Background(), "a1", "user-1", false)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if !got.Pending || got.Result != "" {
		t.Fatalf("a running delegation must come back pending: %+v", got)
	}
}

func TestCollectForbiddenForOtherUsers(t *testing.T) {
	s, r, _ := newService(t)
	seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)
	if _, err := s.Collect(context.Background(), "d1", "someone-else", false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

/* ── Take-over ──────────────────────────────────────────────────────── */

type recordingSteerer struct{ msgs []string }

func (s *recordingSteerer) SendToChild(_ context.Context, _, _, message string) error {
	s.msgs = append(s.msgs, message)
	return nil
}

func TestTakeOverRequiresProfileOptIn(t *testing.T) {
	s, r, _ := newService(t)
	seedProfile(t, r, "researcher") // AllowTakeOver defaults to false
	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 1)
	st := &recordingSteerer{}
	s.Steerer = st

	err := s.TakeOver(context.Background(), "d1", "user-1", "try the other approach", false)
	if !errors.Is(err, ErrTakeOverNotAllowed) {
		t.Fatalf("err = %v, want ErrTakeOverNotAllowed", err)
	}
	if len(st.msgs) != 0 {
		t.Fatalf("message leaked to a role that did not opt in: %v", st.msgs)
	}
}

func TestTakeOverFlagsResultAsSteered(t *testing.T) {
	s, r, _ := newService(t)
	p := seedProfile(t, r, "researcher")
	p.AllowTakeOver = true
	if err := r.SaveProfile(context.Background(), p); err != nil {
		t.Fatalf("save: %v", err)
	}
	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 1)
	st := &recordingSteerer{}
	s.Steerer = st

	if err := s.TakeOver(context.Background(), "d1", "user-1", "focus on v4", false); err != nil {
		t.Fatalf("take over: %v", err)
	}
	if len(st.msgs) != 1 {
		t.Fatalf("messages = %v, want one", st.msgs)
	}
	got, _ := r.Get(context.Background(), "d1")
	if !got.UserSteered {
		t.Fatal("a steered delegation must be flagged so the leader is told")
	}
}

func TestTakeOverRejectedOnFinishedDelegation(t *testing.T) {
	s, r, _ := newService(t)
	p := seedProfile(t, r, "researcher")
	p.AllowTakeOver = true
	_ = r.SaveProfile(context.Background(), p)
	seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)
	s.Steerer = &recordingSteerer{}

	if err := s.TakeOver(context.Background(), "d1", "user-1", "hello", false); !errors.Is(err, ErrNotSteerable) {
		t.Fatalf("err = %v, want ErrNotSteerable", err)
	}
}

/* ── Phase 5: squads ────────────────────────────────────────────────── */

func TestSquadNarrowsRosterButNeverWidens(t *testing.T) {
	profiles := []entity.AgentProfile{
		{Key: "coder"}, {Key: "reviewer"}, {Key: "researcher"},
	}
	sq := &entity.AgentSquad{
		Key:               "release",
		MemberProfileKeys: EncodeSquadMembers([]string{"coder", "reviewer"}),
	}
	got := FilterBySquad(profiles, sq)
	var keys []string
	for _, p := range got {
		keys = append(keys, p.Key)
	}
	if !reflect.DeepEqual(keys, []string{"coder", "reviewer"}) {
		t.Fatalf("got %v, want [coder reviewer]", keys)
	}
}

func TestSquadWithNoMembersIsNotACage(t *testing.T) {
	profiles := []entity.AgentProfile{{Key: "coder"}}
	sq := &entity.AgentSquad{Key: "loose", MemberProfileKeys: "[]"}
	if got := FilterBySquad(profiles, sq); len(got) != 1 {
		t.Fatalf("an empty roster must not filter everything out: %v", got)
	}
	if !SquadAllows(sq, "anything") {
		t.Fatal("empty roster must allow any profile")
	}
}

/* ── Phase 3: workspace ─────────────────────────────────────────────── */

type fakeWorkspaces struct {
	path, note string
	err        error
	released   []string
}

func (f *fakeWorkspaces) PrepareWorktree(context.Context, string, string) (string, string, error) {
	return f.path, f.note, f.err
}
func (f *fakeWorkspaces) ReleaseWorktree(_ context.Context, path string) error {
	f.released = append(f.released, path)
	return nil
}

// A non-git project must still get its work done — in the shared
// workspace, with the downgrade reported rather than hidden.
func TestWorktreeFallsBackToSharedWithNote(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "done"}, {Type: event.Done},
	}}
	s, r, _ := runService(t, stream, &fakeRunner{})
	s.Workspaces = &fakeWorkspaces{path: "", note: "project is not a git repository; ran in the shared workspace"}

	req := baseReq()
	req.Workspace = WorkspaceWorktree

	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.WorkspaceNote == "" {
		t.Fatal("a silent downgrade is the bug: the note must reach the leader")
	}
	got, _ := r.Get(context.Background(), res.DelegationID)
	if got.Workspace != WorkspaceShared {
		t.Fatalf("workspace = %q, want shared fallback", got.Workspace)
	}
}

func TestWorktreeReleasedAfterRun(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "done"}, {Type: event.Done},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})
	ws := &fakeWorkspaces{path: "/tmp/wt-1"}
	s.Workspaces = ws

	req := baseReq()
	req.Workspace = WorkspaceWorktree
	if _, err := s.Run(context.Background(), req); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !reflect.DeepEqual(ws.released, []string{"/tmp/wt-1"}) {
		t.Fatalf("released = %v, want the worktree cleaned up", ws.released)
	}
}

/* ── Phases 7 & 8: task board + kanban ──────────────────────────────── */

func seedBoard(t *testing.T, r *Repo, key, gate string) *entity.AgentBoard {
	t.Helper()
	b := &entity.AgentBoard{
		ID: key + "-id", Key: key, Name: key,
		Columns: EncodeColumns(DefaultColumns()), GateMode: gate,
	}
	if err := r.SaveBoard(context.Background(), b); err != nil {
		t.Fatalf("seed board: %v", err)
	}
	return b
}

func seedTask(t *testing.T, r *Repo, boardID, id, profile string) *entity.AgentTask {
	t.Helper()
	task := &entity.AgentTask{
		ID: id, BoardID: boardID, Title: id, ProfileKey: profile,
		Stage: entity.StageReady, ClaimState: entity.ClaimUnclaimed,
	}
	if err := r.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return task
}

// The claim guard is the whole point of a shared board: two workers
// polling at once must produce exactly one winner.
func TestClaimIsExclusive(t *testing.T) {
	r := testRepo(t)
	b := seedBoard(t, r, "eng", entity.GateModeOff)
	seedTask(t, r, b.ID, "t1", "")

	first, err := r.ClaimTask(context.Background(), b.ID, "worker-a", "")
	if err != nil || first == nil {
		t.Fatalf("first claim: task=%v err=%v", first, err)
	}
	second, err := r.ClaimTask(context.Background(), b.ID, "worker-b", "")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second != nil {
		t.Fatalf("two workers claimed the same board: %v", second.ID)
	}
}

func TestClaimReturnsNilWhenBoardEmpty(t *testing.T) {
	r := testRepo(t)
	b := seedBoard(t, r, "eng", entity.GateModeOff)
	got, err := r.ClaimTask(context.Background(), b.ID, "worker-a", "")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil on an empty board", got)
	}
}

func TestClaimRespectsProfileTargeting(t *testing.T) {
	r := testRepo(t)
	b := seedBoard(t, r, "eng", entity.GateModeOff)
	seedTask(t, r, b.ID, "t-review", "reviewer")

	if got, _ := r.ClaimTask(context.Background(), b.ID, "w1", "coder"); got != nil {
		t.Fatalf("a coder claimed a reviewer task: %v", got.ID)
	}
	if got, _ := r.ClaimTask(context.Background(), b.ID, "w2", "reviewer"); got == nil {
		t.Fatal("the matching role could not claim its own task")
	}
}

func TestStartRequiresHoldingTheClaim(t *testing.T) {
	r := testRepo(t)
	b := seedBoard(t, r, "eng", entity.GateModeOff)
	seedTask(t, r, b.ID, "t1", "")
	if _, err := r.ClaimTask(context.Background(), b.ID, "worker-a", ""); err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := r.StartTask(context.Background(), "t1", "worker-b", ""); !errors.Is(err, ErrTaskClaimed) {
		t.Fatalf("err = %v; a worker must not start a task it does not hold", err)
	}
	if err := r.StartTask(context.Background(), "t1", "worker-a", "del-1"); err != nil {
		t.Fatalf("holder could not start: %v", err)
	}
}

// The evidence gate must hold on the MCP path, not just in the UI —
// agents call MCP directly, so a UI-only gate is no gate at all.
func TestBlockingGateRefusesCompletionWithoutEvidence(t *testing.T) {
	r := testRepo(t)
	b := seedBoard(t, r, "eng", entity.GateModeBlocking)
	seedTask(t, r, b.ID, "t1", "")
	_, _ = r.ClaimTask(context.Background(), b.ID, "w1", "")

	err := r.CompleteTask(context.Background(), b, "t1", "w1", "all good", "", false)
	if !errors.Is(err, ErrEvidenceRequired) {
		t.Fatalf("err = %v, want ErrEvidenceRequired", err)
	}
	if err := r.CompleteTask(context.Background(), b, "t1", "w1", "all good", "tests pass: 42/42", false); err != nil {
		t.Fatalf("completion with evidence: %v", err)
	}
}

func TestWarningGateAllowsButFlags(t *testing.T) {
	r := testRepo(t)
	b := seedBoard(t, r, "eng", entity.GateModeWarning)
	seedTask(t, r, b.ID, "t1", "")
	_, _ = r.ClaimTask(context.Background(), b.ID, "w1", "")

	if err := r.CompleteTask(context.Background(), b, "t1", "w1", "done", "", false); err != nil {
		t.Fatalf("warning mode must not block: %v", err)
	}
	if GateWarning(b, "") == "" {
		t.Fatal("warning mode must still flag missing evidence")
	}
	if GateWarning(b, "evidence") != "" {
		t.Fatal("no warning when evidence was supplied")
	}
}

// Columns are presentation; the state machine reads Stage. Renaming or
// reordering columns must not change behaviour.
func TestStageIsIndependentOfColumnIdentity(t *testing.T) {
	cols := []BoardColumn{
		{ID: "our-custom-lane", Title: "Shipping!", Stage: entity.StageDone, Order: 0},
	}
	if got := StageForColumn(cols, "our-custom-lane"); got != entity.StageDone {
		t.Fatalf("got %q, want the mapped stage regardless of the column id", got)
	}
	// An unknown column must fall back to backlog, never to a terminal
	// stage — a mis-mapped column must not look like finished work.
	if got := StageForColumn(cols, "typo"); got != entity.StageBacklog {
		t.Fatalf("unknown column resolved to %q, want backlog", got)
	}
}

func TestMoveToDoneAppliesTheSameGateAsCompletion(t *testing.T) {
	r := testRepo(t)
	b := seedBoard(t, r, "eng", entity.GateModeBlocking)
	seedTask(t, r, b.ID, "t1", "")

	// Dragging a card into Done must not launder work past the gate the
	// tool call has to obey.
	if err := r.MoveTask(context.Background(), b, "t1", "done", 0); !errors.Is(err, ErrEvidenceRequired) {
		t.Fatalf("err = %v, want the gate to apply to the drag path too", err)
	}
}

// A crashed worker's claim must not pin a task forever.
func TestStaleClaimsAreReleased(t *testing.T) {
	r := testRepo(t)
	b := seedBoard(t, r, "eng", entity.GateModeOff)
	seedTask(t, r, b.ID, "t1", "")
	if _, err := r.ClaimTask(context.Background(), b.ID, "dead-worker", ""); err != nil {
		t.Fatalf("claim: %v", err)
	}

	n, err := r.ReleaseStaleClaims(context.Background(), -time.Minute)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if n != 1 {
		t.Fatalf("released %d, want 1", n)
	}
	if got, _ := r.ClaimTask(context.Background(), b.ID, "fresh-worker", ""); got == nil {
		t.Fatal("a released task must be claimable again")
	}
}

func TestDecodeColumnsFallsBackToUsableLayout(t *testing.T) {
	for _, raw := range []string{"", "[]", "not json"} {
		if got := DecodeColumns(raw); len(got) == 0 {
			t.Fatalf("raw %q produced no columns; the board would be unusable", raw)
		}
	}
}

/* ── helpers ────────────────────────────────────────────────────────── */

func waitForStatus(t *testing.T, r *Repo, id, want string) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if got, err := r.Get(context.Background(), id); err == nil && got.Status == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, _ := r.Get(context.Background(), id)
	t.Fatalf("delegation %s never reached %q (last: %q)", id, want, got.Status)
}

// waitForDeliveries polls until fetch returns exactly want entries.
// Needed wherever a test asserts on delivery after waitForStatus: the
// async goroutine marks the row terminal INSIDE await and calls deliver
// after it returns, so "status is done" runs slightly ahead of "the
// result has been delivered" and a single read races that gap.
func waitForDeliveries(t *testing.T, fetch func() []string, want int) []string {
	t.Helper()
	var got []string
	for i := 0; i < 400; i++ {
		got = fetch()
		if len(got) == want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("deliveries = %v, want exactly %d", got, want)
	return nil
}
