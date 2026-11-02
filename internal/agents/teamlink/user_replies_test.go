package teamlink

import (
	"context"
	"strings"
	"testing"
)

// The reply to a person's @mention never wakes the chat's agent, but is
// handed to it once with the person's next message.
func TestUserTaskReplyHeldForNextMessage(t *testing.T) {
	h, _, note := newTestHub(func(_ Peer, _ string) string { return "the answer is 42" })
	res, err := h.Send(context.Background(), SendInput{
		CallerSession: "sess-cap", CallerAgentID: "a-cap", SessionUser: "u1",
		To: "anton", Text: "what is it?", Wait: -1, Mention: true, Human: true,
	})
	if err != nil {
		t.Fatalf("send = %+v, %v", res, err)
	}
	waitTaskState(t, h, "sess-cap", res.TaskID, "completed")
	noDeliveries(t, note)

	got := h.TakeUserReplies("sess-cap")
	if !strings.HasPrefix(got, UserRepliesHeader) || !strings.Contains(got, "the answer is 42") || !strings.Contains(got, "@anton") {
		t.Fatalf("held = %q", got)
	}
	if again := h.TakeUserReplies("sess-cap"); again != "" {
		t.Fatalf("handed over twice: %q", again)
	}
	if other := h.TakeUserReplies("sess-other"); other != "" {
		t.Fatalf("another chat got it: %q", other)
	}
}

// An agent's own message is delivered as a reply, not held.
func TestAgentTaskReplyNotHeld(t *testing.T) {
	h, _, _ := newTestHub(func(_ Peer, _ string) string { return "done" })
	res, err := h.Send(context.Background(), SendInput{
		CallerSession: "sess-cap", CallerAgentID: "a-cap", SessionUser: "u1",
		To: "anton", Text: "do it", Wait: -1,
	})
	if err != nil {
		t.Fatalf("send = %+v, %v", res, err)
	}
	waitTaskState(t, h, "sess-cap", res.TaskID, "completed")
	if got := h.TakeUserReplies("sess-cap"); got != "" {
		t.Fatalf("held = %q", got)
	}
}

// Only the newest replies are kept, each cut to a bounded size.
func TestUserRepliesBounded(t *testing.T) {
	h := &Hub{}
	long := strings.Repeat("x", userReplyMaxChars+50)
	for i := 0; i < userReplyKeep+2; i++ {
		h.noteUserReply("s", Peer{Handle: "anton"}, "t", "TASK_STATE_COMPLETED", long)
	}
	got := h.TakeUserReplies("s")
	if n := strings.Count(got, "From "); n != userReplyKeep {
		t.Fatalf("kept %d, want %d", n, userReplyKeep)
	}
	if !strings.Contains(got, "…(cut)") {
		t.Fatal("long reply not cut")
	}
}
