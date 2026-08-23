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

// The wire value the MODEL reads is a name, not the stored id. An agent given
// "author: fd8dfab2-08c6-…" learns only that two notes have different authors:
// it cannot read the id, cannot mention the person, and cannot match them to
// anyone it has been told about. The web UI resolves these ids for the same
// reason; this is the agent's half of that.
func TestAuthorName_ResolvesTheStoredID(t *testing.T) {
	c := &connector.Ctx{}
	c.SetUserNameResolver(func(id string) (string, bool) {
		if id == "user-ada" {
			return "Ada Lovelace", true
		}
		return "", false
	})

	if got := authorName(c, "user-ada"); got != "Ada Lovelace" {
		t.Fatalf("author = %q, want the resolved name", got)
	}
}

// The two sentinels are not ids and must never be looked up. Both read as
// "unknown user": naming an actor we cannot identify is worse than admitting
// we cannot, and a reader could not tell a fabricated one from a real name.
func TestAuthorName_SentinelsAreNotLookedUp(t *testing.T) {
	c := &connector.Ctx{}
	c.SetUserNameResolver(func(string) (string, bool) {
		t.Fatal("a sentinel must not reach the resolver")
		return "", false
	})

	for _, stored := range []string{authorUnknown, "agent"} {
		if got := authorName(c, stored); got != "unknown user" {
			t.Errorf("authorName(%q) = %q, want %q", stored, got, "unknown user")
		}
	}
}

// An id that resolves to nothing — a deleted user, or no resolver wired at all
// — reports the same way rather than leaking the uuid, which the reader cannot
// use for anything either way.
func TestAuthorName_UnresolvableIDIsNotLeaked(t *testing.T) {
	withResolver := &connector.Ctx{}
	withResolver.SetUserNameResolver(func(string) (string, bool) { return "", false })

	for name, c := range map[string]*connector.Ctx{
		"resolver misses": withResolver,
		"no resolver":     {},
	} {
		got := authorName(c, "user-gone")
		if got != "unknown user" {
			t.Errorf("%s: authorName = %q, want %q", name, got, "unknown user")
		}
		if got == "user-gone" {
			t.Errorf("%s: leaked the raw id", name)
		}
	}
}

// No author stored at all stays empty, so the field is omitted rather than
// asserting an unknown human where there was none recorded.
func TestAuthorName_EmptyStaysEmpty(t *testing.T) {
	if got := authorName(&connector.Ctx{}, ""); got != "" {
		t.Fatalf("authorName(\"\") = %q, want empty", got)
	}
}
