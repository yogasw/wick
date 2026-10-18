package teamlink

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A reply that opens with [silent] and says more is still the asker's
// answer: only the marker goes.
func TestSilentMarkerWithTextIsDelivered(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "[silent] noted: 3 rows fixed" })
	res, err := h.Send(context.Background(), SendInput{CallerSession: "s", CallerAgentID: "a-cap", To: "anton", Text: "fyi"})
	if err != nil || res.State != "completed" || res.ReplyText != "noted: 3 rows fixed" {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

func TestStripSilent(t *testing.T) {
	for in, want := range map[string]string{
		"[silent]":          "",
		"  [SILENT] x ":     "x",
		"[silent]\nreport":  "report",
		"not [silent] here": "not [silent] here",
	} {
		if got, _ := StripSilent(in); got != want {
			t.Errorf("StripSilent(%q) = %q, want %q", in, got, want)
		}
	}
}

// What the teammate says on its own after its task ended (the final
// report of work it kept doing) reaches the asker as a follow-up of that
// task; a reply older than the task's end or past the window does not.
func TestFollowUpAfterTaskEnded(t *testing.T) {
	h, turns, note := newFanHub(t, false)
	a := fanSend(t, h, "anton", nil)
	turns.line("anton") <- "[silent]"
	settled(t, h, a.TaskID, "completed")
	quiet(t, note, 0)
	if !h.HasAnswered("sess-a-anton") {
		t.Fatal("answering session not known")
	}
	ctx := context.Background()
	if h.FollowUpAfter(ctx, "sess-a-anton", "too early", time.Now().Add(-time.Hour), time.Time{}) {
		t.Fatal("a reply from before the task ended was forwarded")
	}
	if h.FollowUpAfter(ctx, "sess-a-anton", "too late", time.Now().Add(FollowUpWindow+time.Hour), time.Time{}) {
		t.Fatal("a reply past the window was forwarded")
	}
	// A person wrote in the chat after the task ended: the chat is
	// theirs, what the agent tells them is not the asker's (B4).
	if h.FollowUpAfter(ctx, "sess-a-anton", "Yoga, here is the summary", time.Now().Add(time.Second), time.Now().Add(time.Millisecond)) {
		t.Fatal("a reply to a person was forwarded")
	}
	if !h.FollowUpAfter(ctx, "sess-a-anton", "final report: all 12 done", time.Now().Add(time.Second), time.Now().Add(-time.Hour)) {
		t.Fatal("final report not forwarded")
	}
	got := quiet(t, note, 1)[0]
	if !strings.Contains(got, "Follow-up from Anton (@anton) [task "+a.TaskID+"]") || !strings.Contains(got, "final report: all 12 done") {
		t.Fatalf("delivered = %q", got)
	}
	if h.FollowUpAfter(ctx, "sess-unknown", "x", time.Now(), time.Time{}) {
		t.Fatal("a session that answered nothing forwarded")
	}
}

// The agent that got a task may write back into the conversation that
// sent it, without a shared ticket; any other chat of the asker stays
// closed.
func TestReplyToTaskSenderChat(t *testing.T) {
	h, turns, _ := newFanHub(t, false)
	a := fanSend(t, h, "anton", nil)
	turns.line("anton") <- "on it"
	settled(t, h, a.TaskID, "completed")
	ctx := context.Background()
	capPeer, err := h.Dir.Get(ctx, "a-cap")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.chatAllowed(ctx, capPeer, "sess-a-anton", "sess-cap", false); err != nil {
		t.Fatalf("reply to the sender's chat refused: %v", err)
	}
	if _, err := h.chatAllowed(ctx, capPeer, "sess-a-anton", "sess-other", false); err == nil {
		t.Fatal("another chat of the asker was let in")
	}
	if _, err := h.chatAllowed(ctx, capPeer, "sess-a-vera", "sess-cap", false); err == nil {
		t.Fatal("a chat that answered no task of the asker was let in")
	}
}
