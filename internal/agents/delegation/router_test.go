package delegation

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/entity"
)

// routerService wires a service whose runner and stream let a delegation
// complete immediately, so a dispatch is observable as a finished row.
func routerService(t *testing.T) (*Service, *Repo) {
	t.Helper()
	s, r := serialService(t, &scriptedStream{events: []StreamEvent{
		{Type: event.TextDelta, Text: "ok"},
		{Type: event.Done},
	}})
	s.Waker = &fakeWaker{}
	return s, r
}

// seedLiveWorker builds a tree whose ROOT row exists — the hop counter
// lives on it, so a tree without one cannot carry a message.
func seedLiveWorker(t *testing.T, r *Repo) {
	t.Helper()
	ctx := context.Background()
	root := seedDelegation(t, r, "root-1", "root-1", entity.DelegationRunning, 0)
	root.Handle = entity.LeaderHandle
	root.Blocked = true // the leader is talking, not working
	if err := r.SaveDelegationForTest(ctx, root); err != nil {
		t.Fatalf("seed root: %v", err)
	}
	live := seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 0)
	live.Handle = "worker"
	if err := r.SaveDelegationForTest(ctx, live); err != nil {
		t.Fatalf("seed worker: %v", err)
	}
}

func TestRouteSpawnsARoleAndMessagesALiveAgent(t *testing.T) {
	s, r := routerService(t)
	ctx := context.Background()
	seedLiveWorker(t, r)

	got := s.Route(ctx, RouteInput{
		SessionID: "parent", FromHandle: entity.LeaderHandle, Human: true,
		TriggeredBy: "user-1",
		Text:        "@researcher find the changelog\n@worker what have you got?",
	})

	if len(got) != 2 {
		t.Fatalf("dispatches = %+v, want 2", got)
	}
	if got[0].Kind != TargetRole || got[0].DelegationID == "" {
		t.Fatalf("first = %+v, want a spawned role", got[0])
	}
	if got[1].Kind != TargetAgent {
		t.Fatalf("second = %+v, want a message to a live agent", got[1])
	}

	msgs, err := r.ListThread(ctx, "root-1", 10)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(msgs) != 1 || msgs[0].ToHandle != "worker" {
		t.Fatalf("messages = %+v, want one to @worker", msgs)
	}
	if msgs[0].Kind != entity.MessageTell {
		t.Fatalf("kind = %q, want tell — an ask would block a turn that has ended", msgs[0].Kind)
	}
}

// A fan-out that blocks is not a fan-out.
func TestMultiMentionForcesAsyncSessionSink(t *testing.T) {
	s, r := routerService(t)
	ctx := context.Background()
	seedProfile(t, r, "coder")

	got := s.Route(ctx, RouteInput{
		SessionID: "parent", FromHandle: entity.LeaderHandle, Human: true,
		TriggeredBy: "user-1",
		Text:        "@researcher read the docs\n@coder read the handler",
	})
	if len(got) != 2 {
		t.Fatalf("dispatches = %+v, want 2", got)
	}

	for _, d := range got {
		if d.DelegationID == "" {
			t.Fatalf("@%s was not dispatched: %q", d.Token, d.Err)
		}
		row, err := r.Get(ctx, d.DelegationID)
		if err != nil {
			t.Fatalf("get %s: %v", d.DelegationID, err)
		}
		if row.Mode != ModeAsync {
			t.Fatalf("%s mode = %q, want async", d.Token, row.Mode)
		}
		if row.DeliverySink != SinkSession {
			t.Fatalf("%s sink = %q, want session", d.Token, row.DeliverySink)
		}
	}
}

// One mention is not a fan-out: the role's own default decides. Here the
// role declares foreground, which is the only way to get blocking now —
// an undeclared role runs in the background like everything else.
func TestSingleMentionUsesTheRoleDefaultMode(t *testing.T) {
	s, r := routerService(t)
	ctx := context.Background()

	p := seedProfile(t, r, "researcher")
	p.DefaultMode = ModeForeground
	if err := r.SaveProfile(ctx, p); err != nil {
		t.Fatalf("set role mode: %v", err)
	}

	got := s.Route(ctx, RouteInput{
		SessionID: "parent", FromHandle: entity.LeaderHandle, Human: true,
		TriggeredBy: "user-1",
		Text:        "@researcher find the changelog",
	})
	if len(got) != 1 {
		t.Fatalf("dispatches = %+v, want 1", got)
	}
	row, err := r.Get(ctx, got[0].DelegationID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if row.Mode != ModeSync {
		t.Fatalf("mode = %q, want the profile default (foreground)", row.Mode)
	}
}

// The kill switch must restore the pre-router behaviour exactly: text
// carrying mentions is just text.
func TestRouterDisabledDispatchesNothing(t *testing.T) {
	s, _ := routerService(t)
	lim := s.Limits
	lim.MentionRouter = false
	s.Limits = lim

	got := s.Route(context.Background(), RouteInput{
		SessionID: "parent", FromHandle: entity.LeaderHandle, Human: true,
		Text: "@researcher find the changelog",
	})
	if len(got) != 0 {
		t.Fatalf("dispatches = %+v, want none", got)
	}
}

// The asymmetry that shapes the whole scanner: a missed mention costs one
// clarifying turn, a false one spends a spawn because a model wrote an
// email address.
func TestRouteIgnoresLookalikeTokens(t *testing.T) {
	s, _ := routerService(t)
	ctx := context.Background()

	corpus := []string{
		"@media (min-width: 40rem) { .a { color: red } }",
		"mail me at researcher@abc.com about it",
		"```\n@researcher find the changelog\n```",
		"the @researcher decorator is unrelated",
		"@researcher",
	}
	for _, in := range corpus {
		if got := s.Route(ctx, RouteInput{
			SessionID: "parent", FromHandle: entity.LeaderHandle, Human: true,
			TriggeredBy: "user-1", Text: in,
		}); len(got) != 0 {
			t.Fatalf("%q dispatched %+v, want nothing", in, got)
		}
	}
}

// An unknown token is silent. Reporting it would teach the model to avoid
// an @ syntax it was never misusing.
func TestRouteIsSilentOnUnknownTokens(t *testing.T) {
	s, _ := routerService(t)
	got := s.Route(context.Background(), RouteInput{
		SessionID: "parent", FromHandle: entity.LeaderHandle, Human: true,
		Text: "@nobody-at-all please help",
	})
	if len(got) != 0 {
		t.Fatalf("dispatches = %+v, want none", got)
	}
}

// The marker is what stops the leader dispatching a mention wick has
// already taken. It must name the mention WITHOUT starting anything —
// the send path runs it, and a spawn there would block the person's POST.
func TestPreRouteNoteNamesMentionsWithoutDispatching(t *testing.T) {
	s, r := routerService(t)
	ctx := context.Background()
	seedLiveWorker(t, r)

	note := s.PreRouteNote(ctx, RouteInput{
		SessionID: "parent", FromHandle: entity.LeaderHandle, Human: true,
		Text: "@researcher find the changelog\n@worker what have you got?",
	})

	if !strings.HasPrefix(note, RoutedMarker) {
		t.Fatalf("note = %q, want it to open with %q", note, RoutedMarker)
	}
	for _, want := range []string{"@researcher", "@worker"} {
		if !strings.Contains(note, want) {
			t.Fatalf("note = %q, want it to name %s", note, want)
		}
	}

	rows, err := r.ListByRoot(ctx, "root-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Only the two seeded rows: the note must not have spawned anything.
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want the 2 seeded ones — the note dispatched something", len(rows))
	}
	msgs, err := r.ListThread(ctx, "root-1", 10)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages = %+v, want none", msgs)
	}
}

// Nothing to say, nothing appended: an ordinary message must reach the
// leader exactly as the person wrote it.
func TestPreRouteNoteIsEmptyWhenNothingResolves(t *testing.T) {
	s, _ := routerService(t)
	ctx := context.Background()

	for _, in := range []string{
		"how is the deploy going?",
		"@nobody-at-all please help",
		"mail me at researcher@abc.com about it",
		"",
	} {
		if got := s.PreRouteNote(ctx, RouteInput{
			SessionID: "parent", FromHandle: entity.LeaderHandle, Human: true, Text: in,
		}); got != "" {
			t.Fatalf("%q produced %q, want no marker", in, got)
		}
	}
}

// The kill switch has to silence the marker too, or a leader told not to
// dispatch would leave the work undone.
func TestPreRouteNoteIsEmptyWhenTheRouterIsOff(t *testing.T) {
	s, _ := routerService(t)
	lim := s.Limits
	lim.MentionRouter = false
	s.Limits = lim

	if got := s.PreRouteNote(context.Background(), RouteInput{
		SessionID: "parent", FromHandle: entity.LeaderHandle, Human: true,
		Text: "@researcher find the changelog",
	}); got != "" {
		t.Fatalf("note = %q, want none while the router is off", got)
	}
}

func TestFormatDispatches(t *testing.T) {
	got := FormatDispatches([]Dispatch{
		{Token: "log-investigator", Kind: TargetRole, DelegationID: "D-7f3a"},
		{Token: "docs-investigator", Kind: TargetRole, DelegationID: "D-7f3b", Queued: true, QueuePos: 1},
		{Token: "worker", Kind: TargetAgent},
		{Token: "coder", Kind: TargetRole, Err: "This delegation tree has used its whole turn budget."},
	})
	want := "dispatched: @log-investigator (running, D-7f3a) · " +
		"@docs-investigator (queued #1, D-7f3b) · " +
		"@worker (messaged) · " +
		"@coder (refused: This delegation tree has used its whole turn budget.)"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// The author's address comes from its SESSION. A model that could name
// its own sender could impersonate a peer.
func TestRouteAgentTurnResolvesTheAuthorFromItsSession(t *testing.T) {
	s, r := routerService(t)
	ctx := context.Background()
	seedLiveWorker(t, r)

	// A second sub-agent, whose session is the one authoring the turn.
	author := seedDelegation(t, r, "d2", "root-1", entity.DelegationRunning, 0)
	author.Handle = "scribe"
	author.ChildSessionID = "sess-scribe"
	if err := r.SaveDelegationForTest(ctx, author); err != nil {
		t.Fatalf("seed author: %v", err)
	}

	s.RouteAgentTurn(ctx, "sess-scribe", "claude", "@worker what did you find?")

	msgs, err := r.ListThread(ctx, "root-1", 10)
	if err != nil {
		t.Fatalf("thread: %v", err)
	}
	var toWorker *entity.AgentMessage
	for i := range msgs {
		if msgs[i].ToHandle == "worker" {
			toWorker = &msgs[i]
		}
	}
	if toWorker == nil {
		t.Fatalf("no message to @worker in %+v", msgs)
	}
	if toWorker.FromHandle != "scribe" {
		t.Fatalf("from = %q, want scribe (resolved from the session)", toWorker.FromHandle)
	}
}

// A sub-agent has no rail panel, so the dispatch line is the only report
// it will ever get.
func TestRouteAgentTurnReportsBackToASubAgent(t *testing.T) {
	s, r := routerService(t)
	ctx := context.Background()
	seedLiveWorker(t, r)

	author := seedDelegation(t, r, "d2", "root-1", entity.DelegationRunning, 0)
	author.Handle = "scribe"
	author.ChildSessionID = "sess-scribe"
	if err := r.SaveDelegationForTest(ctx, author); err != nil {
		t.Fatalf("seed author: %v", err)
	}

	s.RouteAgentTurn(ctx, "sess-scribe", "claude", "@worker status?")

	msgs, _ := r.ListThread(ctx, "root-1", 10)
	var reported bool
	for _, m := range msgs {
		if m.ToHandle == "scribe" && strings.HasPrefix(m.Body, "dispatched:") {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("no dispatch report reached @scribe: %+v", msgs)
	}
}

// The leader is not messaged: its panel already shows every dispatch, and
// waking it to say so would spend one of the tree's turns on nothing.
func TestRouteAgentTurnDoesNotReportToTheLeader(t *testing.T) {
	s, r := routerService(t)
	ctx := context.Background()
	seedLiveWorker(t, r)

	s.RouteAgentTurn(ctx, "parent", "claude", "@worker status?")

	msgs, _ := r.ListThread(ctx, "root-1", 10)
	for _, m := range msgs {
		if strings.HasPrefix(m.Body, "dispatched:") {
			t.Fatalf("the leader was sent a dispatch report: %+v", m)
		}
	}
}

func TestFormatDispatchesIsEmptyWhenNothingHappened(t *testing.T) {
	if got := FormatDispatches(nil); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
