package team

import (
	"fmt"
	"strings"
)

// maxTeamListed bounds the "Your Team" line so a big roster does not
// grow every spawn's prompt; the rest is counted, not named.
const maxTeamListed = 12

// Member is one Team agent as the identity block names it. Name and
// Description come from the agent's project, Handle from its row.
type Member struct {
	Name, Handle, Description string
	IsCaptain                 bool
}

// WhoYouAre is the dynamic "Who you are" block of a Team agent's prompt,
// built at spawn from the agent's row so a renamed agent never answers to
// an old name its persona may still carry. team is every OTHER agent of
// the owner, Captain first is fine but not required.
func WhoYouAre(self Member, team []Member) string {
	var b strings.Builder
	b.WriteString("## Who you are\n")
	fmt.Fprintf(&b, "Your name is %s. People and other agents call you @%s (that is how you are mentioned).", self.Name, self.Handle)
	if d := strings.TrimSpace(self.Description); d != "" {
		fmt.Fprintf(&b, " Your role: %s.", strings.TrimRight(d, ". "))
	}
	if self.IsCaptain {
		b.WriteString(" You are the Captain — the owner's main agent.")
	}
	b.WriteString("\n")
	if len(team) > 0 {
		parts := make([]string, 0, maxTeamListed)
		for i, m := range team {
			if i == maxTeamListed {
				break
			}
			s := fmt.Sprintf("%s (@%s)", m.Name, m.Handle)
			if d := strings.TrimSpace(m.Description); d != "" {
				s += " — " + d
			}
			parts = append(parts, s)
		}
		line := strings.Join(parts, "; ")
		if extra := len(team) - maxTeamListed; extra > 0 {
			line += fmt.Sprintf("; +%d more", extra)
		}
		b.WriteString("Your Team: " + line + "\n")
	}
	b.WriteString("If your persona below names you differently, the name and handle above are the current ones.")
	return b.String()
}

// SubAgentOfTeam is the one line a sub-agent delegated from a Team
// agent's session gets instead of the Team blocks.
func SubAgentOfTeam(handle string) string {
	return fmt.Sprintf("You are a temporary sub-agent working for @%s; you are not a Team member.", handle)
}
