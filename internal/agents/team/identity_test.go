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
	got := WhoYouAre(Member{Name: "X", Handle: "x", IsCaptain: true}, team)
	if strings.Count(got, "(@a)") != maxTeamListed || !strings.Contains(got, "+3 more") {
		t.Fatalf("cap wrong:\n%s", got)
	}
}

func TestWhoYouAreNonCaptainGetsOnlyCaptain(t *testing.T) {
	team := []Member{{Name: "Ops", Handle: "ops", Description: "runs ops"}, {Name: "Cap", Handle: "cap", IsCaptain: true, Description: "lead"}}
	got := WhoYouAre(Member{Name: "Bob", Handle: "bob"}, team)
	if !strings.Contains(got, "Your Captain is Cap (@cap); the Captain coordinates the Team.") {
		t.Fatalf("captain line missing:\n%s", got)
	}
	if strings.Contains(got, "Your Team") || strings.Contains(got, "@ops") || strings.Contains(got, "lead") {
		t.Fatalf("non-captain got the roster:\n%s", got)
	}
	// No Captain in the Team: no line at all.
	if got := WhoYouAre(Member{Name: "Bob", Handle: "bob"}, team[:1]); strings.Contains(got, "Captain") {
		t.Fatalf("unexpected captain line:\n%s", got)
	}
}

func TestWhoYouAreCaptainRosterHasTagline(t *testing.T) {
	got := WhoYouAre(Member{Name: "Cap", Handle: "cap", IsCaptain: true}, []Member{{Name: "Ops", Handle: "ops", Tagline: "keeps it up", Description: "runs ops"}})
	if !strings.Contains(got, "Your Team: Ops (@ops), keeps it up — runs ops") {
		t.Fatalf("roster wrong:\n%s", got)
	}
}

func TestSubAgentOfTeam(t *testing.T) {
	if got := SubAgentOfTeam("ops"); got != "You are a temporary sub-agent working for @ops; you are not a Team member." {
		t.Fatal(got)
	}
}
