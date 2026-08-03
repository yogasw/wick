package delegation

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// A fresh sub-agent used to learn the roster only when somebody messaged
// it, so it never considered asking anyone anything. This block is what
// makes the messaging ops visible at the moment they become usable.
func TestFormatRosterBlockAtSpawn(t *testing.T) {
	got := FormatRosterBlock(
		[]RosterEntry{
			{Handle: "main", Role: "leader", State: "working"},
			{Handle: "code-investigator", Role: "code-investigator", State: "working"},
		},
		[]string{"log-investigator", "docs-investigator"},
		BudgetLine{TurnsUsed: 6, TurnsMax: 40, Hop: 0, HopMax: 10},
	)
	want := "roster (snapshot at spawn — call list_agents for the current list):\n" +
		"  @main (leader, working) · @code-investigator (code-investigator, working)\n" +
		"spawnable roles: log-investigator, docs-investigator\n" +
		"left: 34/40 turns left · 10/10 hops left\n" +
		"Message a peer with the message op, or open a line with @handle at the start of a line.\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

// An empty header reads as "everyone left", which is worse than saying
// nothing at all.
func TestFormatRosterBlockIsEmptyWhenAlone(t *testing.T) {
	if got := FormatRosterBlock(nil, nil, BudgetLine{TurnsMax: 40, HopMax: 10}); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// A tree with colleagues but no un-started roles must not print a bare
// "spawnable roles:" line.
func TestFormatRosterBlockOmitsAnEmptySpawnableList(t *testing.T) {
	got := FormatRosterBlock(
		[]RosterEntry{{Handle: "main", Role: "leader", State: "working"}},
		nil,
		BudgetLine{TurnsMax: 40, HopMax: 10},
	)
	if strings.Contains(got, "spawnable roles") {
		t.Fatalf("got:\n%s", got)
	}
}

func TestFormatInboundCarriesSenderRosterAndBudget(t *testing.T) {
	msgs := []entity.AgentMessage{
		{FromHandle: "reviewer", Body: "2 of 5 files done.", Kind: entity.MessageTell},
	}
	roster := []RosterEntry{
		{Handle: entity.LeaderHandle, Role: "leader", State: "working"},
		{Handle: "reviewer", Role: "code-reviewer", State: "working"},
	}
	out := FormatInbound(msgs, roster, BudgetLine{
		TurnsUsed: 28, TurnsMax: 40, TokensUsed: 340_000, TokensMax: 1_000_000, Hop: 7, HopMax: 10,
	})

	if !strings.Contains(out, "from @reviewer") {
		t.Fatalf("sender missing:\n%s", out)
	}
	if !strings.Contains(out, "2 of 5 files done.") {
		t.Fatalf("body missing:\n%s", out)
	}
	if !strings.Contains(out, "@reviewer (code-reviewer, working)") {
		t.Fatalf("roster missing:\n%s", out)
	}
	// Remaining, not consumed: an agent budgets against what is LEFT.
	if !strings.Contains(out, "12/40 turns left") {
		t.Fatalf("turn remainder missing or wrong:\n%s", out)
	}
	if !strings.Contains(out, "3/10 hops left") {
		t.Fatalf("hop remainder missing or wrong:\n%s", out)
	}
	if !strings.Contains(out, "660k/1000k tokens left") {
		t.Fatalf("token remainder missing or wrong:\n%s", out)
	}
}

func TestFormatInboundBatchesEveryMessageAndMarksAsks(t *testing.T) {
	msgs := []entity.AgentMessage{
		{FromHandle: entity.LeaderHandle, Body: "first", Kind: entity.MessageTell},
		{ID: "m-2", FromHandle: "worker-2", Body: "second", Kind: entity.MessageAsk},
	}
	out := FormatInbound(msgs, nil, BudgetLine{TurnsMax: 40, HopMax: 10})
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Fatalf("batched messages dropped:\n%s", out)
	}
	// The recipient needs both the fact a reply is owed and the id to
	// reply with; without the id it cannot answer even if it wants to.
	if !strings.Contains(out, "asks") || !strings.Contains(out, "m-2") {
		t.Fatalf("ask not actionable:\n%s", out)
	}
}

// A tokenless provider reports 0 usage. Printing "0/0 tokens left" there
// would read as "budget exhausted" and stop work that has no token cap.
func TestFormatInboundOmitsTokensWhenThereIsNoCap(t *testing.T) {
	out := FormatInbound(
		[]entity.AgentMessage{{FromHandle: "a", Body: "x", Kind: entity.MessageTell}},
		nil,
		BudgetLine{TurnsMax: 40, HopMax: 10, TokensMax: 0},
	)
	if strings.Contains(out, "tokens left") {
		t.Fatalf("token line shown despite no cap:\n%s", out)
	}
}
