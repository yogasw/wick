package team

import (
	"strings"
	"testing"
)

func TestWhoYouAre(t *testing.T) {
	self := Member{Name: "Captain", Handle: "captain", Description: "Lead agent.", IsCaptain: true}
	got := WhoYouAre(self, []Member{{Name: "Log Hunter", Handle: "log-hunter", Description: "reads logs"}, {Name: "Bob", Handle: "bob"}})
	for _, want := range []string{
		"## Who you are",
		"Your name is Captain. People and other agents call you @captain",
		"Your role: Lead agent.",
		"You are the Captain — the owner's main agent.",
		"Your Team: Log Hunter (@log-hunter) — reads logs; Bob (@bob)",
		"the name and handle above are the current ones",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestWhoYouAreNonCaptainNoRole(t *testing.T) {
	got := WhoYouAre(Member{Name: "Bob", Handle: "bob"}, nil)
	if strings.Contains(got, "Your role") || strings.Contains(got, "Captain") || strings.Contains(got, "Your Team") {
		t.Fatalf("unexpected parts:\n%s", got)
	}
}

func TestWhoYouAreCapsTheTeam(t *testing.T) {
	var team []Member
	for i := 0; i < 15; i++ {
		team = append(team, Member{Name: "A", Handle: "a"})
	}
	got := WhoYouAre(Member{Name: "X", Handle: "x"}, team)
	if strings.Count(got, "(@a)") != maxTeamListed || !strings.Contains(got, "+3 more") {
		t.Fatalf("cap wrong:\n%s", got)
	}
}

func TestSubAgentOfTeam(t *testing.T) {
	if got := SubAgentOfTeam("ops"); got != "You are a temporary sub-agent working for @ops; you are not a Team member." {
		t.Fatal(got)
	}
}
