package delegation

import (
	"fmt"
	"strings"
)

// Telling a sub-agent what its turn budget is.
//
// The cap was always enforced (await counts Done events and kills on
// overrun) and never announced: a child was spawned with its role prompt
// and nothing else, so it worked blind — it did not know its allowance,
// how much of it was gone, or that running out means being killed
// MID-ACTION with no closing turn. Sub-agents were therefore cut off
// halfway through an edit, twice leaving a tree that would not build.
//
// Patching this with a sentence in the task ("stop while the build is
// green") tells an agent to watch a fuel gauge it cannot see. This is the
// gauge: the numbers go into the child's system addon at spawn, next to
// the role prompt, so they are in front of it for every turn.

// turnBudgetHeading opens the injected block. It is also the marker used
// to strip a previous block before writing a fresh one — a continued
// delegation re-runs the spawn path on the same session, and appending
// each time would stack contradictory budgets in one prompt.
const turnBudgetHeading = "## Your turn budget for this delegation"

// TurnBudgetWarnRatio is the share of the turn cap that may be spent
// before the supervising agent is told the sub-agent is running low.
//
// 0.8 leaves a real margin: at a cap of 12 the note goes out at turn 10,
// which is two turns to wrap up — enough to land a build and report a
// position, short enough that the warning is not sent so early it is
// ignored.
const TurnBudgetWarnRatio = 0.8

// FormatTurnBudget renders the budget block for a child's system addon.
//
// Only the allowance is stated, deliberately. wick has no channel that
// pushes a live remaining-turn count into a running agent's context, so a
// promise of one would be a mechanism that does not exist — and an agent
// that believes it will be warned plans to work until the warning. What
// it gets instead is the starting number plus the obligation to leave the
// work valid at every turn boundary, which needs no counter.
//
// Returns "" for a non-positive cap: no cap, nothing honest to say.
func FormatTurnBudget(turnsUsed, maxTurns int) string {
	if maxTurns <= 0 {
		return ""
	}
	left := maxTurns - turnsUsed
	if left < 0 {
		left = 0
	}
	var b strings.Builder
	b.WriteString(turnBudgetHeading + "\n")
	if turnsUsed > 0 {
		// A continued delegation carries its earlier spend: the cap is
		// absolute, so the number that matters to it is what is LEFT.
		fmt.Fprintf(&b, "Turn cap: %d (one turn = one reply of yours, however many tool calls it contains). "+
			"%d already spent on earlier legs of this delegation, %d left.\n", maxTurns, turnsUsed, left)
	} else {
		fmt.Fprintf(&b, "Turn cap: %d turns (one turn = one reply of yours, however many tool calls it contains).\n", maxTurns)
	}
	b.WriteString("This count is enforced by the harness, not by you. When it runs out you are KILLED MID-ACTION — " +
		"no final turn, no chance to finish an edit, no chance to summarise. Work that was halfway through stays halfway through.\n")
	b.WriteString("So the rule is: leave the work VALID at every turn boundary. For a code repository that means the tree still builds — " +
		"never end a turn with a half-applied change. As you approach the cap, stop starting things you cannot finish and " +
		"spend a turn reporting where you got to: a partial result that says exactly where it stopped is worth far more to " +
		"whoever reads it than one more file edited into a broken state.\n")
	return b.String()
}

// composeChildAddon joins a role prompt and its budget block, replacing
// any budget block already present.
func composeChildAddon(rolePrompt, budget string) string {
	parts := make([]string, 0, 2)
	if p := strings.TrimSpace(stripTurnBudget(rolePrompt)); p != "" {
		parts = append(parts, p)
	}
	if b := strings.TrimSpace(budget); b != "" {
		parts = append(parts, b)
	}
	return strings.Join(parts, "\n\n")
}

// stripTurnBudget removes a previously injected budget block.
//
// The block is always written last, so everything from the heading on is
// ours to drop. Anything before it — the role prompt, or an addon set
// elsewhere on this session — is left exactly as it was.
func stripTurnBudget(addon string) string {
	i := strings.Index(addon, turnBudgetHeading)
	if i < 0 {
		return addon
	}
	return strings.TrimRight(addon[:i], "\n ")
}

// turnWarnThreshold is the turn number at which a supervised sub-agent's
// leader is warned.
//
// Always at least one turn short of the cap: a warning that fires on the
// same turn that kills the agent tells the leader nothing it is not about
// to be told anyway.
func turnWarnThreshold(maxTurns int) int {
	if maxTurns <= 1 {
		return 0
	}
	n := int(float64(maxTurns)*TurnBudgetWarnRatio + 0.999999)
	if n < 1 {
		n = 1
	}
	if n >= maxTurns {
		n = maxTurns - 1
	}
	return n
}

// formatTurnWarning is the single note a leader gets when its sub-agent
// is running low on turns.
//
// Sent once per delegation, not per turn: a leader woken on every turn
// past the threshold spends its own budget reading the same fact, and the
// warning stops being read. It names the sub-agent's last known position
// because the decision this note exists to enable — let it finish, steer
// it to wrap up, or plan a continuation — cannot be made from a number.
func formatTurnWarning(name string, turnsUsed, maxTurns int, lastReport string) string {
	if name == "" {
		name = "the sub-agent"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "@%s is running out of turns — %d/%d used. This is a WARNING, not a result.\n\n",
		name, turnsUsed, maxTurns)
	if lr := strings.TrimSpace(lastReport); lr != "" {
		b.WriteString("Last reported position:\n" + lr + "\n\n")
	} else {
		b.WriteString("It has not filed a progress note, so its last position is unknown.\n\n")
	}
	fmt.Fprintf(&b, "When the cap is reached it is killed mid-action and you get partial work. "+
		"Either let it finish, or message @%s now to tell it to wrap up and report. "+
		"You can also continue it afterwards with extra turns — it keeps its session and its context.", name)
	return b.String()
}

// MaxTurnsNote is what a leader is told when a delegation ended because
// its turn cap ran out.
//
// It names the delegation id and the continue op, because the useful next
// move is almost always to carry the SAME sub-agent further in the
// session that still holds its work — and a leader told only "partial"
// re-delegates from scratch, paying again for context the sub-agent
// already has.
func MaxTurnsNote(delegationID string, turnsUsed, maxTurns int) string {
	return fmt.Sprintf(
		"PARTIAL RESULT — stopped at its turn cap (%d/%d turns used), not because the work was finished or wrong. "+
			"To carry it on where it stopped, continue delegation %s with extra_turns; it keeps its session and its "+
			"context. Re-delegating instead starts from nothing and pays for that context again.",
		turnsUsed, maxTurns, delegationID)
}
