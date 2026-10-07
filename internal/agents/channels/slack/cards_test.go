package slack

import (
	"strings"
	"testing"

	slackgo "github.com/slack-go/slack"

	"github.com/yogasw/wick/internal/agents/actioncard"
)

func TestCardBlocksAndValue(t *testing.T) {
	c := actioncard.Card{ID: "cap-1", Title: "New agent", Subtitle: "needs you", Status: "waiting",
		Rows:    [][]string{{"Persona", "Daily recap"}},
		Actions: []actioncard.Button{{Label: "Approve", Value: "ok|go", Style: "primary"}, {Label: "Reject", Value: "no", Style: "ghost"}}}
	blocks := cardBlocks("slack-s1", c)
	if len(blocks) != 4 {
		t.Fatalf("blocks = %d", len(blocks))
	}
	ab, ok := blocks[3].(*slackgo.ActionBlock)
	if !ok || ab.BlockID != cardBlockID || len(ab.Elements.ElementSet) != 2 {
		t.Fatalf("actions block = %+v", blocks[3])
	}
	b := ab.Elements.ElementSet[0].(*slackgo.ButtonBlockElement)
	if b.Style != slackgo.StylePrimary || b.ActionID != "actioncard:0" {
		t.Fatalf("button = %+v", b)
	}
	sid, cid, v, ok := parseCardValue(b.Value)
	if !ok || sid != "slack-s1" || cid != "cap-1" || v != "ok|go" {
		t.Fatalf("value round trip = %q %q %q %v", sid, cid, v, ok)
	}
	if _, _, _, ok := parseCardValue("approve|x|y|z"); !ok {
		t.Fatal("a value with | must still parse")
	}
	if _, _, _, ok := parseCardValue("only|two"); ok {
		t.Fatal("malformed value parsed")
	}

	c.Final = true
	for _, bl := range cardBlocks("s", c) {
		if _, isAct := bl.(*slackgo.ActionBlock); isAct {
			t.Fatal("a final card has buttons")
		}
	}
}

func TestDecidedBlocksLockTheCard(t *testing.T) {
	c := actioncard.Card{ID: "c", Title: "T", Actions: []actioncard.Button{{Label: "A", Value: "a"}}}
	got := decidedBlocks(cardBlocks("s", c), "A", "U1")
	last, ok := got[len(got)-1].(*slackgo.ContextBlock)
	if !ok {
		t.Fatalf("last block = %T", got[len(got)-1])
	}
	txt := last.ContextElements.Elements[0].(*slackgo.TextBlockObject).Text
	if !strings.Contains(txt, "✓ A") || !strings.Contains(txt, "<@U1>") {
		t.Fatalf("decided line = %q", txt)
	}
	for _, b := range got {
		if _, isAct := b.(*slackgo.ActionBlock); isAct {
			t.Fatal("buttons left after a click")
		}
	}
	if !strings.HasPrefix(cardPointer(c), "_T") {
		t.Fatal("pointer")
	}
}
