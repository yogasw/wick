package event

import "testing"

// parseAll feeds lines through a parser and returns all non-Unknown
// events. Errors fail the test.
func parseAll(t *testing.T, p Parser, lines []string) []AgentEvent {
	t.Helper()
	var out []AgentEvent
	for i, line := range lines {
		ev, err := p.Parse(line)
		if err != nil {
			t.Fatalf("line %d parse: %v\nline: %s", i, err, line)
		}
		if ev.Type == Unknown {
			continue
		}
		out = append(out, ev)
	}
	return out
}

func TestClaudeParserSessionStartOnce(t *testing.T) {
	// Real claude shape: `system subtype=init` carries the session_id.
	// Subsequent init events (next turn within the same process)
	// should not re-fire SessionStart.
	p := NewClaudeParser()
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"abc-123","cwd":"/tmp"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]},"session_id":"abc-123"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"hi","session_id":"abc-123"}`,
		`{"type":"system","subtype":"init","session_id":"abc-123","cwd":"/tmp"}`, // turn 2 init
		`{"type":"assistant","message":{"content":[{"type":"text","text":"again"}]},"session_id":"abc-123"}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"again","session_id":"abc-123"}`,
	}
	events := parseAll(t, p, lines)
	// Expect: 1× SessionStart, 2× TextDelta, 2× Done = 5 events.
	if len(events) != 5 {
		t.Fatalf("events: got %d, want 5: %+v", len(events), events)
	}
	if events[0].Type != SessionStart || events[0].SessionID != "abc-123" {
		t.Fatalf("first event: %+v", events[0])
	}
	// Second SessionStart should NOT fire — second event must be TextDelta.
	if events[1].Type != TextDelta {
		t.Fatalf("expected TextDelta after init, got %+v", events[1])
	}
	if p.SessionID() != "abc-123" {
		t.Fatalf("SessionID(): %q", p.SessionID())
	}
}

func TestClaudeParserAssistantText(t *testing.T) {
	p := NewClaudeParser()
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello world"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"hello world"}`,
	}
	events := parseAll(t, p, lines)
	if len(events) != 3 {
		t.Fatalf("events: %d (%+v)", len(events), events)
	}
	if events[1].Type != TextDelta || events[1].Text != "hello world" {
		t.Fatalf("text event: %+v", events[1])
	}
	if events[2].Type != Done {
		t.Fatalf("last event not Done: %+v", events[2])
	}
}

func TestClaudeParserAssistantConcatenatesTextBlocks(t *testing.T) {
	// Claude can pack multiple text blocks in one assistant frame.
	p := NewClaudeParser()
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello "},{"type":"text","text":"world"}]}}`,
	}
	events := parseAll(t, p, lines)
	if len(events) != 2 {
		t.Fatalf("events: %d (%+v)", len(events), events)
	}
	if events[1].Text != "hello world" {
		t.Fatalf("concat: %q", events[1].Text)
	}
}

func TestClaudeParserToolUseExtractsName(t *testing.T) {
	p := NewClaudeParser()
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls -la"}}]}}`,
	}
	events := parseAll(t, p, lines)
	if len(events) != 2 {
		t.Fatalf("events: %d (%+v)", len(events), events)
	}
	tu := events[1]
	if tu.Type != ToolUse {
		t.Fatalf("tool use: %+v", tu)
	}
	if tu.ToolName != "Bash" {
		t.Fatalf("tool name: %q", tu.ToolName)
	}
	if tu.ToolInput == "" || !contains(tu.ToolInput, "ls -la") {
		t.Fatalf("tool input: %q", tu.ToolInput)
	}
}

func TestClaudeParserToolUsePreferredOverText(t *testing.T) {
	// When an assistant frame contains BOTH text and tool_use, we
	// surface tool_use because it's gate-relevant. The trailing
	// `result` event still carries the final user-visible text.
	p := NewClaudeParser()
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"running command"},{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`
	ev, err := p.Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != ToolUse {
		t.Fatalf("expected ToolUse, got %v", ev.Type)
	}
}

func TestClaudeParserToolResultFromUserMessage(t *testing.T) {
	// Tool results come back as `user` messages with tool_result blocks.
	p := NewClaudeParser()
	line := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"file1\nfile2"}]}}`
	ev, err := p.Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != ToolResult {
		t.Fatalf("expected ToolResult, got %v", ev.Type)
	}
	if !contains(ev.Text, "file1") {
		t.Fatalf("text: %q", ev.Text)
	}
}

func TestClaudeParserResultErrorBecomesErrorEvent(t *testing.T) {
	p := NewClaudeParser()
	ev, err := p.Parse(`{"type":"result","subtype":"success","is_error":true,"result":"rate limited"}`)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Type != Error || ev.ErrorMsg != "rate limited" {
		t.Fatalf("event: %+v", ev)
	}
}

func TestClaudeParserUnknownSystemSubtypeSkipped(t *testing.T) {
	// hook_started / hook_response / rate_limit_event / etc. should
	// not drive downstream state. They map to Unknown.
	p := NewClaudeParser()
	cases := []string{
		`{"type":"system","subtype":"hook_started"}`,
		`{"type":"system","subtype":"hook_response"}`,
		`{"type":"rate_limit_event","rate_limit_info":{}}`,
	}
	for _, line := range cases {
		ev, err := p.Parse(line)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		if ev.Type != Unknown {
			t.Fatalf("expected Unknown for %q, got %v", line, ev.Type)
		}
	}
}

func TestClaudeParserBlankLine(t *testing.T) {
	p := NewClaudeParser()
	ev, err := p.Parse("   ")
	if err != nil {
		t.Fatalf("blank line errored: %v", err)
	}
	if ev.Type != Unknown {
		t.Fatalf("blank should be Unknown, got %v", ev.Type)
	}
}

func TestClaudeParserMalformedReturnsError(t *testing.T) {
	p := NewClaudeParser()
	if _, err := p.Parse("not json"); err == nil {
		t.Fatal("expected parse error on garbage input")
	}
}

// contains is a tiny substring helper to keep tests strings.HasPrefix-free
// (we don't import strings here on purpose — keeps the test file lean).
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
