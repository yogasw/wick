package channels

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

func TestSystemNoticeTextCompaction(t *testing.T) {
	got, ok := SystemNoticeText(event.AgentEvent{
		Type: event.Compaction,
		Compaction: &event.CompactionInfo{
			Trigger: "manual", PreTokens: 342500, PostTokens: 12500,
		},
	})
	if !ok {
		t.Fatal("a compaction boundary must be surfaced to channels")
	}
	want := "Context compacted (manual) — 342.5k → 12.5k tokens"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The trigger is only named for /compact, so an unnamed one is the window
// limit — which is the case a reader most needs told apart from a manual one.
func TestSystemNoticeTextCompactionDefaultsToAuto(t *testing.T) {
	got, _ := SystemNoticeText(event.AgentEvent{
		Type:       event.Compaction,
		Compaction: &event.CompactionInfo{PreTokens: 180000, PostTokens: 9000},
	})
	want := "Context compacted (auto) — 180.0k → 9.0k tokens"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A Compaction with no payload says nothing worth posting; a notice built
// from zeros would read as a real compaction of nothing.
func TestSystemNoticeTextCompactionWithoutPayload(t *testing.T) {
	if _, ok := SystemNoticeText(event.AgentEvent{Type: event.Compaction}); ok {
		t.Fatal("a compaction with no metadata must not produce a notice")
	}
}

// Reply traffic is the channel's own business — duplicating it as a notice
// would post every turn twice.
func TestSystemNoticeTextIgnoresReplyTraffic(t *testing.T) {
	for _, ev := range []event.AgentEvent{
		{Type: event.TextDelta, Text: "hello"},
		{Type: event.ToolUse, ToolName: "Bash"},
		{Type: event.ToolResult, Text: "ok"},
		{Type: event.Done},
		{Type: event.Error, ErrorMsg: "boom"},
		{Type: event.Thinking, Text: "hmm"},
	} {
		if text, ok := SystemNoticeText(ev); ok {
			t.Errorf("%v produced a notice %q; only session-level events should", ev.Type, text)
		}
	}
}
