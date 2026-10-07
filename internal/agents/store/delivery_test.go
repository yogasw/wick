package store

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

func TestDeliveryRoundTripAndStamp(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	const sid = "s1"

	if got := LoadDeliveries(layout, sid); len(got) != 0 {
		t.Fatalf("no file yet, got %v", got)
	}
	if LastAssistantTurnID(layout, sid) != "" {
		t.Fatal("no conversation yet, want no turn")
	}
	conv := layout.SessionConversation(sid)
	for _, turn := range []ConversationTurn{
		{TurnID: "u1", Role: "user", Text: "hi"},
		{TurnID: "a1", Role: "assistant", Text: "first"},
		{TurnID: "u2", Role: "user", Text: "again"},
		{TurnID: "a2", Role: "assistant", Text: "second"},
		{TurnID: "x1", Role: "system", Text: "notice"},
	} {
		if err := storage.AppendJSONL(conv, "wick-conv-v1", sid, turn); err != nil {
			t.Fatal(err)
		}
	}
	if got := LastAssistantTurnID(layout, sid); got != "a2" {
		t.Fatalf("LastAssistantTurnID = %q, want a2", got)
	}

	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := SaveDelivery(layout, sid, "a2", Delivery{Channel: "slack", Status: DeliverySending, At: at}); err != nil {
		t.Fatal(err)
	}
	if err := SaveDelivery(layout, sid, "a2", Delivery{Channel: "slack", Status: DeliverySent, Permalink: "https://example.slack.com/x", At: at}); err != nil {
		t.Fatal(err)
	}
	if err := SaveDelivery(layout, sid, "a1", Delivery{Channel: "slack", Status: DeliveryFailed, Error: "not_in_channel", At: at}); err != nil {
		t.Fatal(err)
	}
	if err := SaveDelivery(layout, sid, "", Delivery{}); err == nil {
		t.Fatal("empty turn id must be refused")
	}

	turns := []ConversationTurn{
		{TurnID: "u1", Role: "user"},
		{TurnID: "a1", Role: "assistant"},
		{TurnID: "a2", Role: "assistant"},
		{TurnID: "a3", Role: "assistant"},
	}
	StampDeliveries(turns, LoadDeliveries(layout, sid))
	if turns[0].Delivery != nil || turns[3].Delivery != nil {
		t.Fatalf("unrecorded turns stamped: %+v %+v", turns[0].Delivery, turns[3].Delivery)
	}
	if d := turns[1].Delivery; d == nil || d.Status != DeliveryFailed || d.Error != "not_in_channel" {
		t.Fatalf("a1 = %+v", d)
	}
	if d := turns[2].Delivery; d == nil || d.Status != DeliverySent || d.Permalink == "" {
		t.Fatalf("a2 = %+v (the later save must win)", d)
	}
}
