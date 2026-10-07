package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPostbackText(t *testing.T) {
	p := Postback{CardID: "cap-1", Value: "approve", Label: "Approve"}
	if got, want := p.PostbackText(), "[postback card=cap-1 value=approve] Approve"; got != want {
		t.Fatalf("PostbackText = %q, want %q", got, want)
	}
	if got := (Postback{CardID: "c", Value: "v"}).PostbackText(); got != "[postback card=c value=v]" {
		t.Fatalf("no label: %q", got)
	}
}

func TestPostbackContext(t *testing.T) {
	if PostbackFrom(context.Background()) != nil {
		t.Fatal("bare ctx carries no postback")
	}
	p := &Postback{CardID: "c", Value: "v"}
	if got := PostbackFrom(WithPostback(context.Background(), p)); got != p {
		t.Fatalf("PostbackFrom = %v", got)
	}
}

// An ordinary turn must not grow speaker/postback keys on disk.
func TestTurnOmitsEmptySpeakerAndPostback(t *testing.T) {
	b, _ := json.Marshal(ConversationTurn{Role: "user", Text: "hi"})
	if s := string(b); strings.Contains(s, "speaker") || strings.Contains(s, "postback") {
		t.Fatalf("unexpected keys: %s", s)
	}
}
