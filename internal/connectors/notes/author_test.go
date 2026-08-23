package notes

import (
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

// TestNoteAuthor_UsesTheRealCaller pins who a note is attributed to.
//
// Notes written through an agent used to be stamped with the literal "agent",
// which claimed an identity nobody has: the same note added from the web UI
// showed a person's name, so one conversation could show "Admin" and "agent"
// side by side for what was actually the same kind of act. Worse, once two
// people share a conversation there was no way to tell their notes apart.
//
// The stored value is a USER ID, not a name, so the UI resolves it to the
// current name and a rename shows up on old notes.
func TestNoteAuthor_UsesTheRealCaller(t *testing.T) {
	c := &connector.Ctx{}
	c.SetCallerUserID("user-ada")

	if got := noteAuthor(c); got != "user-ada" {
		t.Fatalf("author = %q, want the caller's user id", got)
	}
}

// TestNoteAuthor_UnknownWhenNoHuman covers calls with nobody behind them — a
// cron run, a system job, a session created before ownership tracking.
//
// "unknown" is deliberate: the old "agent" named an actor that does not exist,
// which reads as though some specific thing wrote the note. Saying we cannot
// name the author is more honest than naming the wrong one.
func TestNoteAuthor_UnknownWhenNoHuman(t *testing.T) {
	if got := noteAuthor(&connector.Ctx{}); got != authorUnknown {
		t.Fatalf("author = %q, want %q", got, authorUnknown)
	}

	// Whitespace is not an identity either.
	c := &connector.Ctx{}
	c.SetCallerUserID("   ")
	if got := noteAuthor(c); got != authorUnknown {
		t.Fatalf("blank caller gave %q, want %q", got, authorUnknown)
	}
}

// TestAuthorUnknown_IsNotAUserID keeps the sentinel from ever colliding with a
// real id: user ids are uuids, so a lookup for this value must always miss and
// fall through to the UI's plain-text rendering.
func TestAuthorUnknown_IsNotAUserID(t *testing.T) {
	if len(authorUnknown) >= 36 {
		t.Fatalf("sentinel %q is uuid-length; it could shadow a real user id", authorUnknown)
	}
}
