package store

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/storage"
)

// reopen returns a store over the SAME session, standing in for the resume
// happening in a different process — which is what a handover makes it.
func reopen(t *testing.T, st *Store) (*Store, string) {
	t.Helper()
	return New(Options{Layout: st.layout, SessionID: st.sessionID, AgentName: st.agentName, Now: st.now}), st.sessionID
}

func readTurns(t *testing.T, st *Store) []ConversationTurn {
	t.Helper()
	var out []ConversationTurn
	err := storage.ReadJSONL(st.layout.SessionConversation(st.sessionID), func(line []byte) bool {
		var turn ConversationTurn
		if json.Unmarshal(line, &turn) == nil && turn.Role != "" {
			out = append(out, turn)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// feed writes text through the store the way a provider does, then flushes.
func feed(t *testing.T, st *Store, text string, interrupted bool) {
	t.Helper()
	if _, err := st.Apply(event.AgentEvent{Type: event.TextDelta, Text: text}); err != nil {
		t.Fatal(err)
	}
	if interrupted {
		if err := st.Flush(); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := st.Apply(event.AgentEvent{Type: event.Done}); err != nil {
		t.Fatal(err)
	}
}

// TestResumeDedupe locks the rule that stops a cut-off answer from being read
// back to the user twice.
func TestResumeDedupe(t *testing.T) {
	t.Run("a resumed turn drops what was already delivered", func(t *testing.T) {
		st, _ := newStore(t, "", false)
		feed(t, st, "Daemon sekarang 0.1.151, aturan barunya aktif.", true)
		// The provider replays from the start and adds the rest.
		st2, _ := reopen(t, st)
		feed(t, st2, "Daemon sekarang 0.1.151, aturan barunya aktif. Work, ini buktinya.", false)

		turns := readTurns(t, st2)
		last := turns[len(turns)-1]
		if strings.Contains(last.Text, "Daemon sekarang") {
			t.Fatalf("the replayed opening was written again: %q", last.Text)
		}
		if !strings.Contains(last.Text, "Work, ini buktinya.") {
			t.Fatalf("the continuation was lost: %q", last.Text)
		}
	})

	t.Run("a reworded resume is left alone", func(t *testing.T) {
		// Not a replay: trimming here would eat the start of a genuinely
		// different message, which is worse than the duplicate it prevents.
		st, _ := newStore(t, "", false)
		feed(t, st, "Daemon sekarang 0.1.151.", true)
		st2, _ := reopen(t, st)
		feed(t, st2, "Ternyata begini ceritanya.", false)

		turns := readTurns(t, st2)
		last := turns[len(turns)-1]
		if last.Text != "Ternyata begini ceritanya." {
			t.Fatalf("a fresh message was altered: %q", last.Text)
		}
	})

	t.Run("an identical replay still records the turn", func(t *testing.T) {
		// Equal text means the resume added nothing. Writing an empty turn
		// would make it look as if the agent said nothing at all.
		st, _ := newStore(t, "", false)
		feed(t, st, "same words", true)
		st2, _ := reopen(t, st)
		feed(t, st2, "same words", false)

		turns := readTurns(t, st2)
		if turns[len(turns)-1].Text != "same words" {
			t.Fatalf("identical replay was emptied: %q", turns[len(turns)-1].Text)
		}
	})

	t.Run("a turn after a NON-interrupted one is untouched", func(t *testing.T) {
		st, _ := newStore(t, "", false)
		feed(t, st, "first answer", false)
		st2, _ := reopen(t, st)
		feed(t, st2, "first answer continues", false)

		turns := readTurns(t, st2)
		if turns[len(turns)-1].Text != "first answer continues" {
			t.Fatalf("trimmed after a clean turn: %q", turns[len(turns)-1].Text)
		}
	})
}
