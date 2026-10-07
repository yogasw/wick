package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/store"
)

func TestParseNumberReply(t *testing.T) {
	for in, want := range map[string]int{"2": 2, " 1. ": 1, "12": 12} {
		if n, ok := ParseNumberReply(in); !ok || n != want {
			t.Errorf("%q = %d,%v", in, n, ok)
		}
	}
	for _, in := range []string{"", "0", "two", "1 please", "123"} {
		if _, ok := ParseNumberReply(in); ok {
			t.Errorf("%q parsed as a choice", in)
		}
	}
}

func TestNumberedCardIsTheLastOpenOne(t *testing.T) {
	card := func(id string, final bool) string {
		f := ""
		if final {
			f = `,"final":true`
		}
		return "```actioncard\n{\"id\":\"" + id + "\",\"title\":\"t\",\"actions\":[{\"label\":\"A\",\"value\":\"a\"}]" + f + "}\n```"
	}
	turns := []store.ConversationTurn{
		{TurnID: "1", Role: "assistant", Text: card("old", false)},
		{TurnID: "2", Role: "assistant", Text: card("new", false)},
		{TurnID: "3", Role: "assistant", Text: card("done", true)},
	}
	if st := numberedCard(turns); st == nil || st.card.ID != "new" {
		t.Fatalf("numbered card = %+v", st)
	}
	turns = append(turns, store.ConversationTurn{TurnID: "4", Role: "user", Postback: &store.Postback{CardID: "new", Value: "a"}})
	if st := numberedCard(turns); st == nil || st.card.ID != "old" {
		t.Fatalf("after clicking new = %+v", st)
	}
}
