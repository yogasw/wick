package event

import (
	"strings"
	"testing"
)

// parseText runs lines through one parser and returns the text its
// TextDelta events carried, joined.
func parseText(t *testing.T, lines ...string) string {
	t.Helper()
	p := NewClaudeParser()
	var b strings.Builder
	for _, l := range lines {
		ev, err := p.Parse(l)
		if err != nil {
			t.Fatalf("parse %s: %v", l, err)
		}
		if ev.Type == TextDelta {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

const (
	msgStart = `{"type":"stream_event","event":{"type":"message_start"}}`
	doneLine = `{"type":"result","subtype":"success","is_error":false,"result":""}`
)

func delta(text string) string {
	return `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"` + text + `"}}}`
}

func textFrame(text string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}`
}

func TestClaudeStreamedFrameIsNotRepeated(t *testing.T) {
	got := parseText(t, msgStart, delta("Hi there, "), delta("all good."), textFrame("Hi there, all good."), doneLine)
	if got != "Hi there, all good." {
		t.Fatalf("text = %q", got)
	}
}

// The stream broke after its first delta and the reply came whole in the
// frame: the turn must not end on the opening words.
func TestClaudeStreamCutKeepsTheRestOfTheFrame(t *testing.T) {
	got := parseText(t, msgStart, delta("Hi Alex, "), textFrame("Hi Alex, the report is ready."), doneLine)
	if got != "Hi Alex, the report is ready." {
		t.Fatalf("text = %q, want the whole reply", got)
	}
}

// A later message of the turn whose text was never streamed is the reply.
func TestClaudeUnstreamedLaterMessageIsKept(t *testing.T) {
	got := parseText(t,
		msgStart, delta("Checking."), textFrame("Checking."),
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		msgStart, textFrame("Done, it works."),
		doneLine)
	if got != "Checking.Done, it works." {
		t.Fatalf("text = %q", got)
	}
}

// A request retried from scratch says it differently: both stay, nothing
// is lost.
func TestClaudeRetriedFrameIsKeptWhole(t *testing.T) {
	got := parseText(t, msgStart, delta("Hello, "), textFrame("Good morning, all set."), doneLine)
	if got != "Hello, \n\nGood morning, all set." {
		t.Fatalf("text = %q", got)
	}
}

func TestUnstreamedText(t *testing.T) {
	cases := []struct{ frame, streamed, want string }{
		{"abc", "", "abc"},
		{"abc", "abc", ""},
		{"ab", "abc", ""},
		{"abcdef", "abc", "def"},
		{"xyz", "abc", "\n\nxyz"},
	}
	for _, c := range cases {
		if got := unstreamedText(c.frame, c.streamed); got != c.want {
			t.Errorf("unstreamedText(%q, %q) = %q, want %q", c.frame, c.streamed, got, c.want)
		}
	}
}
