package delegation

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// scriptedStream replays a fixed event sequence, standing in for a
// provider subprocess. It has NO native --max-turns flag, which is the
// point: the cap must be enforced by wick counting Done events, so it
// works the same on codex/gemini as on claude.
type scriptedStream struct {
	events []StreamEvent
	// hold, when set, keeps the channel open after the script so the
	// runner can be observed mid-run instead of falling through to EOF.
	hold bool
}

func (s *scriptedStream) SubscribeSession(string) (<-chan StreamEvent, func()) {
	ch := make(chan StreamEvent, len(s.events)+1)
	for _, e := range s.events {
		ch <- e
	}
	if !s.hold {
		close(ch)
	}
	return ch, func() {}
}

// fakeTokens is mutex-guarded because async delegations issue and revoke on
// their own background goroutines: a multi-mention fan-out touches these
// counters concurrently, and the assertions read them from the test goroutine.
type fakeTokens struct {
	mu      sync.Mutex
	issued  int
	revoked int
	last    []string
}

func (f *fakeTokens) Issue(_ string, tagIDs []string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.issued++
	f.last = tagIDs
	return "wick_sub_test", nil
}

func (f *fakeTokens) Revoke(string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked++
}

// counts snapshots the tallies under the lock, so assertions never read a
// field a still-draining background run is writing.
func (f *fakeTokens) counts() (issued, revoked int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issued, f.revoked
}

func (f *fakeTokens) lastTags() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.last)
}

type staticTags struct{ tags []string }

func (s staticTags) GetUserFilterTagIDs(context.Context, string) []string { return s.tags }

func runService(t *testing.T, stream EventStream, runner *fakeRunner) (*Service, *Repo, *fakeTokens) {
	t.Helper()
	r := testRepo(t)
	seedProfile(t, r, "researcher")
	tok := &fakeTokens{}
	return &Service{
		Repo: r, Runner: runner, Stream: stream, Tokens: tok,
		Tags: staticTags{tags: []string{"a"}}, Limits: DefaultLimits(),
	}, r, tok
}

// baseReq is the FOREGROUND request shape. Stated explicitly because the
// default is background: every test built on this one asserts on the
// child's answer arriving as the call's own result, which is exactly what
// the background mode does not do.
func baseReq() Request {
	return Request{
		ProfileKey: "researcher", Task: "find the changelog",
		ParentSessionID: "leader", TriggeredBy: "user-1",
		Mode: ModeForeground,
	}
}

func TestRunReturnsFinalTextOnDone(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "the answer"},
		{Type: event.Done},
	}}
	s, r, tok := runService(t, stream, &fakeRunner{})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationDone {
		t.Fatalf("status = %q, want %q", res.Status, entity.DelegationDone)
	}
	if res.Result != "the answer" {
		t.Fatalf("result = %q", res.Result)
	}
	if res.TurnsUsed != 1 {
		t.Fatalf("turns = %d, want 1", res.TurnsUsed)
	}

	got, _ := r.Get(context.Background(), res.DelegationID)
	if got.Status != entity.DelegationDone {
		t.Fatalf("persisted status = %q", got.Status)
	}
	// The scoped credential must not outlive the run.
	if issued, revoked := tok.counts(); issued != 1 || revoked != 1 {
		t.Fatalf("token issued=%d revoked=%d, want 1/1", issued, revoked)
	}
}

// The ⭐ guarantee: the turn cap is enforced by counting normalized Done
// events and killing, with no provider flag involved.
func TestRunEnforcesMaxTurnsWithoutProviderFlag(t *testing.T) {
	// Turns that produce no text, so the run cannot end early on "first
	// complete answer" and must be stopped by the cap itself.
	stream := &scriptedStream{hold: true, events: []StreamEvent{
		{Type: event.Done}, {Type: event.Done}, {Type: event.Done}, {Type: event.Done},
	}}
	runner := &fakeRunner{partial: "work in progress"}
	s, r, _ := runService(t, stream, runner)

	req := baseReq()
	req.MaxTurns = 2

	res, err := s.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationStoppedMaxTurns {
		t.Fatalf("status = %q, want %q", res.Status, entity.DelegationStoppedMaxTurns)
	}
	if res.TurnsUsed != 2 {
		t.Fatalf("turns = %d, want exactly the cap (2)", res.TurnsUsed)
	}
	if k := runner.killed(); len(k) != 1 {
		t.Fatalf("kills = %v, want one — the cap must stop the process", k)
	}
	// Partial work still reaches the leader.
	if res.Result != "work in progress" {
		t.Fatalf("result = %q, want captured partial", res.Result)
	}
	if res.Note == "" {
		t.Fatal("a truncated run must explain itself to the leader")
	}

	got, _ := r.Get(context.Background(), res.DelegationID)
	if got.Status != entity.DelegationStoppedMaxTurns {
		t.Fatalf("persisted status = %q", got.Status)
	}
}

// Stopping a sub-agent must read to the leader as a completed tool call
// carrying partial work — never as an error, which invites a blind retry
// immediately after a human asked it to stop.
func TestRunInterruptedReturnsPartialNotError(t *testing.T) {
	stream := &scriptedStream{hold: true}
	runner := &fakeRunner{partial: "as far as I got"}
	s, r, _ := runService(t, stream, runner)

	done := make(chan *Result, 1)
	go func() {
		res, err := s.Run(context.Background(), baseReq())
		if err != nil {
			t.Errorf("run returned an error instead of a partial result: %v", err)
			done <- nil
			return
		}
		done <- res
	}()

	// Wait for the row to exist, then stop it the way a human would.
	var id string
	for i := 0; i < 200; i++ {
		var rows []entity.AgentDelegation
		_ = r.DB().Find(&rows).Error
		if len(rows) == 1 && rows[0].Status == entity.DelegationRunning {
			id = rows[0].ID
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("delegation never reached running")
	}
	if _, err := s.Interrupt(context.Background(), id, "user-1", false); err != nil {
		t.Fatalf("interrupt: %v", err)
	}

	select {
	case res := <-done:
		if res == nil {
			t.Fatal("run failed")
		}
		if res.Status != entity.DelegationInterrupted {
			t.Fatalf("status = %q, want %q", res.Status, entity.DelegationInterrupted)
		}
		if res.Result != "as far as I got" {
			t.Fatalf("result = %q, want the partial", res.Result)
		}
		if res.Note != interruptNote {
			t.Fatalf("note = %q, want the do-not-retry instruction", res.Note)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupt did not unblock the waiting Run")
	}
}

func TestRunSurfacesErrorEventAsFailedStatus(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.Error, Text: "model overloaded"},
	}}
	s, _, _ := runService(t, stream, &fakeRunner{})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != entity.DelegationFailed {
		t.Fatalf("status = %q, want %q", res.Status, entity.DelegationFailed)
	}
}

// The child's MCP identity must carry the intersected tag set, not the
// user's full set and never an admin token.
func TestRunIssuesScopedTokenWithNarrowedTags(t *testing.T) {
	r := testRepo(t)
	p := seedProfile(t, r, "researcher")
	p.AllowedTagIDs = `["a"]`
	if err := r.SaveProfile(context.Background(), p); err != nil {
		t.Fatalf("save: %v", err)
	}
	tok := &fakeTokens{}
	s := &Service{
		Repo: r, Runner: &fakeRunner{},
		Stream: &scriptedStream{events: []StreamEvent{{Type: event.TextDelta, Text: "x"}, {Type: event.Done}}},
		Tokens: tok, Tags: staticTags{tags: []string{"a", "b", "c"}}, Limits: DefaultLimits(),
	}

	if _, err := s.Run(context.Background(), baseReq()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if last := tok.lastTags(); len(last) != 1 || last[0] != "a" {
		t.Fatalf("token tags = %v, want only the intersection [a]", last)
	}
}

func TestRunRefusedByGovernorDoesNotSpawn(t *testing.T) {
	runner := &fakeRunner{}
	s, _, _ := runService(t, &scriptedStream{}, runner)
	s.Limits.Disabled = true

	if _, err := s.Run(context.Background(), baseReq()); err == nil {
		t.Fatal("kill-switch did not refuse the delegation")
	}
	if k := runner.killed(); len(k) != 0 {
		t.Fatalf("unexpected process activity: %v", k)
	}
}

func TestRunUnknownProfileFails(t *testing.T) {
	s, _, _ := runService(t, &scriptedStream{}, &fakeRunner{})
	req := baseReq()
	req.ProfileKey = "nope"

	if _, err := s.Run(context.Background(), req); err == nil {
		t.Fatal("expected an error for an unknown profile")
	}
}

func TestRunSetsRootIDAndChildLinkage(t *testing.T) {
	stream := &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "done"}, {Type: event.Done},
	}}
	s, r, _ := runService(t, stream, &fakeRunner{})

	res, err := s.Run(context.Background(), baseReq())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got, _ := r.Get(context.Background(), res.DelegationID)
	// A depth-0 delegation roots its own tree, so budget accounting has
	// something to aggregate on.
	if got.RootID != got.ID {
		t.Fatalf("root_id = %q, want self (%q)", got.RootID, got.ID)
	}
	if got.ParentSessionID != "leader" || got.ChildSessionID == "" {
		t.Fatalf("linkage lost: parent=%q child=%q", got.ParentSessionID, got.ChildSessionID)
	}
	// The chain must include this profile so a descendant can detect a cycle.
	if keys := decodeStringSlice(got.AncestorKeys); len(keys) != 1 || keys[0] != "researcher" {
		t.Fatalf("ancestor keys = %v, want [researcher]", keys)
	}
}
