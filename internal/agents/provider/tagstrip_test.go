package provider

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
)

func feedAll(t *testing.T, pieces []string) string {
	t.Helper()
	ts := newTagStripper(nil)
	var b strings.Builder
	for _, p := range pieces {
		b.WriteString(ts.feed(p))
	}
	b.WriteString(ts.flush())
	return b.String()
}

func TestTagStripperSplitAcrossDeltas(t *testing.T) {
	in := "Checking now:\n\n<system-reminder>\nToday: 2026-01-02\n</system-reminder>\n\nDone <b>ok</b> a<b"
	want := "Checking now:\n\nDone <b>ok</b> a<b"
	// every split size, down to one byte per delta
	for size := 1; size <= len(in); size++ {
		var pieces []string
		for i := 0; i < len(in); i += size {
			j := i + size
			if j > len(in) {
				j = len(in)
			}
			pieces = append(pieces, in[i:j])
		}
		if got := feedAll(t, pieces); got != want {
			t.Fatalf("size %d: got %q, want %q", size, got, want)
		}
	}
}

func TestTagStripperUnclosedTagIsReleased(t *testing.T) {
	if got := feedAll(t, []string{"a <system-rem", "inder> never closed"}); got != "a <system-reminder> never closed" {
		t.Fatalf("got %q", got)
	}
}

func TestStripStreamFlushesBeforeToolUse(t *testing.T) {
	ts := newTagStripper(nil)
	got := ts.stripStream([]event.AgentEvent{
		{Type: event.TextDelta, Text: "look <sys"},
		{Type: event.ToolUse, ToolName: "Bash"},
	})
	if len(got) != 3 || got[0].Text != "look " || got[1].Text != "<sys" || got[2].Type != event.ToolUse {
		t.Fatalf("unexpected events: %+v", got)
	}
	got = ts.stripStream([]event.AgentEvent{{Type: event.TextDelta, Text: "<system-reminder>x"}})
	if len(got) != 0 {
		t.Fatalf("held text leaked: %+v", got)
	}
	got = ts.stripStream([]event.AgentEvent{{Type: event.TextDelta, Text: "</system-reminder>hi"}})
	if len(got) != 1 || got[0].Text != "hi" {
		t.Fatalf("unexpected events: %+v", got)
	}
}

func TestStripTagList(t *testing.T) {
	got := StripTagList("  <ctx-note>\n\nsystem-reminder\n</extra>\nbad tag\n<a>b</a>\n")
	want := []string{"system-reminder", "system-notice", "ctx-note", "extra"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", got, want)
	}
	if got := StripTagList(""); len(got) != 2 || got[0] != "system-reminder" || got[1] != "system-notice" {
		t.Fatalf("default: got %v", got)
	}
}

func TestTagStripperConfiguredTag(t *testing.T) {
	ts := newTagStripper(StripTagList("ctx-note"))
	out := ts.feed("a<ctx-") + ts.feed("note>hidden</ctx-no") + ts.feed("te>b<system-reminder>x</system-reminder>c") + ts.flush()
	if out != "abc" {
		t.Fatalf("got %q", out)
	}
}

func TestTagStripperDropsPaddingAfterTag(t *testing.T) {
	in := "Checking:\n\n<system-reminder>\nToday: 2026-01-02\n</system-reminder>\n\n\n\n\n\nNext step"
	for size := 1; size <= len(in); size++ {
		var pieces []string
		for i := 0; i < len(in); i += size {
			j := min(i+size, len(in))
			pieces = append(pieces, in[i:j])
		}
		if got := feedAll(t, pieces); got != "Checking:\n\nNext step" {
			t.Fatalf("size %d: got %q", size, got)
		}
	}
}

func TestStripsSystemNoticeByDefault(t *testing.T) {
	ts := newTagStripper(StripTagList(""))
	out := ts.feed("before <system-") + ts.feed("notice>harness note</system-notice>\n\nafter") + ts.flush()
	if out != "before after" {
		t.Fatalf("got %q", out)
	}
}
