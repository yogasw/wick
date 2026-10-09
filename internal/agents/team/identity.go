package team

import (
	"github.com/yogasw/wick/internal/agents/teamlink"

	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxTagline is the longest tagline, in characters: a label that sits
// beside a name, not a sentence. Room for a short role phrase such as
// "product specialist and support"; the persona column is
// varchar(64), so it must stay at or under that.
const MaxTagline = 50

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

// CaptainRoleAddon is what the Captain role adds to the agent's own
// persona. It lives here, in the spawn-time "Who you are" block, rather
// than in the persona text, so it follows the role when "Make Captain"
// moves it and the owner's persona is never overwritten.
const CaptainRoleAddon = "Help the owner run their Team — who handles what — " +
	"and handle yourself whatever doesn't fit another agent."

// Member is one Team agent as the identity block names it. Name and
// Description come from the agent's project, Handle from its row; Tagline
// is the short one-liner when the agent has one.
type Member struct {
	Name, Handle, Tagline, Description string
	IsCaptain                          bool
	// MentionFrom is the member's mention policy (teamlink.Mention*).
	MentionFrom string
	// Remote marks an agent that runs outside wick (Slack, A2A, plugin):
	// it gets only the message text, and "Nobody" keeps it to its owner.
	Remote bool
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
		b.WriteString(" You are the Captain — the owner's main agent. " + CaptainRoleAddon)
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

// YourTeam is the "Your team" block every Team agent gets: the other
// members (up to maxTeamListed, then a count) with handle, tagline and
// whom they take mentions from. "" with no other member. Kept apart from
// sub-agent roles, which are temporary workers, not members.
func YourTeam(team []Member) string {
	if len(team) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Your team\n")
	b.WriteString("Other agents of your owner (members, not sub-agent roles). Mention one with @handle or team_message.\n")
	for i, m := range team {
		if i == maxTeamListed {
			fmt.Fprintf(&b, "- +%d more\n", len(team)-maxTeamListed)
			break
		}
		line := fmt.Sprintf("- @%s — %s", m.Handle, m.Name)
		if t := strings.TrimSpace(m.Tagline); t != "" {
			line += ", " + t
		}
		if m.IsCaptain {
			line += " (Captain)"
		}
		line += "; " + mentionPolicyText(m)
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// SharedWithOwner is the "Shared with your owner" block: agents of other
// people shared with this agent's owner. The agent may message them like
// a teammate; their turns run in the owner's own chat with them.
func SharedWithOwner(shared []Member) string {
	if len(shared) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Shared with your owner\n")
	b.WriteString("Agents other people shared with your owner. You can message them like a teammate (team_message or @handle); each turn runs in your owner's own chat with that agent, under its own mention setting. Use one when it fits the request.\n")
	for i, m := range shared {
		if i == maxTeamListed {
			fmt.Fprintf(&b, "- +%d more\n", len(shared)-maxTeamListed)
			break
		}
		line := fmt.Sprintf("- @%s — %s", m.Handle, m.Name)
		if t := strings.TrimSpace(m.Tagline); t != "" {
			line += ", " + t
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// mentionPolicyText says in a few words whom a member takes mentions from.
// A remote member says so, and one set to "Nobody" says why agents are
// refused and how the owner lifts it, so the caller explains instead of
// guessing.
func mentionPolicyText(m Member) string {
	if m.Remote {
		if teamlink.NormalizeMentionFrom(m.MentionFrom) == teamlink.MentionOff {
			return "remote agent; only the owner may use it — agents (you too) are refused; the owner can switch its Settings › Mention to \"Any of my agents\""
		}
		return "remote agent (gets only your message text); " + mentionPolicyText(Member{MentionFrom: m.MentionFrom})
	}
	switch teamlink.NormalizeMentionFrom(m.MentionFrom) {
	case teamlink.MentionCaptain:
		return "takes mentions from the Captain only"
	case teamlink.MentionList:
		return "takes mentions from listed agents only"
	case teamlink.MentionOff:
		return "takes no mentions"
	}
	return "takes mentions from anyone"
}
