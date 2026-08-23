package slack

import (
	"strings"
	"testing"

	slackgo "github.com/slack-go/slack"
)

func member(id, email string) slackgo.User {
	u := slackgo.User{ID: id}
	u.Profile.Email = email
	return u
}

// TestCountMemberEmails ignores accounts that legitimately have no email. Left
// in, they would make a correctly-scoped workspace look broken.
func TestCountMemberEmails(t *testing.T) {
	bot := slackgo.User{ID: "B1", IsBot: true}
	gone := slackgo.User{ID: "U9", Deleted: true}
	slackbot := slackgo.User{ID: "USLACKBOT"}

	humans, withEmail := countMemberEmails([]slackgo.User{
		member("U1", "a@example.com"),
		member("U2", ""),
		bot, gone, slackbot,
	})
	if humans != 2 {
		t.Errorf("humans = %d, want 2 (bot/deleted/slackbot excluded)", humans)
	}
	if withEmail != 1 {
		t.Errorf("withEmail = %d, want 1", withEmail)
	}

	// Whitespace is not an email.
	if _, w := countMemberEmails([]slackgo.User{member("U1", "   ")}); w != 0 {
		t.Errorf("whitespace counted as an email: %d", w)
	}
}

// TestEmailScopeVerdict pins the distinction the probe exists for.
//
// The scope failure is SILENT — users.info still succeeds without
// users:read.email, it just returns blank emails. So "nobody has an email" is
// the only signal available, and it has to be told apart from "some members
// have no address on file", which is normal.
func TestEmailScopeVerdict(t *testing.T) {
	const name = "users.info (email)"
	const need = "needs scope: users:read.email"

	t.Run("nobody has an email is a scope failure", func(t *testing.T) {
		got := emailScopeVerdict(name, need, 5, 0)
		if got.OK {
			t.Fatal("OK with zero emails; the missing scope would go unreported")
		}
		if !strings.Contains(got.Detail, "users:read.email") {
			t.Errorf("detail must name the scope to add: %q", got.Detail)
		}
	})

	t.Run("some members without an email is not a failure", func(t *testing.T) {
		got := emailScopeVerdict(name, need, 5, 3)
		if !got.OK {
			t.Fatal("partial coverage reported as a failure; operators would learn to ignore this check")
		}
		if !strings.Contains(got.Detail, "3 of 5") {
			t.Errorf("detail should say who is unmatched: %q", got.Detail)
		}
	})

	t.Run("all covered is clean", func(t *testing.T) {
		got := emailScopeVerdict(name, need, 4, 4)
		if !got.OK || got.Error != "" {
			t.Fatalf("full coverage not clean: %+v", got)
		}
	})

	t.Run("no humans is inconclusive not failing", func(t *testing.T) {
		got := emailScopeVerdict(name, need, 0, 0)
		if !got.OK {
			t.Fatal("empty workspace reported as a scope failure the operator cannot act on")
		}
	})
}
