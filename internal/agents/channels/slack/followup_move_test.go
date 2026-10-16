package slack

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// A follow-up that takes over a turn with a live reply deletes that reply and
// the next flush posts the gathered text as a new message, not an edit.
func TestFollowUpMovesLiveReplyBelow(t *testing.T) {
	f := newLiveFake(t, false)
	c := f.channel()
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: "setengah jawab"})
	c.flushLiveMessage("slack-t1")

	c.mu.Lock()
	old := c.turns["slack-t1"]
	nt := &turn{channelID: old.channelID, threadTS: old.threadTS, running: true}
	nt.carryOver(old)
	c.turns["slack-t1"] = nt
	c.mu.Unlock()
	c.moveLiveBelow("slack-t1")
	defer c.stopEditTicker(c.turns["slack-t1"])

	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: " dan sisanya"})
	c.flushLiveMessage("slack-t1")
	calls := f.got()
	if len(calls) != 3 || calls[0].method != "post" || calls[1].method != "delete" || calls[2].method != "post" {
		t.Fatalf("want post, delete, post; got %+v", calls)
	}
	if got := callText(calls[2]); !strings.Contains(got, "setengah jawab dan sisanya") {
		t.Fatalf("re-posted %q, want the whole text so far", got)
	}
}

// chat.delete refused: the old message is left alone and the text continues
// as a new message below, never as an edit of the old one.
func TestFollowUpDeleteRefusedContinuesBelow(t *testing.T) {
	f := newLiveFake(t, true)
	c := f.channel()
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: "setengah"})
	c.flushLiveMessage("slack-t1")
	c.mu.Lock()
	old := c.turns["slack-t1"]
	nt := &turn{channelID: old.channelID, threadTS: old.threadTS, running: true}
	nt.carryOver(old)
	c.turns["slack-t1"] = nt
	c.mu.Unlock()
	c.moveLiveBelow("slack-t1")
	defer c.stopEditTicker(c.turns["slack-t1"])
	c.OnAgentEvent("slack-t1", event.AgentEvent{Type: event.TextDelta, Text: " lanjut"})
	c.flushLiveMessage("slack-t1")
	calls := f.got()
	last := calls[len(calls)-1]
	if last.method != "post" || !strings.Contains(callText(last), "setengah lanjut") {
		t.Fatalf("want a fresh post with the whole text last, got %+v", calls)
	}
	for _, c := range calls {
		if c.method == "update" {
			t.Fatalf("old message edited after a follow-up: %+v", calls)
		}
	}
}
