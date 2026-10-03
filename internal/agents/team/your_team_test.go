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
