package actioncard

import (
	"strings"
	"testing"
)

const reply = "Here is the plan.\n```actioncard\n{\"id\":\"cap-1\",\"title\":\"New agent\",\"status\":\"waiting\",\"rows\":[[\"Persona\",\"Daily recap\"]],\"actions\":[{\"label\":\"Approve\",\"value\":\"approve\",\"style\":\"primary\"},{\"label\":\"Reject\",\"value\":\"reject\"}]}\n```\nThanks.\n```actioncard\n{not json}\n```"

func TestSplitReplacesOnlyCards(t *testing.T) {
	text, cards := Split(reply, func(c Card) string { return "<" + c.ID + ">" })
	if len(cards) != 1 || cards[0].ID != "cap-1" || len(cards[0].Actions) != 2 {
		t.Fatalf("cards = %+v", cards)
	}
	if !strings.Contains(text, "Here is the plan.\n<cap-1>\nThanks.") || !strings.Contains(text, "{not json}") {
		t.Fatalf("text = %q", text)
	}
	if kept, _ := Split(reply, nil); kept != reply {
		t.Fatal("nil render must keep the text as written")
	}
	if got := Parse("```actioncard\n{\"id\":\"x\"}"); len(got) != 0 {
		t.Fatal("unterminated fence is not a card")
	}
}

func TestPlainTextNumbersActions(t *testing.T) {
	got := PlainText(Parse(reply)[0])
	for _, want := range []string{"*New agent* [waiting]", "• Persona: Daily recap", "1. Approve", "2. Reject", "Reply with the number."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	c := Parse(reply)[0]
	c.Final = true
	if strings.Contains(PlainText(c), "1. Approve") {
		t.Error("a final card offers no choices")
	}
}
