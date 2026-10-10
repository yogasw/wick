package teamlink

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// A question's "[options: …]" line becomes the task's choices: off the
// question text, on the result and the task list, kept across a restart,
// and gone once the question is answered.
func TestQuestionOptionsLine(t *testing.T) {
	dir := t.TempDir()
	h1, _, _ := newTestHub(func(_ Peer, text string) string {
		if strings.HasSuffix(text, "prod") {
			return "deployed to prod"
		}
		return InputRequiredToken + " Which environment?\n[options: prod | staging | " + strings.Repeat("x", 45) + " | dev | qa]"
	})
	if err := h1.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	want := []string{"prod", "staging", "dev", "qa"}
	q, err := h1.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "deploy it"})
	if err != nil || q.State != "input_required" || q.ReplyText != "Which environment?" || !reflect.DeepEqual(q.Options, want) {
		t.Fatalf("question = %+v, %v", q, err)
	}
	list := h1.SentFrom("sess-cap")
	if len(list) != 1 || list[0].Reply != "Which environment?" || list[0].Summary != "Which environment?" || !reflect.DeepEqual(list[0].Options, want) {
		t.Fatalf("list = %+v", list)
	}

	h2, _, _ := newTestHub(func(Peer, string) string { return "deployed to prod" })
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	if list := h2.SentFrom("sess-cap"); len(list) != 1 || !reflect.DeepEqual(list[0].Options, want) {
		t.Fatalf("list after restart = %+v", list)
	}

	a, err := h1.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: q.TaskID, Text: "prod"})
	if err != nil || a.State != "completed" || a.Options != nil {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	if list := h1.SentFrom("sess-cap"); len(list) != 1 || list[0].Options != nil {
		t.Fatalf("list after the answer = %+v", list)
	}
}

// A question without the line has no choices and keeps its text.
func TestQuestionWithoutOptionsLine(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return InputRequiredToken + " Which one (a / b)?" })
	q, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "fix it"})
	if err != nil || q.ReplyText != "Which one (a / b)?" || q.Options != nil {
		t.Fatalf("question = %+v, %v", q, err)
	}
}

// An answer that never reaches the teammate gives the question back with
// its choices.
func TestUnclaimRestoresOptions(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return InputRequiredToken + " Which env?\n[options: prod | staging]" })
	q, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}
	_, _, claim, err := h.answering("a-cap", SendInput{TaskID: q.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	if list := h.SentFrom("sess-cap"); len(list) != 1 || list[0].Options != nil {
		t.Fatalf("claimed task still offers choices: %+v", list)
	}
	h.unclaim(a2a.TaskID(q.TaskID), claim)
	if v := waitTaskState(t, h, "sess-cap", q.TaskID, "input_required"); !reflect.DeepEqual(v.Options, []string{"prod", "staging"}) {
		t.Fatalf("choices not given back: %+v", v)
	}
}

// A remote posting its question again, options line and all, is the text
// the caller already has: it is not delivered twice.
func TestFollowUpSameQuestionWithOptionsIsDropped(t *testing.T) {
	const ask = "Which env?\n[options: prod | staging]"
	h, _, note := newTestHub(func(Peer, string) string { return InputRequiredToken + " " + ask })
	ctx := context.Background()
	if q, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "deploy it"}); err != nil || q.ReplyText != "Which env?" {
		t.Fatalf("question = %+v, %v", q, err)
	}
	note.mu.Lock()
	before := len(note.delivered)
	note.mu.Unlock()
	if !h.FollowUp(ctx, "sess-a-anton", ask) {
		t.Fatal("follow-up not handled")
	}
	note.mu.Lock()
	defer note.mu.Unlock()
	if len(note.delivered) != before {
		t.Fatalf("same question delivered again: %q", note.delivered[before:])
	}
}
