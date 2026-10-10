package agents

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/yogasw/wick/internal/agents/a2aremote"
	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/remote"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/teamlink"
)

// lateWorld is a remote agent's session S-remote that @asker handed a
// task to over the team link and that timed out. Deliveries are recorded;
// no Hub exists, as after a restart.
type lateWorld struct {
	mu        sync.Mutex
	delivered map[string][]string
	// followed is what was put on the person's own task (followUpLate),
	// by the session it came from.
	followed map[string][]string
}

func newLateWorld(t *testing.T, fromSession string) *lateWorld {
	t.Helper()
	return newLateWorldOrigin(t, fromSession, "")
}

// newLateWorldOrigin is newLateWorld for a task of origin (P45).
func newLateWorldOrigin(t *testing.T, fromSession, origin string) *lateWorld {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	prevLayout, prevDeliver, prevFollow := globalLayout, deliverLate, followUpLate
	globalLayout = layout
	w := &lateWorld{delivered: map[string][]string{}, followed: map[string][]string{}}
	followUpLate = func(_ context.Context, sid, text string) bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.followed[sid] = append(w.followed[sid], text)
		return true
	}
	deliverLate = func(_ context.Context, sid, text string) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.delivered[sid] = append(w.delivered[sid], text)
		return nil
	}
	t.Cleanup(func() { globalLayout, deliverLate, followUpLate = prevLayout, prevDeliver, prevFollow })
	at := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	h := teamlink.Handoff{From: "asker", To: "remote-x", ToID: "ag-r", TaskID: "task-1", ContextID: "c1", State: a2a.TaskStateWorking, FromSession: fromSession, Origin: origin}
	turns := []store.ConversationTurn{
		handoffTurn(h, at),
		{TurnID: "u1", Timestamp: at.Add(time.Second), Role: "user", Source: sourceTeam, Text: "please check"},
		{TurnID: "e1", Timestamp: at.Add(3 * time.Minute), Role: "system", IsError: true, Text: remote.TimeoutMessage(3 * time.Minute)},
	}
	h.State = a2a.TaskStateCompleted
	turns = append(turns, handoffTurn(h, at.Add(3*time.Minute+time.Second)))
	for _, tr := range turns {
		if err := storage.AppendJSONL(layout.SessionConversation("S-remote"), "wick-conv-v1", "S-remote", tr); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func (w *lateWorld) sent(sid string) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.delivered[sid]...)
}

func lateTurns(t *testing.T) []store.ConversationTurn {
	t.Helper()
	turns, err := loadConversation(globalLayout, "S-remote")
	if err != nil {
		t.Fatal(err)
	}
	return turns
}

// "Check again" keeps the reply in place of the timeout and forwards it
// to the agent that asked — once, however often it is clicked, and with
// no Hub memory of the task.
func TestSettleLateReplyReplacesAndForwardsOnce(t *testing.T) {
	w := newLateWorld(t, "S-asker")
	out, err := settleLateReply(context.Background(), "S-remote", "The answer.", remote.NoteRechecked)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Replaced || out.ForwardedTo != "asker" {
		t.Fatalf("outcome = %+v", out)
	}
	turns := lateTurns(t)
	last := turns[len(turns)-1]
	if last.Role != "assistant" || last.Replaces != "e1" || last.Text != "The answer." || last.RemoteNote != remote.NoteRechecked {
		t.Fatalf("replacement = %+v", last)
	}
	got := w.sent("S-asker")
	if len(got) != 1 || got[0] != "Late reply from @remote-x for task task-1 (it timed out earlier):\n\nThe answer." {
		t.Fatalf("delivered = %q", got)
	}

	out, err = settleLateReply(context.Background(), "S-remote", "The answer.", remote.NoteRechecked)
	if err != nil || !out.Replaced || out.ForwardedTo != "" {
		t.Fatalf("second outcome = %+v, %v", out, err)
	}
	if n := len(lateTurns(t)); n != len(turns) {
		t.Fatalf("second check wrote a turn: %d turns, want %d", n, len(turns))
	}
	if n := len(w.sent("S-asker")); n != 1 {
		t.Fatalf("forwarded %d times, want once", n)
	}
}

// P45: the person's own task (their @mention in the asker's chat) — its
// late reply is never forwarded into that chat as a prompt.
func TestSettleLateReplyUserOriginNotForwarded(t *testing.T) {
	w := newLateWorldOrigin(t, "S-asker", teamlink.OriginUser)
	out, err := settleLateReply(context.Background(), "S-remote", "The answer.", remote.NoteRechecked)
	if err != nil || !out.Replaced || out.ForwardedTo != "" {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if got := w.sent("S-asker"); len(got) != 0 {
		t.Fatalf("forwarded into the asking chat: %q", got)
	}
	if h := lateTurns(t)[0]; h.Extras["origin"] != teamlink.OriginUser {
		t.Fatalf("handoff extras = %+v", h.Extras)
	}
	// S2: the reply goes on the person's task instead.
	if got := w.followed["S-remote"]; len(got) != 1 || got[0] != "The answer." {
		t.Fatalf("follow-up on the task = %q", got)
	}
}

// P45 B2: the person answered the remote's question, and the answer's
// turn timed out. Its handoff (written by an older process, without the
// origin) is the one nearest the timeout; the task is still the person's,
// so the late reply goes on the task, not into the asking chat.
func TestSettleLateReplyAfterUserAnswerNotForwarded(t *testing.T) {
	w := newLateWorldOrigin(t, "S-asker", teamlink.OriginUser)
	at := time.Date(2026, 1, 2, 4, 0, 0, 0, time.UTC)
	h := teamlink.Handoff{From: "asker", To: "remote-x", ToID: "ag-r", TaskID: "task-1", ContextID: "c1", State: a2a.TaskStateWorking, FromSession: "S-asker"}
	for _, tr := range []store.ConversationTurn{
		handoffTurn(h, at),
		{TurnID: "u2", Timestamp: at.Add(time.Second), Role: "user", Source: sourceTeam, Text: "Answer from the user:\n\nyes"},
		{TurnID: "e2", Timestamp: at.Add(3 * time.Minute), Role: "system", IsError: true, Text: remote.TimeoutMessage(3 * time.Minute)},
	} {
		if err := storage.AppendJSONL(globalLayout.SessionConversation("S-remote"), "wick-conv-v1", "S-remote", tr); err != nil {
			t.Fatal(err)
		}
	}
	out, err := settleLateReply(context.Background(), "S-remote", "Done.", remote.NoteRechecked)
	if err != nil || !out.Replaced || out.ForwardedTo != "" {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if got := w.sent("S-asker"); len(got) != 0 {
		t.Fatalf("forwarded into the asking chat: %q", got)
	}
	if got := w.followed["S-remote"]; len(got) != 1 || got[0] != "Done." {
		t.Fatalf("follow-up on the task = %q", got)
	}
}

// A turn a person asked forwards nowhere, but the reply still replaces.
func TestSettleLateReplyNoCallerOnlyReplaces(t *testing.T) {
	w := newLateWorld(t, "")
	out, err := settleLateReply(context.Background(), "S-remote", "The answer.", remote.NoteRechecked)
	if err != nil || !out.Replaced || out.ForwardedTo != "" {
		t.Fatalf("outcome = %+v, %v", out, err)
	}
	if len(w.delivered) != 0 {
		t.Fatalf("delivered = %v", w.delivered)
	}
}

// A message sent after the timeout leaves it alone.
func TestSettleLateReplyAfterNewMessageDoesNothing(t *testing.T) {
	w := newLateWorld(t, "S-asker")
	if err := storage.AppendJSONL(globalLayout.SessionConversation("S-remote"), "wick-conv-v1", "S-remote",
		store.ConversationTurn{TurnID: "u2", Timestamp: time.Now(), Role: "user", Text: "next"}); err != nil {
		t.Fatal(err)
	}
	out, err := settleLateReply(context.Background(), "S-remote", "The answer.", remote.NoteRechecked)
	if err != nil || out.Replaced || out.ForwardedTo != "" || len(w.delivered) != 0 {
		t.Fatalf("outcome = %+v, %v, delivered %v", out, err, w.delivered)
	}
}

// The late window's reply goes to the asker through the same path (the
// store writes the turn itself), and a later "Check again" does not send
// it twice.
func TestLateWindowForwardsThroughSamePath(t *testing.T) {
	w := newLateWorld(t, "S-asker")
	prev := remote.OnFollowUp
	t.Cleanup(func() { remote.OnFollowUp = prev })
	NewTeamLinkHub(nil, nil)
	remote.OnFollowUp("S-remote", "Late answer.", remote.NoteLate)
	if got := w.sent("S-asker"); len(got) != 1 || !strings.Contains(got[0], "Late answer.") {
		t.Fatalf("delivered = %q", got)
	}
	if n := len(lateTurns(t)); n != 4 {
		t.Fatalf("late window wrote %d turns itself, want the store to", n)
	}
	// What the session's store writes for that reply.
	if err := storage.AppendJSONL(globalLayout.SessionConversation("S-remote"), "wick-conv-v1", "S-remote",
		store.ConversationTurn{TurnID: "a1", Timestamp: time.Now(), Role: "assistant", Text: "Late answer.", RemoteNote: remote.NoteLate, Replaces: "e1"}); err != nil {
		t.Fatal(err)
	}
	out, err := settleLateReply(context.Background(), "S-remote", "Late answer.", remote.NoteRechecked)
	if err != nil || !out.Replaced || out.ForwardedTo != "" {
		t.Fatalf("recheck after late window = %+v, %v", out, err)
	}
	if n := len(w.sent("S-asker")); n != 1 {
		t.Fatalf("forwarded %d times, want once", n)
	}
}

// A remote turn that timed out reads to the asker as a notice that the
// reply will follow, not as an empty answer.
func TestCollectTurnErrSeesRemoteTimeout(t *testing.T) {
	ch := make(chan delegation.StreamEvent, 2)
	ch <- delegation.StreamEvent{Type: event.Error, Text: remote.TimeoutMessage(3 * time.Minute)}
	text, failed := collectTurnErr(context.Background(), ch, false)
	if text != "" {
		t.Fatalf("text = %q", text)
	}
	if d, ok := remote.IsTimeout(failed); !ok || d != 3*time.Minute {
		t.Fatalf("failed = %q", failed)
	}
}

func TestHandoffTurnKeepsCallerSession(t *testing.T) {
	tr := handoffTurn(teamlink.Handoff{From: "a", To: "b", FromSession: "S-a"}, time.Now())
	if tr.Extras["from_session"] != "S-a" {
		t.Fatalf("extras = %v", tr.Extras)
	}
	if _, ok := handoffTurn(teamlink.Handoff{From: "a"}, time.Now()).Extras["from_session"]; ok {
		t.Fatal("empty from_session should be left out")
	}
}

// A remote turn's error is its last event: collectTurnErr returns it
// there, while a local turn's error waits for Done.
func TestCollectTurnErrRemoteError(t *testing.T) {
	ch := make(chan delegation.StreamEvent, 3)
	ch <- delegation.StreamEvent{Type: event.TextDelta, Text: "partial"}
	ch <- delegation.StreamEvent{Type: event.Error, Text: "Remote agent: boom"}
	text, failed := collectTurnErr(context.Background(), ch, true)
	if text != "partial" || failed != "Remote agent: boom" {
		t.Fatalf("remote = %q, %q", text, failed)
	}
	ch = make(chan delegation.StreamEvent, 3)
	ch <- delegation.StreamEvent{Type: event.Error, Text: "rate limited"}
	ch <- delegation.StreamEvent{Type: event.TextDelta, Text: "recovered"}
	ch <- delegation.StreamEvent{Type: event.Done}
	if text, failed := collectTurnErr(context.Background(), ch, false); text != "recovered" || failed != "rate limited" {
		t.Fatalf("local = %q, %q", text, failed)
	}
}

// remoteTurnEnd: a remote's failed, rejected, canceled or auth_required
// task ends the team task in that state with the remote's message; a
// question stays input_required; a stale state never speaks for this
// turn; a timeout is no end.
func TestRemoteTurnEnd(t *testing.T) {
	start := time.Now()
	fresh, stale := start.Add(time.Second), start.Add(-time.Minute)
	for _, c := range []struct {
		name   string
		st     a2aremote.State
		failed string
		state  a2a.TaskState
		text   string
	}{
		{"completed", a2aremote.State{LastState: string(a2a.TaskStateCompleted), UpdatedAt: fresh}, "", "", ""},
		{"question", a2aremote.State{InputRequired: true, TaskID: "t1", UpdatedAt: fresh}, "", a2a.TaskStateInputRequired, "reply"},
		{"failed", a2aremote.State{LastState: string(a2a.TaskStateFailed), Reason: "boom", UpdatedAt: fresh}, "Remote agent: boom", a2a.TaskStateFailed, "boom"},
		{"rejected", a2aremote.State{LastState: string(a2a.TaskStateRejected), Reason: "not my job", UpdatedAt: fresh}, "Remote agent: not my job", a2a.TaskStateRejected, "not my job"},
		{"canceled", a2aremote.State{LastState: string(a2a.TaskStateCanceled), Reason: "stopped", UpdatedAt: fresh}, "Remote agent: stopped", a2a.TaskStateCanceled, "stopped"},
		{"auth", a2aremote.State{LastState: string(a2a.TaskStateAuthRequired), Reason: "sign in", UpdatedAt: fresh}, "Remote agent: sign in", a2a.TaskStateAuthRequired, "sign in"},
		{"stale question", a2aremote.State{InputRequired: true, TaskID: "t1", UpdatedAt: stale}, "", "", ""},
		{"stale question, turn failed", a2aremote.State{InputRequired: true, TaskID: "t1", UpdatedAt: stale}, "Remote agent: gone", a2a.TaskStateFailed, "Remote agent: gone"},
		{"stale failed", a2aremote.State{LastState: string(a2a.TaskStateFailed), Reason: "old", UpdatedAt: stale}, "", "", ""},
		{"transport error", a2aremote.State{LastState: string(a2a.TaskStateWorking), UpdatedAt: fresh}, "A2A call failed: refused", a2a.TaskStateFailed, "A2A call failed: refused"},
		{"slack error", a2aremote.State{}, "channel not found", a2a.TaskStateFailed, "channel not found"},
		{"timeout", a2aremote.State{LastState: string(a2a.TaskStateWorking), UpdatedAt: fresh}, remote.TimeoutMessage(time.Minute), "", ""},
	} {
		end := remoteTurnEnd(c.st, start, "reply", c.failed)
		if c.state == "" {
			if end != nil {
				t.Fatalf("%s: end = %+v, want none", c.name, end)
			}
			continue
		}
		if end == nil || end.State != c.state || end.Text != c.text {
			t.Fatalf("%s: end = %+v", c.name, end)
		}
	}
}
