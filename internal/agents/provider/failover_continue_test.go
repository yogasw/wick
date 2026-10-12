package provider

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/store"
)

func TestBriefTools(t *testing.T) {
	events := []store.TurnEvent{
		{Type: "thinking", Text: "hmm"},
		{Type: "tool_use", ToolName: "Bash", ToolUseID: "t1", ToolInput: "ls\n  -la   /tmp"},
		{Type: "tool_result", ToolUseID: "t1", Text: "ok"},
		{Type: "tool_use", ToolName: "Edit", ToolUseID: "t2", ToolInput: strings.Repeat("x", 200)},
		{Type: "tool_result", ToolUseID: "t2", IsError: true},
		{Type: "tool_use", ToolName: "slack_send", ToolUseID: "t3", ToolInput: `{"text":"hi"}`},
		{Type: "text", Text: "partial"},
	}
	got := briefTools(events)
	want := []ToolBrief{
		{Name: "Bash", Input: "ls -la /tmp", Status: ToolOK},
		{Name: "Edit", Input: strings.Repeat("x", ContinueToolInputMax-1) + "…", Status: ToolError},
		{Name: "slack_send", Input: `{"text":"hi"}`, Status: ToolUnknown},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("briefTools =\n%+v\nwant\n%+v", got, want)
	}

	// A result with no id closes the latest tool still waiting.
	got = briefTools([]store.TurnEvent{
		{Type: "tool_use", ToolName: "A"},
		{Type: "tool_use", ToolName: "B"},
		{Type: "tool_result"},
		{Type: "tool_result", ToolUseID: "stray"},
	})
	if got[0].Status != ToolOK || got[1].Status != ToolOK {
		t.Fatalf("id-less results = %+v", got)
	}
	if briefTools(nil) != nil {
		t.Fatal("no events → nil")
	}
}

func TestNewTurnSnapshot(t *testing.T) {
	if s := NewTurnSnapshot("q", "  \n", nil); s.Produced {
		t.Fatal("blank text and no tools counted as produced")
	}
	if s := NewTurnSnapshot("q", "", []store.TurnEvent{{Type: "tool_use", ToolName: "Bash"}}); !s.Produced {
		t.Fatal("an in-flight tool is output")
	}
	a := NewTurnSnapshot("q", "first", []store.TurnEvent{{Type: "tool_use", ToolName: "A"}})
	b := NewTurnSnapshot("[wick] continue", "second", []store.TurnEvent{{Type: "tool_use", ToolName: "B"}})
	c := a.Combine(b)
	if c.UserMsg != "q" || c.Text != "first\n\nsecond" || len(c.Tools) != 2 || !c.Produced {
		t.Fatalf("Combine = %+v", c)
	}
	if e := (TurnSnapshot{UserMsg: "q"}).Combine(NewTurnSnapshot("x", "only", nil)); e.Text != "only" || !e.Produced || e.UserMsg != "q" {
		t.Fatalf("Combine onto empty = %+v", e)
	}
	// The continue text of hop 2 never becomes the user message.
	if e := (TurnSnapshot{}).Combine(NewTurnSnapshot("[wick] continue", "t", nil)); e.UserMsg != "" {
		t.Fatalf("Combine promoted the continue text: %q", e.UserMsg)
	}
}

func TestContinueMessage(t *testing.T) {
	tools := []ToolBrief{
		{Name: "Bash", Input: "ls", Status: ToolOK},
		{Name: "slack_send", Input: "{}", Status: ToolUnknown},
	}
	base := ContinueInput{From: "claude/a", To: "claude/c", Reason: event.ErrLimit, UserMsg: "please deploy"}

	t.Run("nothing produced resends the user message unchanged", func(t *testing.T) {
		for _, same := range []bool{true, false} {
			in := base
			in.HistoryCarries = same
			if got := ContinueMessage(in); got != "please deploy" {
				t.Fatalf("same=%v: %q", same, got)
			}
		}
	})

	t.Run("same type: continue note without the user message", func(t *testing.T) {
		in := base
		in.HistoryCarries, in.Produced, in.PartialText, in.Tools = true, true, "half an answer", tools
		got := ContinueMessage(in)
		for _, want := range []string{
			"[wick] The previous provider (claude/a) stopped mid-turn: usage limit reached.",
			"You are now on claude/c with the same conversation.",
			"Do NOT repeat actions",
			"- Bash(ls) → ok",
			"- slack_send({}) → status unknown — verify before repeating",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in\n%s", want, got)
			}
		}
		for _, bad := range []string{"please deploy", "half an answer", "did not carry over"} {
			if strings.Contains(got, bad) {
				t.Errorf("same-type continue carries %q:\n%s", bad, got)
			}
		}
	})

	t.Run("cross type: user message and tail of the output", func(t *testing.T) {
		in := base
		in.To, in.Reason, in.Produced, in.PartialText = "codex/b", event.ErrAuth, true, "the answer so far"
		got := ContinueMessage(in)
		for _, want := range []string{
			"stopped mid-turn: login failed.",
			"You are now on codex/b. Your conversation history did not carry over.",
			`Last user message: """please deploy"""`,
			`Output already produced this turn (summary): """the answer so far"""`,
			"Tools already run: none.",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("missing %q in\n%s", want, got)
			}
		}
	})

	t.Run("caps", func(t *testing.T) {
		var many []ToolBrief
		for i := 0; i < 25; i++ {
			many = append(many, ToolBrief{Name: "T" + strings.Repeat("x", i%3), Input: strings.Repeat("i", ContinueToolInputMax), Status: ToolOK})
		}
		in := ContinueInput{From: "claude/a", To: "codex/b", Produced: true,
			UserMsg: strings.Repeat("u", 10000), PartialText: strings.Repeat("p ", 5000) + "END", Tools: many}
		got := ContinueMessage(in)
		if n := len([]rune(got)); n > ContinueMax {
			t.Fatalf("len = %d, want ≤ %d", n, ContinueMax)
		}
		if !strings.Contains(got, "(15 earlier tools omitted)") || strings.Count(got, "\n- T") != ContinueToolsMax {
			t.Fatalf("tool list not capped at %d:\n%s", ContinueToolsMax, got)
		}
		if !strings.Contains(got, `END"""`) {
			t.Fatal("the tail of the output must survive the cap")
		}
		if !strings.Contains(got, "Tools already run:") {
			t.Fatal("the tool list must survive the cap")
		}
		_, after, _ := strings.Cut(got, `(summary): """`)
		tail, _, _ := strings.Cut(after, `"""`)
		if n := len([]rune(tail)); n > ContinueTailMax {
			t.Fatalf("tail = %d chars, want ≤ %d", n, ContinueTailMax)
		}

		in.HistoryCarries = true
		if n := len([]rune(ContinueMessage(in))); n > ContinueMax {
			t.Fatalf("same-type len = %d", n)
		}
	})

	t.Run("deterministic", func(t *testing.T) {
		in := base
		in.Produced, in.PartialText, in.Tools = true, "x", tools
		if ContinueMessage(in) != ContinueMessage(in) {
			t.Fatal("not deterministic")
		}
	})
}

func TestContinueInputFrom(t *testing.T) {
	snap := TurnSnapshot{UserMsg: "q", Text: "t", Produced: true, Tools: []ToolBrief{{Name: "A"}}}
	in := ContinueInputFrom(SwitchResult{From: "claude/a", To: "claude/c", HistoryCarries: true}, event.ErrLimit, time.Time{}, snap)
	if !in.HistoryCarries || in.From != "claude/a" || in.To != "claude/c" || in.UserMsg != "q" || in.PartialText != "t" || !in.Produced || len(in.Tools) != 1 {
		t.Fatalf("carried = %+v", in)
	}
	// H1: same type but the history did not carry → the continue recaps.
	in = ContinueInputFrom(SwitchResult{From: "claude/a", To: "claude/c"}, event.ErrLimit, time.Time{}, snap)
	if in.HistoryCarries {
		t.Fatal("same type reported as carrying history")
	}
	if msg := ContinueMessage(in); !strings.Contains(msg, `Last user message: """q"""`) || !strings.Contains(msg, "did not carry over") {
		t.Fatalf("fresh same-type target got no recap:\n%s", msg)
	}
}

func TestSummarizeTail(t *testing.T) {
	if got := summarizeTail("  short  ", 10); got != "short" {
		t.Fatalf("short = %q", got)
	}
	got := summarizeTail("alpha beta gamma delta", 12)
	if got != "…gamma delta" && got != "…delta" {
		t.Fatalf("tail = %q", got)
	}
	if n := len([]rune(got)); n > 12 {
		t.Fatalf("tail len %d > 12", n)
	}
	if summarizeTail("abc", 0) != "" {
		t.Fatal("max 0")
	}
	if got := summarizeTail(strings.Repeat("é", 50), 10); len([]rune(got)) != 10 || !strings.HasPrefix(got, "…") {
		t.Fatalf("rune cut = %q", got)
	}
}
