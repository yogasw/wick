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

func TestShapeOneMessage_BotRichTextRenderedInFull(t *testing.T) {
	msg := map[string]any{
		"bot_id": "B01TESTBOT",
		"text":   "Intro line. Found: - item…",
		"blocks": []any{map[string]any{"type": "rich_text", "elements": []any{
			map[string]any{"type": "rich_text_section", "elements": []any{
				map[string]any{"type": "text", "text": "Intro "},
				map[string]any{"type": "text", "text": "acme", "style": map[string]any{"code": true}},
				map[string]any{"type": "text", "text": " line.\n\nFound:"},
			}},
			map[string]any{"type": "rich_text_list", "style": "bullet", "indent": float64(0), "elements": []any{
				map[string]any{"type": "rich_text_section", "elements": []any{map[string]any{"type": "text", "text": "first"}}},
			}},
			map[string]any{"type": "rich_text_list", "style": "ordered", "indent": float64(1), "elements": []any{
				map[string]any{"type": "rich_text_section", "elements": []any{
					map[string]any{"type": "text", "text": "bold", "style": map[string]any{"bold": true}},
					map[string]any{"type": "text", "text": " see "},
					map[string]any{"type": "link", "url": "https://example.com", "text": "docs"},
					map[string]any{"type": "user", "user_id": "U01TESTUSER"},
				}},
			}},
		}}},
	}
	want := "Intro `acme` line.\n\nFound:\n• first\n  1. *bold* see <https://example.com|docs><@U01TESTUSER>"
	if got := shapeOneMessage(msg)["text"]; got != want {
		t.Fatalf("text = %q\nwant %q", got, want)
	}
}

func TestShapeOneMessage_BotRichTextWithExactFallbackKept(t *testing.T) {
	msg := map[string]any{
		"bot_id": "B01TESTFLOW",
		"text":   "Form posted by _acme_\n\nbody",
		"blocks": []any{map[string]any{"type": "rich_text", "elements": []any{
			map[string]any{"type": "rich_text_section", "elements": []any{map[string]any{"type": "text", "text": "Form posted by "}}},
		}}},
	}
	if got := shapeOneMessage(msg)["text"]; got != "Form posted by _acme_\n\nbody" {
		t.Fatalf("text = %q", got)
	}
}
