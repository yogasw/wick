package teamlink

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// P45: a person's @mention in the captain's chat is the person's task.

// userAskingHub sends "deploy it" to a teammate that asks back, as the
// person's mention in sess-cap (the route routeHumanMentions takes).
func userAskingHub(t *testing.T, dir string) (*Hub, *fakeNotify, string) {
	t.Helper()
	h, _, note := newTestHub(func(_ Peer, text string) string {
		if strings.HasSuffix(text, "prod") {
			return "deployed to prod"
		}
		return InputRequiredToken + " which environment?"
	})
	if dir != "" {
		if err := h.Persist(dir); err != nil {
			t.Fatal(err)
		}
	}
	res, err := h.Send(context.Background(), SendInput{
		CallerSession: "sess-cap", CallerAgentID: "a-cap", SessionUser: "u1",
		To: "anton", Text: "deploy it", Wait: -1, Mention: true, Human: true,
	})
	if err != nil {
		t.Fatalf("send = %+v, %v", res, err)
	}
	return h, note, res.TaskID
}

// noDeliveries fails when anything was delivered into a chat as a prompt.
func noDeliveries(t *testing.T, note *fakeNotify) {
	t.Helper()
	if got, _ := note.snapshot(); len(got) != 0 {
		t.Fatalf("delivered into the chat (wakes its agent): %v", got)
	}
}

// The question of the person's task needs them at once — even while the
// captain's turn runs — and is never delivered into the captain's chat.
func TestUserTaskQuestionNeedsYouWithoutWaking(t *testing.T) {
	h, note, id := userAskingHub(t, "")
	h.CallerBusy = func(string) bool { return true }
	v := waitTaskState(t, h, "sess-cap", id, "input_required")
	if v.Origin != OriginUser || !v.NeedsYou || v.Reply != "which environment?" {
		t.Fatalf("view = %+v", v)
	}
	noDeliveries(t, note)
	var changed []TaskView
	h.OnTaskChange = func(_ string, v TaskView) { changed = append(changed, v) }
	h.RefreshNeedsYou("sess-cap")
	if len(changed) != 1 || !changed[0].NeedsYou || changed[0].Origin != OriginUser {
		t.Fatalf("refresh = %+v", changed)
	}
}

// The captain cannot answer or cancel the person's task, and get_task
// says the question is the user's.
func TestUserTaskRefusesAgent(t *testing.T) {
	h, _, id := userAskingHub(t, "")
	waitTaskState(t, h, "sess-cap", id, "input_required")
	ctx := context.Background()
	_, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: id, Text: "prod"})
	if !errors.Is(err, ErrUserTask) || !strings.Contains(err.Error(), "belongs to the user") {
		t.Fatalf("agent answer: %v", err)
	}
	if _, err := h.CancelTask(ctx, "a-cap", id); !errors.Is(err, ErrUserTask) {
		t.Fatalf("agent cancel: %v", err)
	}
	got, err := h.GetTask(ctx, "a-cap", id)
	if err != nil || got.State != "input_required" || !strings.Contains(got.Note, "only the user answers it") {
		t.Fatalf("get_task = %+v, %v", got, err)
	}
	// The refusal claimed nothing: the person still answers.
	if v := waitTaskState(t, h, "sess-cap", id, "input_required"); !v.NeedsYou {
		t.Fatalf("after the refused answer: %+v", v)
	}
}

// The person answers; the teammate's reply shows on the task, and
// nothing — not the answer, not the reply — is delivered to the captain.
func TestUserTaskAnswerFromUserNoWake(t *testing.T) {
	h, note, id := userAskingHub(t, "")
	waitTaskState(t, h, "sess-cap", id, "input_required")
	if _, err := h.AnswerFromUser(context.Background(), "sess-cap", id, "prod"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	v := waitTaskState(t, h, "sess-cap", id, "completed")
	if v.Reply != "deployed to prod" || v.Origin != OriginUser {
		t.Fatalf("view = %+v", v)
	}
	noDeliveries(t, note)
	// A follow-up joins the reply on the task, still without waking.
	h.mu.Lock()
	h.answered["sess-anton"] = a2a.TaskID(id)
	h.mu.Unlock()
	if !h.FollowUp(context.Background(), "sess-anton", "also tagged v2") {
		t.Fatal("follow-up not handled")
	}
	v = waitTaskState(t, h, "sess-cap", id, "completed")
	if !strings.Contains(v.Reply, "deployed to prod") || !strings.Contains(v.Reply, "also tagged v2") {
		t.Fatalf("reply after the follow-up = %q", v.Reply)
	}
	noDeliveries(t, note)
}

// The person cancels their task; the captain is not told.
func TestUserTaskCancelFromUserNoWake(t *testing.T) {
	h, note, id := userAskingHub(t, "")
	waitTaskState(t, h, "sess-cap", id, "input_required")
	res, err := h.CancelFromUser(context.Background(), "sess-cap", id)
	if err != nil || res.State != "canceled" || !strings.Contains(res.Reason, "canceled by the user") {
		t.Fatalf("cancel = %+v, %v", res, err)
	}
	noDeliveries(t, note)
}

// The origin survives a restart: on disk, read back, and still refused
// to the agent.
func TestUserTaskOriginPersisted(t *testing.T) {
	dir := t.TempDir()
	h1, _, id := userAskingHub(t, dir)
	waitTaskState(t, h1, "sess-cap", id, "input_required")
	rec, ok := readRecord(filepath.Join(dir, id+".json"))
	if !ok || rec.Origin != OriginUser || rec.OriginUser != "u1" {
		t.Fatalf("record = %+v", rec)
	}
	h2, _, _ := newTestHub(func(Peer, string) string { return "x" })
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	list := h2.SentFrom("sess-cap")
	if len(list) != 1 || list[0].Origin != OriginUser || !list[0].NeedsYou {
		t.Fatalf("list after restart = %+v", list)
	}
	if _, err := h2.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: id, Text: "prod"}); !errors.Is(err, ErrUserTask) {
		t.Fatalf("agent answer after restart: %v", err)
	}
}

// A record written before origin existed reads as the agent's task.
func TestOldRecordWithoutOriginIsAgentTask(t *testing.T) {
	dir := t.TempDir()
	h1, _, id := userAskingHub(t, dir)
	waitTaskState(t, h1, "sess-cap", id, "input_required")
	path := filepath.Join(dir, id+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "origin")
	delete(m, "origin_user")
	b, _ := json.Marshal(m)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	h2, _, _ := newTestHub(func(Peer, string) string { return "x" })
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	list := h2.SentFrom("sess-cap")
	if len(list) != 1 || list[0].Origin != "" || list[0].State != "input_required" {
		t.Fatalf("old record = %+v", list)
	}
	got, err := h2.GetTask(context.Background(), "a-cap", id)
	if err != nil || !strings.Contains(got.Note, "Answer with message task_id=") {
		t.Fatalf("old record get_task = %+v, %v", got, err)
	}
}

// Only the person's mention route (Human) makes a task theirs: an agent's
// own message — a mention line in its reply included — stays its own.
func TestAgentSendIsNeverUserTask(t *testing.T) {
	for _, in := range []SendInput{
		{CallerSession: "sess-cap", CallerAgentID: "a-cap", SessionUser: "u1", To: "anton", Text: "deploy it", Wait: -1},
		{CallerSession: "sess-cap", CallerAgentID: "a-cap", SessionUser: "u1", To: "anton", Text: "deploy it", Wait: -1, Mention: true},
	} {
		h, _, _ := newTestHub(func(Peer, string) string { return InputRequiredToken + " which environment?" })
		res, err := h.Send(context.Background(), in)
		if err != nil {
			t.Fatal(err)
		}
		if v := waitTaskState(t, h, "sess-cap", res.TaskID, "input_required"); v.Origin != "" {
			t.Fatalf("agent task read as the user's: %+v", v)
		}
	}
}

// B1: a restart lost the person's task, so their answer goes as a new
// task. That task is still theirs: the teammate's next question needs
// them and never reaches the captain's chat, and the captain cannot
// answer it.
func TestUserTaskLostOnRestartStaysTheirs(t *testing.T) {
	dir := t.TempDir()
	h1, _, id := userAskingHub(t, dir)
	waitTaskState(t, h1, "sess-cap", id, "input_required")

	h2, _, note := newTestHub(func(Peer, string) string { return InputRequiredToken + " which region?" })
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, err := h2.AnswerFromUser(ctx, "sess-cap", id, "prod")
	if err != nil || a.TaskID == id {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	v := waitTaskState(t, h2, "sess-cap", a.TaskID, "input_required")
	if v.Origin != OriginUser || !v.NeedsYou || v.Reply != "which region?" {
		t.Fatalf("new task = %+v", v)
	}
	noDeliveries(t, note)
	if _, err := h2.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: a.TaskID, Text: "eu"}); !errors.Is(err, ErrUserTask) {
		t.Fatalf("agent answer to the new task: %v", err)
	}
	if rec, ok := readRecord(filepath.Join(dir, a.TaskID+".json")); !ok || rec.Origin != OriginUser || rec.OriginUser != "u1" {
		t.Fatalf("new record = %+v", rec)
	}
}

// B2: the turn that runs the person's answer is audited as theirs too,
// so a remote's late reply to it is not forwarded into the captain's chat.
func TestUserAnswerTurnHandoffIsTheirs(t *testing.T) {
	h, note, id := userAskingHub(t, "")
	waitTaskState(t, h, "sess-cap", id, "input_required")
	if _, err := h.AnswerFromUser(context.Background(), "sess-cap", id, "prod"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	waitTaskState(t, h, "sess-cap", id, "completed")
	note.mu.Lock()
	hs := append([]Handoff(nil), note.handoffs...)
	note.mu.Unlock()
	if len(hs) < 6 {
		t.Fatalf("handoffs = %+v", hs)
	}
	for _, ho := range hs {
		if ho.TaskID == id && ho.Origin != OriginUser {
			t.Fatalf("handoff without the origin: %+v", ho)
		}
	}
}

// S1: the person's answer continues their mention, whatever the
// teammate's mention policy says; an answer to the agent's own task gets
// no such pass.
func TestUserAnswerPassesTheTargetsMentionPolicy(t *testing.T) {
	ctx := context.Background()
	for _, policy := range []string{MentionOff, MentionList} {
		h, note, id := userAskingHub(t, "")
		setPeer(h, "a-anton", func(p *Peer) { p.MentionFrom, p.MentionAllow = policy, nil })
		waitTaskState(t, h, "sess-cap", id, "input_required")
		if _, err := h.AnswerFromUser(ctx, "sess-cap", id, "prod"); err != nil {
			t.Fatalf("%s: answer: %v", policy, err)
		}
		if v := waitTaskState(t, h, "sess-cap", id, "completed"); v.Reply != "deployed to prod" {
			t.Fatalf("%s: view = %+v", policy, v)
		}
		noDeliveries(t, note)
	}

	h, _, _ := newTestHub(func(Peer, string) string { return InputRequiredToken + " which environment?" })
	q, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("agent task = %+v, %v", q, err)
	}
	setPeer(h, "a-anton", func(p *Peer) { p.MentionFrom = MentionOff })
	if _, err := h.AnswerFromUser(ctx, "sess-cap", q.TaskID, "prod"); !errors.Is(err, ErrMentionsOff) {
		t.Fatalf("answer to the agent's task = %v, want ErrMentionsOff", err)
	}
}

// S1: in bob's chat with alice's lena, bob's @anton is bob's own anton;
// his answer reaches that same anton again, not alice's.
func TestUserAnswerThroughAShareReachesTheSameTeammate(t *testing.T) {
	h, _, turns, _ := newLinkHub(nil)
	turns.asks = 1
	ctx := context.Background()
	q, err := h.Send(ctx, SendInput{CallerSession: "s-lena-bob", CallerAgentID: "a-lena", SessionUser: "bob", To: "@anton", Text: "hi", Human: true, Mention: true, Wait: -1})
	if err != nil {
		t.Fatal(err)
	}
	waitTaskState(t, h, "s-lena-bob", q.TaskID, "input_required")
	if _, err := h.AnswerFromUser(ctx, "s-lena-bob", q.TaskID, "the blue one"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if v := waitTaskState(t, h, "s-lena-bob", q.TaskID, "completed"); v.Reply != "ok" || v.Origin != OriginUser {
		t.Fatalf("view = %+v", v)
	}
	runs := turns.runs()
	if len(runs) != 2 || !strings.HasPrefix(runs[0], "anton/bob@") || !strings.HasPrefix(runs[1], "anton/bob@") {
		t.Fatalf("runs = %v", runs)
	}
}

// S2/S4: a late reply to the person's task whose turn failed (a remote's
// timeout) becomes its reply; a follow-up never rewrites a question.
func TestUserTaskFollowUpByState(t *testing.T) {
	h, note, id := userAskingHub(t, "")
	waitTaskState(t, h, "sess-cap", id, "input_required")
	h.mu.Lock()
	h.answered["sess-anton"] = a2a.TaskID(id)
	h.mu.Unlock()
	ctx := context.Background()
	if !h.FollowUp(ctx, "sess-anton", "one more thing") {
		t.Fatal("follow-up not handled")
	}
	if v := waitTaskState(t, h, "sess-cap", id, "input_required"); v.Reply != "which environment?" {
		t.Fatalf("question rewritten: %+v", v)
	}
	h.mu.Lock()
	ref := h.tasks[a2a.TaskID(id)]
	ref.state, ref.reply = a2a.TaskStateFailed, "the teammate's turn failed: timed out"
	h.mu.Unlock()
	if !h.FollowUp(ctx, "sess-anton", "deployed to prod, late") {
		t.Fatal("late reply not handled")
	}
	v := waitTaskState(t, h, "sess-cap", id, "completed")
	if v.Reply != "deployed to prod, late" || v.Origin != OriginUser {
		t.Fatalf("view after the late reply = %+v", v)
	}
	if list := h.SentFrom("sess-cap"); len(list) != 1 || list[0].Reply != "deployed to prod, late" {
		t.Fatalf("sent from = %+v", list)
	}
	noDeliveries(t, note)
}
