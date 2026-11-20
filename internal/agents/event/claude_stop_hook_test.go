package event

import "testing"

// A blocking Stop hook sends claude back to work inside the same turn:
// the text it ended on is refused, so the parser takes it back.
func TestParseStopHookFeedbackTakesTextBack(t *testing.T) {
	p := NewClaudeParser()
	ev, err := p.Parse(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Stop hook feedback:\nreply was interim"}]}}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != TextReplace || ev.Text != "" {
		t.Fatalf("got %v %q, want an empty TextReplace", ev.Type, ev.Text)
	}
}

// Other user text frames stay Unknown.
func TestParseUserTextIsNotStopHook(t *testing.T) {
	p := NewClaudeParser()
	ev, err := p.Parse(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"a plain note"}]}}`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ev.Type != Unknown {
		t.Fatalf("got %v, want Unknown", ev.Type)
	}
}
