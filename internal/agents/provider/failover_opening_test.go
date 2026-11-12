package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

const openingBlock = "[Slack thread context — injected by wick]\nChannel: #ops [C01TESTCHAN]\nThread: 1700000000.000100"

// A fresh target gets the session's opening context again, and the
// continue keeps the person's [from: …] line on top.
func TestContinueMessageFreshTargetKeepsOpeningAndSender(t *testing.T) {
	in := ContinueInput{From: "claude/a", To: "claude/b", Reason: event.ErrLimit,
		UserMsg: "[from: Dev Example (u1)]\nplease send the list", Produced: true,
		PartialText: "Account format: op@id.", Opening: openingBlock}
	got := ContinueMessage(in)
	if !strings.HasPrefix(got, "[from: Dev Example (u1)]\n[wick] The previous provider") {
		t.Fatalf("sender line not on top:\n%s", got)
	}
	for _, want := range []string{"This session opened with:\n" + openingBlock, `Last user message: """[from: Dev Example (u1)]`, "did not carry over"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}

	// Nothing produced: the user's message unchanged, then the opening.
	in.Produced = false
	got = ContinueMessage(in)
	if !strings.HasPrefix(got, in.UserMsg+"\n\n[wick] You are now on claude/b.") || !strings.Contains(got, openingBlock) {
		t.Fatalf("not produced:\n%s", got)
	}

	// History carries: the target saw the opening, none is repeated.
	in.HistoryCarries, in.Produced = true, true
	if got = ContinueMessage(in); strings.Contains(got, "This session opened with") {
		t.Fatalf("carried history repeats the opening:\n%s", got)
	}
}

func TestOpeningContextReadsFirstInjectedBlock(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(layout.SessionConversation("s1")), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, turn := range []store.ConversationTurn{
		{Role: "system", Text: openingBlock + "\n\nWorkspace members (first 2): @a (U1) | @b (U2)"},
		{Role: "user", Text: "[from: Dev Example (u1)]\nhello"},
		{Role: "system", Text: "[Other context — injected by wick]\nlater"},
	} {
		if err := storage.AppendJSONL(layout.SessionConversation("s1"), "wick-conv-v1", "s1", turn); err != nil {
			t.Fatal(err)
		}
	}
	if got := OpeningContext(layout, "s1"); got != openingBlock {
		t.Fatalf("OpeningContext = %q", got)
	}
	if got := OpeningContext(layout, "missing"); got != "" {
		t.Fatalf("missing session = %q", got)
	}
}

func TestSenderLine(t *testing.T) {
	for in, want := range map[string]string{
		"[from: A (u1)]\nhi":     "[from: A (u1)]",
		"\n  [from: A (u1)]\nhi": "[from: A (u1)]",
		"hi [from: A]":           "",
		"[from: unclosed\nhi":    "",
	} {
		if got := senderLine(in); got != want {
			t.Errorf("senderLine(%q) = %q, want %q", in, got, want)
		}
	}
}
