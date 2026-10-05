package team

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/teamlink"
)

func TestYourTeamBlock(t *testing.T) {
	if YourTeam(nil) != "" {
		t.Fatal("empty team must render nothing")
	}
	got := YourTeam([]Member{
		{Name: "Cap", Handle: "cap", IsCaptain: true},
		{Name: "Logs", Handle: "logs", Tagline: "log hunter", MentionFrom: teamlink.MentionCaptain},
	})
	for _, want := range []string{"## Your team", "- @cap — Cap (Captain); takes mentions from anyone", "- @logs — Logs, log hunter; takes mentions from the Captain only"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	var many []Member
	for i := 0; i < maxTeamListed+3; i++ {
		many = append(many, Member{Name: fmt.Sprint(i), Handle: fmt.Sprintf("a%d", i)})
	}
	got = YourTeam(many)
	if strings.Count(got, "\n- @") != maxTeamListed || !strings.Contains(got, "- +3 more") {
		t.Fatalf("cap not applied:\n%s", got)
	}
	// Stays well under the Team overlay budget (~600 tokens ≈ 2400 bytes).
	if len(got) > 2400 {
		t.Fatalf("Your team block is %d bytes", len(got))
	}
}

// A remote member set to "Nobody" says plainly that agents are refused
// and how the owner lifts it; a remote one open to agents says it is
// remote and still names its policy.
func TestYourTeamRemoteMember(t *testing.T) {
	got := YourTeam([]Member{
		{Name: "Halodev", Handle: "halodev", Tagline: "spesialis product", MentionFrom: "off", Remote: true},
		{Name: "Research", Handle: "research", MentionFrom: "all", Remote: true},
	})
	for _, want := range []string{
		"- @halodev — Halodev, spesialis product; remote agent; only the owner may use it",
		"Settings › Mention",
		"- @research — Research; remote agent (gets only your message text); takes mentions from anyone",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("YourTeam missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "halodev — Halodev, spesialis product; takes mentions from anyone") {
		t.Fatalf("owner-only remote advertised as open:\n%s", got)
	}
}

func TestSharedWithOwnerBlock(t *testing.T) {
	if SharedWithOwner(nil) != "" {
		t.Fatal("no shared agents must add no block")
	}
	got := SharedWithOwner([]Member{{Name: "Ops", Handle: "ops", Tagline: "on-call"}})
	for _, want := range []string{"## Shared with your owner", "You cannot message them", "- @ops — Ops, on-call"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
