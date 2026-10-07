package event

import "testing"

// A frame whose .message is a bare STRING must decode instead of turning
// the whole line into a parse error. Regression for:
//   claude parse: json: cannot unmarshal string into Go struct field
//   claudeRaw.message of type struct { Content json.RawMessage; ... }
func TestParseMessageAsPlainString(t *testing.T) {
	p := NewClaudeParser()
	for _, line := range []string{
		`{"type":"assistant","message":"Invalid API key · Please run /login"}`,
		`{"type":"user","message":""}`,
		`{"type":"system","subtype":"notice","message":"context low"}`,
	} {
		if _, err := p.Parse(line); err != nil {
			t.Fatalf("Parse(%s) returned error: %v", line, err)
		}
	}
}

// The object form must keep working, including .content as a string.
func TestParseMessageObjectStillWorks(t *testing.T) {
	p := NewClaudeParser()
	ev, err := p.Parse(`{"type":"assistant","message":{"content":[{"type":"text","text":"halo"}]}}`)
	if err != nil {
		t.Fatalf("object form failed: %v", err)
	}
	if ev.Text != "halo" {
		t.Fatalf("want text %q, got %q", "halo", ev.Text)
	}
	if _, err := p.Parse(`{"type":"user","message":{"content":"plain string content"}}`); err != nil {
		t.Fatalf("content-as-string form failed: %v", err)
	}
}
