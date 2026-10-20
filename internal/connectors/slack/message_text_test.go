package slack

import "testing"

func TestShapeOneMessage_PrefersBlockTextOverCollapsedFallback(t *testing.T) {
	msg := map[string]any{
		"ts":   "1700000000.000100",
		"text": "first paragraph.  second paragraph.",
		"blocks": []any{
			map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "first paragraph.\n\nsecond paragraph."}},
			map[string]any{"type": "context", "elements": []any{map[string]any{"type": "mrkdwn", "text": "Sent using <@U01TESTBOT>"}}},
		},
	}
	if got := shapeOneMessage(msg)["text"]; got != "first paragraph.\n\nsecond paragraph." {
		t.Fatalf("text = %q", got)
	}
}

func TestShapeOneMessage_MarkdownBlockBeatsClippedFallback(t *testing.T) {
	msg := map[string]any{
		"text":   "**title** body…",
		"blocks": []any{map[string]any{"type": "markdown", "text": "**title**\n\nbody that is longer"}},
	}
	if got := shapeOneMessage(msg)["text"]; got != "**title**\n\nbody that is longer" {
		t.Fatalf("text = %q", got)
	}
}

func TestShapeOneMessage_RichTextKeepsTopLevelText(t *testing.T) {
	msg := map[string]any{
		"text":   "typed by a person\n\nsecond line",
		"blocks": []any{map[string]any{"type": "rich_text", "elements": []any{}}},
	}
	if got := shapeOneMessage(msg)["text"]; got != "typed by a person\n\nsecond line" {
		t.Fatalf("text = %q", got)
	}
}
