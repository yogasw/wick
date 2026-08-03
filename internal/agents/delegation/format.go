package delegation

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// RosterEntry is one addressable agent, as the recipient should see it.
type RosterEntry struct {
	Handle string
	Role   string
	State  string // working | idle | done
}

// BudgetLine is what is left of the tree's allowances.
type BudgetLine struct {
	TurnsUsed, TurnsMax   int
	TokensUsed, TokensMax int
	Hop, HopMax           int
}

// FormatInbound renders a batch of queued messages as ONE turn.
//
// The roster and the budget ride on every delivery rather than being
// injected once at spawn, for two reasons. The roster changes as
// instances appear and finish, so a snapshot taken at spawn names agents
// that no longer exist within minutes. And an agent that cannot see what
// is left will happily open an exchange it has no budget to finish, then
// stop mid-thought when the cap lands.
func FormatInbound(msgs []entity.AgentMessage, roster []RosterEntry, b BudgetLine) string {
	var sb strings.Builder
	for _, m := range msgs {
		verb := "says"
		switch m.Kind {
		case entity.MessageAsk:
			// Spelled out: the recipient has to know an answer is owed, or
			// the sender blocks until its timeout for nothing.
			verb = fmt.Sprintf("asks (reply with message_id %s)", m.ID)
		case entity.MessageReply:
			verb = "replies"
		}
		fmt.Fprintf(&sb, "── from @%s %s ──\n%s\n\n", m.FromHandle, verb, strings.TrimSpace(m.Body))
	}
	if len(roster) > 0 {
		fmt.Fprintf(&sb, "roster: %s\n", rosterLine(roster))
	}
	sb.WriteString("left: " + b.String() + "\n")
	return sb.String()
}

// rosterLine renders the address list. Shared with the spawn-time block
// so the two cannot drift into different formats for the same thing.
func rosterLine(roster []RosterEntry) string {
	parts := make([]string, 0, len(roster))
	for _, r := range roster {
		parts = append(parts, fmt.Sprintf("@%s (%s, %s)", r.Handle, r.Role, r.State))
	}
	return strings.Join(parts, " · ")
}

// FormatRosterBlock is what a sub-agent is told about its colleagues at
// SPAWN time.
//
// Until this existed a fresh sub-agent learned the roster only when
// somebody messaged it, so it never considered asking anyone anything —
// the messaging ops were reachable and invisible at the same time.
//
// The snapshot is labelled as one, with the op that refreshes it named in
// the same breath. That is the honest answer to the objection that a
// roster goes stale: the problem is not the snapshot, it is a snapshot
// presented as current.
func FormatRosterBlock(roster []RosterEntry, spawnable []string, b BudgetLine) string {
	if len(roster) == 0 && len(spawnable) == 0 {
		return ""
	}
	var sb strings.Builder
	if len(roster) > 0 {
		sb.WriteString("roster (snapshot at spawn — call list_agents for the current list):\n")
		sb.WriteString("  " + rosterLine(roster) + "\n")
	}
	if len(spawnable) > 0 {
		// Keys only: a description would grow every spawn's prompt without
		// changing the decision, and list_agents returns them on demand.
		fmt.Fprintf(&sb, "spawnable roles: %s\n", strings.Join(spawnable, ", "))
	}
	sb.WriteString("left: " + b.String() + "\n")
	sb.WriteString("Message a peer with the message op, or open a line with @handle at the start of a line.\n")
	return sb.String()
}

// String renders remaining allowances.
//
// Remaining rather than consumed: "28/40 used" needs arithmetic before it
// changes a decision, and the decision is always "can I afford one more
// round?".
func (b BudgetLine) String() string {
	parts := []string{
		fmt.Sprintf("%d/%d turns left", max(b.TurnsMax-b.TurnsUsed, 0), b.TurnsMax),
		fmt.Sprintf("%d/%d hops left", max(b.HopMax-b.Hop, 0), b.HopMax),
	}
	if b.TokensMax > 0 {
		parts = append(parts, fmt.Sprintf("%dk/%dk tokens left",
			max(b.TokensMax-b.TokensUsed, 0)/1000, b.TokensMax/1000))
	}
	return strings.Join(parts, " · ")
}
