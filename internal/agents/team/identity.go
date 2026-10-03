package team

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxTagline is the longest tagline, in characters: a label that sits
// beside a name, not a sentence.
const MaxTagline = 32

// NormalizeTagline trims a tagline and refuses one longer than
// MaxTagline. "" is valid (no tagline).
func NormalizeTagline(s string) (string, error) {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > MaxTagline {
		return "", fmt.Errorf("tagline must be at most %d characters", MaxTagline)
	}
	return s, nil
}

// maxTeamListed bounds the Captain's "Your Team" line so a big roster
// does not grow every spawn's prompt; the rest is counted, not named.
const maxTeamListed = 12

// Member is one Team agent as the identity block names it. Name and
// Description come from the agent's project, Handle from its row; Tagline
// is the short one-liner when the agent has one.
type Member struct {
	Name, Handle, Tagline, Description string
	IsCaptain                          bool
}

// WhoYouAre is the dynamic "Who you are" block of a Team agent's prompt,
// built at spawn from the agent's row so a renamed agent never answers to
// an old name its persona may still carry. team is every OTHER agent of
// the owner. Only the Captain, which coordinates, gets the roster; any
// other agent is told who its Captain is and nothing more.
func WhoYouAre(self Member, team []Member) string {
	var b strings.Builder
	b.WriteString("## Who you are\n")
	fmt.Fprintf(&b, "Your name is %s. People and other agents call you @%s (that is how you are mentioned).", self.Name, self.Handle)
	if d := strings.TrimSpace(self.Description); d != "" {
		fmt.Fprintf(&b, " Your role: %s.", strings.TrimRight(d, ". "))
	}
	if t := strings.TrimSpace(self.Tagline); t != "" {
		fmt.Fprintf(&b, " People know you as %s, %s.", self.Name, strings.TrimRight(t, ". "))
	}
	if self.IsCaptain {
		b.WriteString(" You are the Captain — the owner's main agent.")
	}
	b.WriteString("\n")
	if self.IsCaptain {
		if line := teamLine(team); line != "" {
			b.WriteString("Your Team: " + line + "\n")
			b.WriteString("Reach a member with team_message (to: \"@handle\"), e.g. team_message to \"@" + team[0].Handle + "\".\n")
		}
	} else {
		for _, m := range team {
			if m.IsCaptain {
				fmt.Fprintf(&b, "Your Captain is %s (@%s); the Captain coordinates the Team. Reach it with team_message (to: \"@%s\").\n", m.Name, m.Handle, m.Handle)
				break
			}
		}
	}
	b.WriteString("If your persona below names you differently, the name and handle above are the current ones.")
	return b.String()
}

// teamLine names up to maxTeamListed agents with their tagline and
// description, counting the rest.
func teamLine(team []Member) string {
	parts := make([]string, 0, maxTeamListed)
	for i, m := range team {
		if i == maxTeamListed {
			break
		}
		s := fmt.Sprintf("%s (@%s)", m.Name, m.Handle)
		if t := strings.TrimSpace(m.Tagline); t != "" {
			s += ", " + t
		}
		if d := strings.TrimSpace(m.Description); d != "" {
			s += " — " + d
		}
		parts = append(parts, s)
	}
	line := strings.Join(parts, "; ")
	if extra := len(team) - maxTeamListed; extra > 0 {
		line += fmt.Sprintf("; +%d more", extra)
	}
	return line
}

// SubAgentOfTeam is the one line a sub-agent delegated from a Team
// agent's session gets instead of the Team blocks.
func SubAgentOfTeam(handle string) string {
	return fmt.Sprintf("You are a temporary sub-agent working for @%s; you are not a Team member.", handle)
}
