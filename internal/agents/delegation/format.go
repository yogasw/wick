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
		parts := make([]string, 0, len(roster))
		for _, r := range roster {
			parts = append(parts, fmt.Sprintf("@%s (%s, %s)", r.Handle, r.Role, r.State))
		}
		fmt.Fprintf(&sb, "roster: %s\n", strings.Join(parts, " · "))
	}
	sb.WriteString("left: " + b.String() + "\n")
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
