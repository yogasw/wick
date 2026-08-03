package delegation

import "testing"

func TestParseMentionsAcceptsLineLeadingKnownHandles(t *testing.T) {
	roster := []string{"main", "reviewer"}
	got := ParseMentions("@reviewer can you look at auth.go?\nthanks", roster)
	if len(got) != 1 {
		t.Fatalf("want 1 mention, got %d: %+v", len(got), got)
	}
	if got[0].Handle != "reviewer" {
		t.Fatalf("handle = %q", got[0].Handle)
	}
	if got[0].Body != "can you look at auth.go?" {
		t.Fatalf("body = %q", got[0].Body)
	}
}

// Every entry here appears in ordinary agent output. A false positive is
// not cosmetic: it spawns an agent and spends tokens because the model
// happened to write an email address.
func TestParseMentionsIgnoresLookalikes(t *testing.T) {
	// No "media" here on purpose: handles come from profile keys, and the
	// at-rule is only safe while nothing in the tree is called media. The
	// case where one is is pinned separately below.
	roster := []string{"main", "reviewer"}
	cases := map[string]string{
		"css at-rule":    "@media (min-width: 40rem) { .a { color: red } }",
		"email":          "ping ops@abc.com about it",
		"npm scope":      "@scope/pkg is the dependency",
		"mid-line":       "I told @reviewer already",
		"unknown handle": "@nobody are you there?",
		"inside a fence": "```\n@reviewer look here\n```",
		"decorator":      "@ts-ignore",
		"bare handle":    "@reviewer",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ParseMentions(in, roster); len(got) != 0 {
				t.Fatalf("false positive on %s: %+v", name, got)
			}
		})
	}
}

// "@media (min-width…)" is only safe because "media" is not in the roster
// in real trees; when a role IS called media, the at-rule and the mention
// are genuinely ambiguous and the roster entry wins. Pinned so the
// trade-off is visible rather than discovered.
func TestParseMentionsPrefersTheRosterOnAmbiguity(t *testing.T) {
	got := ParseMentions("@media check the responsive layout", []string{"media"})
	if len(got) != 1 || got[0].Handle != "media" {
		t.Fatalf("a real roster handle must win: %+v", got)
	}
}

func TestParseMentionsFindsSeveralInOneText(t *testing.T) {
	got := ParseMentions("@reviewer check auth.go\n@researcher find the changelog", []string{"reviewer", "researcher"})
	if len(got) != 2 {
		t.Fatalf("want 2 mentions, got %d: %+v", len(got), got)
	}
}
