package wick

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/store"
)

// A replayed reply carries its quote once, in the same place the live send
// put it: after the sender line, before the person's words and the
// attachment block. The stored text never holds it, so it is not doubled.
func TestTurnToContentReappliesReplyQuote(t *testing.T) {
	turn := store.ConversationTurn{
		Role:        "user",
		Text:        "roll it back",
		Sender:      &store.Sender{ID: "u1", Name: "Rina", Channel: "ui"},
		ReplyTo:     &store.ReplyTo{TurnID: "111", Author: "@ops", Excerpt: "deploy finished"},
		Attachments: []store.Attachment{{Name: "log.txt", AbsPath: "/tmp/x/log.txt", MIME: "text/plain"}},
	}
	c := turnToContent(turn, store.SenderName)
	if c == nil || len(c.Parts) == 0 {
		t.Fatalf("no content: %+v", c)
	}
	text := c.Parts[0].Text
	quote := `> Replying to @ops: "deploy finished"`
	if strings.Count(text, quote) != 1 {
		t.Fatalf("quote not exactly once: %q", text)
	}
	from, q, words, atts := strings.Index(text, "[from: Rina"), strings.Index(text, quote), strings.Index(text, "roll it back"), strings.Index(text, "[Attached files]")
	if from < 0 || !(from < q && q < words && words < atts) {
		t.Fatalf("order sender→quote→text→attachments broken: %q", text)
	}
	// A turn without reply_to replays as before.
	turn.ReplyTo = nil
	if strings.Contains(turnToContent(turn, store.SenderName).Parts[0].Text, "Replying to") {
		t.Fatal("old turn grew a quote")
	}
}
