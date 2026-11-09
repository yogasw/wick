package channels

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

// A compaction boundary is context-window bookkeeping: it shows in the web
// UI, never as a line in a chat thread, whatever triggered it.
func TestSystemNoticeTextSkipsCompaction(t *testing.T) {
	for _, ev := range []event.AgentEvent{
		{Type: event.Compaction, Compaction: &event.CompactionInfo{Trigger: "manual", PreTokens: 342500, PostTokens: 12500}},
		{Type: event.Compaction, Compaction: &event.CompactionInfo{PreTokens: 180000, PostTokens: 9000}},
		{Type: event.Compaction},
	} {
		if text, ok := SystemNoticeText(ev); ok {
			t.Errorf("compaction produced a notice %q; it must stay out of chat channels", text)
		}
	}
}

func TestSystemNoticeTextNotice(t *testing.T) {
	got, ok := SystemNoticeText(event.AgentEvent{Type: event.Notice, Text: "teammate replied"})
	if !ok || got != "teammate replied" {
		t.Errorf("got %q, %v; want the notice text", got, ok)
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
