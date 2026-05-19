package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
)

func newStore(t *testing.T, agentName string, recordRaw bool) (*Store, config.Layout) {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID:     "S1",
		Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
	if agentName != "" {
		if err := session.AddAgent(layout, "S1", agentName, "claude"); err != nil {
			t.Fatal(err)
		}
	}
	clk := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)
	st := New(Options{
		Layout:    layout,
		SessionID: "S1",
		AgentName: agentName,
		RecordRaw: recordRaw,
		Now:       func() time.Time { return clk },
	})
	return st, layout
}

func readConvLines(t *testing.T, layout config.Layout) []ConversationTurn {
	t.Helper()
	var out []ConversationTurn
	err := storage.ReadJSONL(layout.SessionConversation("S1"), func(line []byte) bool {
		var turn ConversationTurn
		if err := json.Unmarshal(line, &turn); err != nil {
			t.Fatal(err)
		}
		out = append(out, turn)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAppendUserTurn(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	if err := st.AppendUserTurn("user", "ui", "hello"); err != nil {
		t.Fatal(err)
	}
	lines := readConvLines(t, layout)
	if len(lines) != 1 || lines[0].Role != "user" || lines[0].Text != "hello" {
		t.Fatalf("turn: %+v", lines)
	}
}

func TestApplyTextDeltasFlushedOnDone(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	flushed, _ := st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "hello "})
	if flushed {
		t.Fatal("flush before Done")
	}
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "world"})
	flushed, err := st.Apply(event.AgentEvent{Type: event.Done})
	if err != nil {
		t.Fatal(err)
	}
	if !flushed {
		t.Fatal("Done did not flush")
	}
	lines := readConvLines(t, layout)
	if len(lines) != 1 || lines[0].Role != "assistant" || lines[0].Text != "hello world" {
		t.Fatalf("turn: %+v", lines)
	}
	if lines[0].Agent != "backend" {
		t.Fatalf("agent: %q", lines[0].Agent)
	}
}

func TestApplyErrorFlushesPartial(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "partial"})
	flushed, _ := st.Apply(event.AgentEvent{Type: event.Error, ErrorMsg: "boom"})
	if !flushed {
		t.Fatal("error did not flush")
	}
	lines := readConvLines(t, layout)
	if len(lines) != 1 || lines[0].Text != "partial" {
		t.Fatalf("turn: %+v", lines)
	}
}

func TestSessionStartPersistsCLISessionID(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	if _, err := st.Apply(event.AgentEvent{Type: event.SessionStart, SessionID: "claude-abc"}); err != nil {
		t.Fatal(err)
	}
	sess, err := session.Load(layout, "S1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.Agents) != 1 || sess.Agents[0].CLISessionID != "claude-abc" {
		t.Fatalf("agents: %+v", sess.Agents)
	}
}

func TestSessionStartWithoutAgentNameSkipsPersist(t *testing.T) {
	st, layout := newStore(t, "", false)
	if _, err := st.Apply(event.AgentEvent{Type: event.SessionStart, SessionID: "claude-x"}); err != nil {
		t.Fatal(err)
	}
	sess, _ := session.Load(layout, "S1")
	if len(sess.Agents) != 0 {
		t.Fatalf("agents shouldn't be touched: %+v", sess.Agents)
	}
}

func TestFlushMarksTruncated(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "incomplete"})
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	lines := readConvLines(t, layout)
	if len(lines) != 1 || !lines[0].Truncated {
		t.Fatalf("expected truncated turn: %+v", lines)
	}
}

func TestRecordRawAppendsRawJSONL(t *testing.T) {
	st, layout := newStore(t, "backend", true)
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "x", Raw: `{"type":"content_block_delta"}`})
	count, err := storage.CountJSONLEntries(layout.SessionRaw("S1"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("raw entries: %d", count)
	}
}

func TestLargeTurnTruncated(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	huge := strings.Repeat("x", MaxAssistantTurnBytes+100)
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: huge})
	st.Apply(event.AgentEvent{Type: event.Done})
	lines := readConvLines(t, layout)
	if len(lines) != 1 || !lines[0].Truncated {
		t.Fatalf("expected truncated: %+v", lines[0].Truncated)
	}
	if len(lines[0].Text) > MaxAssistantTurnBytes+50 {
		t.Fatalf("body not capped: %d", len(lines[0].Text))
	}
}

func TestApplyThinkingBufferedInEvents(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	st.Apply(event.AgentEvent{Type: event.Thinking, Text: "let me think"})
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "answer"})
	st.Apply(event.AgentEvent{Type: event.Done})
	lines := readConvLines(t, layout)
	if len(lines) != 1 {
		t.Fatalf("turns: %d", len(lines))
	}
	if len(lines[0].Events) != 1 {
		t.Fatalf("events: %d, want 1", len(lines[0].Events))
	}
	ev := lines[0].Events[0]
	if ev.Type != "thinking" || ev.Text != "let me think" {
		t.Fatalf("thinking event: %+v", ev)
	}
}

func TestApplyToolUseAndResultBuffered(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	st.Apply(event.AgentEvent{
		Type:      event.ToolUse,
		ToolName:  "Bash",
		ToolInput: `{"command":"ls"}`,
		ToolUseID: "t1",
	})
	st.Apply(event.AgentEvent{
		Type:      event.ToolResult,
		Text:      "file1\nfile2",
		ToolUseID: "t1",
		IsError:   false,
	})
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "done"})
	st.Apply(event.AgentEvent{Type: event.Done})
	lines := readConvLines(t, layout)
	if len(lines) != 1 {
		t.Fatalf("turns: %d", len(lines))
	}
	evs := lines[0].Events
	if len(evs) != 2 {
		t.Fatalf("events: %d, want 2", len(evs))
	}
	if evs[0].Type != "tool_use" || evs[0].ToolName != "Bash" || evs[0].ToolUseID != "t1" {
		t.Fatalf("tool_use event: %+v", evs[0])
	}
	if evs[1].Type != "tool_result" || evs[1].ToolUseID != "t1" || evs[1].IsError {
		t.Fatalf("tool_result event: %+v", evs[1])
	}
}

func TestApplyToolResultIsErrorFlagged(t *testing.T) {
	st, layout := newStore(t, "backend", false)
	st.Apply(event.AgentEvent{
		Type:      event.ToolUse,
		ToolName:  "Bash",
		ToolInput: `{"command":"bad"}`,
		ToolUseID: "t2",
	})
	st.Apply(event.AgentEvent{
		Type:      event.ToolResult,
		Text:      "command not found",
		ToolUseID: "t2",
		IsError:   true,
	})
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "sorry"})
	st.Apply(event.AgentEvent{Type: event.Done})
	lines := readConvLines(t, layout)
	evs := lines[0].Events
	if len(evs) != 2 {
		t.Fatalf("events: %d", len(evs))
	}
	if !evs[1].IsError {
		t.Fatal("expected IsError=true on tool_result")
	}
}

func TestEventBufferClearedBetweenTurns(t *testing.T) {
	// Events from turn 1 must not bleed into turn 2.
	st, layout := newStore(t, "backend", false)
	// turn 1
	st.Apply(event.AgentEvent{Type: event.Thinking, Text: "turn1 thought"})
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "reply1"})
	st.Apply(event.AgentEvent{Type: event.Done})
	// turn 2 — no events, just text
	st.Apply(event.AgentEvent{Type: event.TextDelta, Text: "reply2"})
	st.Apply(event.AgentEvent{Type: event.Done})

	lines := readConvLines(t, layout)
	if len(lines) != 2 {
		t.Fatalf("turns: %d", len(lines))
	}
	if len(lines[0].Events) != 1 {
		t.Fatalf("turn1 events: %d", len(lines[0].Events))
	}
	if len(lines[1].Events) != 0 {
		t.Fatalf("turn2 should have no events, got: %+v", lines[1].Events)
	}
}
