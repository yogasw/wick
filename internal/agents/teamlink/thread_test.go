package teamlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// threadOf is task id's thread as sess-cap reads it.
func threadOf(t *testing.T, h *Hub, id string) []ThreadEntry {
	t.Helper()
	log, err := h.TaskThread("sess-cap", id)
	if err != nil {
		t.Fatalf("thread of %s: %v", id, err)
	}
	return log
}

// roles is a thread as "role:text" lines, for a compact comparison.
func roles(log []ThreadEntry) string {
	var b []string
	for _, e := range log {
		b = append(b, e.Role+":"+e.Text)
	}
	return strings.Join(b, " | ")
}

// The brief, the question, the captain's answer and the reply each join
// the thread, named and dated; message_count follows.
func TestThreadLogsBriefQuestionAnswerReply(t *testing.T) {
	clock := &testClock{now: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)}
	h, _, _ := newTestHub(func(_ Peer, text string) string {
		if strings.Contains(text, "deploy") {
			return InputRequiredToken + " which environment?"
		}
		return "deployed to prod"
	})
	h.now = clock.get
	ctx := context.Background()
	q, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}
	clock.add(time.Minute)
	if _, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: q.TaskID, Text: "prod"}); err != nil {
		t.Fatal(err)
	}
	log := threadOf(t, h, q.TaskID)
	if got := roles(log); got != "captain:deploy it | teammate:which environment? | captain:prod | teammate:deployed to prod" {
		t.Fatalf("thread = %s", got)
	}
	if log[0].Handle != "captain" || log[0].Name != "Yoga Bot" || log[1].Handle != "vera" || log[1].Name != "Vera" {
		t.Fatalf("authors = %+v", log)
	}
	if !log[2].At.After(log[1].At) {
		t.Fatalf("answer dated %v, question %v", log[2].At, log[1].At)
	}
	if v := viewOf(t, h, q.TaskID); v.MessageCount != 4 {
		t.Fatalf("message_count = %d, want 4", v.MessageCount)
	}
}

// The user's answer shows as the user's words, without the framing the
// teammate reads; a person's @mention opens the thread as the user's.
func TestThreadLogsUserMessages(t *testing.T) {
	h, _, _ := newTestHub(func(_ Peer, text string) string {
		if strings.Contains(text, "deploy") {
			return InputRequiredToken + " which environment?"
		}
		return "done"
	})
	h.CallerBusy = func(string) bool { return false }
	ctx := context.Background()
	q, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}
	if _, err := h.AnswerFromUser(ctx, "sess-cap", q.TaskID, "staging"); err != nil {
		t.Fatal(err)
	}
	log := threadOf(t, h, q.TaskID)
	if got := roles(log); got != "captain:deploy it | teammate:which environment? | user:staging | teammate:done" {
		t.Fatalf("thread = %s", got)
	}
	if log[2].Handle != "" || log[2].Name != "" {
		t.Fatalf("user entry carries an agent: %+v", log[2])
	}

	m, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", SessionUser: "u1", To: "@anton", Text: "check logs", Human: true, Mention: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := roles(threadOf(t, h, m.TaskID)); got != "user:check logs | teammate:done" {
		t.Fatalf("mention thread = %s", got)
	}
}

// A follow-up joins the thread (a repeat does not); a refusal is the
// teammate's word, a failure is not.
func TestThreadLogsFollowUpAndRefusal(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "first part" })
	ctx := context.Background()
	res, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "how?"})
	if err != nil || res.State != "completed" {
		t.Fatalf("send = %+v, %v", res, err)
	}
	h.FollowUp(ctx, "sess-a-anton", "second part")
	h.FollowUp(ctx, "sess-a-anton", "second part")
	if got := roles(threadOf(t, h, res.TaskID)); got != "captain:how? | teammate:first part | teammate:second part" {
		t.Fatalf("thread = %s", got)
	}

	h2, _, _ := newTestHub(func(Peer, string) string { return InputRequiredToken + " which?" })
	r, _ := h2.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "pick"})
	h2.finished(ctx, a2a.TaskID(r.TaskID), a2a.TaskStateRejected, "not my job")
	f, _ := h2.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "pick"})
	h2.finished(ctx, a2a.TaskID(f.TaskID), a2a.TaskStateFailed, "transport error")
	if got := roles(threadOf(t, h2, r.TaskID)); got != "captain:pick | teammate:which? | teammate:not my job" {
		t.Fatalf("rejected thread = %s", got)
	}
	if got := roles(threadOf(t, h2, f.TaskID)); got != "captain:pick | teammate:which?" {
		t.Fatalf("failed thread = %s", got)
	}
}

// The log keeps the newest maxThreadEntries, each text capped.
func TestThreadCap(t *testing.T) {
	ref := &taskRef{to: Peer{Handle: "vera", Name: "Vera"}}
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	for i := 0; i < maxThreadEntries+7; i++ {
		ref.logTeammateLocked(fmt.Sprintf("m%d", i), at.Add(time.Duration(i)*time.Second))
	}
	if len(ref.log) != maxThreadEntries || ref.log[0].Text != "m7" || ref.log[maxThreadEntries-1].Text != fmt.Sprintf("m%d", maxThreadEntries+6) {
		t.Fatalf("capped log = %d entries, first %q", len(ref.log), ref.log[0].Text)
	}
	ref.logTeammateLocked(strings.Repeat("x", threadTextMax+50), at.Add(time.Hour))
	if last := []rune(ref.log[len(ref.log)-1].Text); len(last) != threadTextMax || !strings.HasSuffix(string(last), "…") {
		t.Fatalf("long text kept %d runes", len(last))
	}
	// An entry dated before the last goes ahead of it.
	ref.logLocked(ThreadEntry{Role: ThreadCaptain, Text: "early", At: at.Add(30 * time.Minute)})
	if n := len(ref.log); ref.log[n-2].Text != "early" {
		t.Fatalf("early entry at %d: %+v", n, ref.log[n-2:])
	}
	ref.logTeammateLocked("   ", at.Add(2*time.Hour))
	if ref.log[len(ref.log)-1].Text == "" {
		t.Fatal("blank entry logged")
	}
}

// The thread survives a restart; an old record without one loads with
// no thread and no message_count on the wire.
func TestThreadPersists(t *testing.T) {
	dir := t.TempDir()
	h1, _, _ := newTestHub(func(Peer, string) string { return "all good" })
	if err := h1.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	res, err := h1.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "status?"})
	if err != nil {
		t.Fatal(err)
	}
	h2, _, _ := newTestHub(func(Peer, string) string { return "" })
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	if got := roles(threadOf(t, h2, res.TaskID)); got != "captain:status? | teammate:all good" {
		t.Fatalf("after restart = %s", got)
	}
	if v := viewOf(t, h2, res.TaskID); v.MessageCount != 2 {
		t.Fatalf("message_count after restart = %d", v.MessageCount)
	}

	var rec taskRecord
	if err := json.Unmarshal([]byte(`{"task_id":"old","caller_session":"sess-cap","started_at":"2026-10-11T09:00:00Z"}`), &rec); err != nil {
		t.Fatal(err)
	}
	ref := refFromRecord(rec)
	if len(ref.log) != 0 {
		t.Fatalf("old record log = %+v", ref.log)
	}
	b, _ := json.Marshal(TaskView{})
	if strings.Contains(string(b), "message_count") {
		t.Fatalf("zero message_count written: %s", b)
	}
	b, _ = json.Marshal(TaskView{MessageCount: 3})
	if !strings.Contains(string(b), `"message_count":3`) {
		t.Fatalf("view json = %s", b)
	}
}

// Another chat, or an unknown task, reads nothing.
func TestThreadOtherSession(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "ok" })
	res, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.TaskThread("sess-other", res.TaskID); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("other session: %v", err)
	}
	if _, err := h.TaskThread("sess-cap", "nope"); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("unknown task: %v", err)
	}
}

// A question lost with a restart and answered anyway: the replacement
// task's thread starts with the lost one's brief and question.
func TestThreadCarriedToSupersedingTask(t *testing.T) {
	dir := t.TempDir()
	h1, _, _ := newTestHub(func(Peer, string) string { return InputRequiredToken + " which environment?" })
	if err := h1.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	q, err := h1.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}
	h2, _, _ := newTestHub(func(Peer, string) string { return "deployed to prod" })
	h2.CallerBusy = func(string) bool { return false }
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	a, err := h2.AnswerFromUser(ctx, "sess-cap", q.TaskID, "prod")
	if err != nil || a.TaskID == q.TaskID {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	waitFor(t, "the new task to finish", func() bool {
		got, err := h2.GetTask(ctx, "a-cap", a.TaskID)
		return err == nil && got.State == "completed"
	})
	if got := roles(threadOf(t, h2, a.TaskID)); got != "captain:deploy it | teammate:which environment? | user:prod | teammate:deployed to prod" {
		t.Fatalf("replacement thread = %s", got)
	}
	if v := viewOf(t, h2, a.TaskID); v.MessageCount != 4 {
		t.Fatalf("message_count = %d, want 4", v.MessageCount)
	}
}

// The carried thread keeps the newest maxThreadEntries.
func TestThreadCarryCapped(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "" })
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	old := &taskRef{claimed: true, to: Peer{Handle: "vera"}}
	for i := 0; i < maxThreadEntries; i++ {
		old.logTeammateLocked(fmt.Sprintf("old%d", i), at.Add(time.Duration(i)*time.Second))
	}
	next := &taskRef{to: Peer{Handle: "vera"}}
	next.logTeammateLocked("new", at.Add(time.Hour))
	h.tasks["lost"], h.tasks["next"] = old, next
	h.supersede("lost", "next")
	if n := len(next.log); n != maxThreadEntries || next.log[0].Text != "old1" || next.log[n-1].Text != "new" {
		t.Fatalf("carried log = %d entries, first %q last %q", n, next.log[0].Text, next.log[n-1].Text)
	}
}
