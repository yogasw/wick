package usagereport

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/store"
)

// A spend row is read by a person deciding where the money went, so it
// must carry the name. The id stays as the key — the UI prints it small
// — but it can never be the only thing shown.
func TestSlicesCarryTheirLabel(t *testing.T) {
	m := map[string]store.UsageTotals{
		"53e3bcf3-1539-4bb3-a578-da34a45f90bf": {Input: 100, Output: 10, CostUSD: 2},
		"ec0c0b8b-e73c-4561-9d19-bfa9c481a816": {Input: 50, Output: 5, CostUSD: 1},
	}
	names := map[string]string{
		"53e3bcf3-1539-4bb3-a578-da34a45f90bf": "Yoga Setiawan",
		// the second id has no user row any more
	}
	b := &Builder{UserName: func(id string) string { return names[id] }}
	rows := Slices(m, 165, b.userName)
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	// Sorted by cost: the labelled one spent more.
	if rows[0].Label != "Yoga Setiawan" {
		t.Fatalf("row 0 label = %q, want the user's name", rows[0].Label)
	}
	// A vanished user keeps its row and its id — the spend still happened.
	if rows[1].Label != "" || rows[1].Key == "" {
		t.Fatalf("row 1 = %+v, want an unlabelled row that still has its key", rows[1])
	}
}

// Provider keys already read as names ("claude/opus"); labelling them
// would just print the same string twice.
func TestSlicesLeaveProviderRowsUnlabelled(t *testing.T) {
	rows := Slices(map[string]store.UsageTotals{"claude/opus": {Input: 1}}, 1, nil)
	if len(rows) != 1 || rows[0].Label != "" {
		t.Fatalf("rows = %+v, want one unlabelled row", rows)
	}
}

// The user name must not echo the id back: the caller's map falls back
// to the id when a user has neither name nor email, and a row whose
// "name" is its own UUID reads as a bug.
func TestUserNameRejectsTheIdAsAName(t *testing.T) {
	id := "ec0c0b8b-e73c-4561-9d19-bfa9c481a816"
	b := &Builder{UserName: func(string) string { return id }}
	if got := b.userName(id); got != "" {
		t.Fatalf("userName = %q, want empty", got)
	}
	b = &Builder{UserName: func(string) string { return "ratih@qiscus.com" }}
	if got := b.userName(id); got != "ratih@qiscus.com" {
		t.Fatalf("userName = %q, want the email fallback", got)
	}
	// No resolver at all is normal (no database wired): the row shows
	// its id rather than the process refusing to build a report.
	if got := (&Builder{}).userName(id); got != "" {
		t.Fatalf("userName with no resolver = %q, want empty", got)
	}
}

// A provider is a door, not a price: "claude/default" answers on opus one
// turn and haiku the next. The breakdown has to name the model, and say
// which door it came through — the same model id reached through two
// accounts is two rows, not one.
func TestModelSlicesNameTheModelAndItsProvider(t *testing.T) {
	roll := store.UsageRollup{
		ByModel: map[string]store.UsageTotals{
			store.ModelRowKey("claude/default", "claude-opus-5"):  {Input: 100, Output: 20, CostUSD: 3},
			store.ModelRowKey("claude/default", "claude-haiku-4"): {Input: 40, Output: 5, CostUSD: 0.2},
			store.ModelRowKey("codex/work", "gpt-5"):              {Input: 10, Output: 2, CostUSD: 1},
		},
		ModelTurns: map[string]int{
			store.ModelRowKey("claude/default", "claude-opus-5"):  3,
			store.ModelRowKey("claude/default", "claude-haiku-4"): 40,
			store.ModelRowKey("codex/work", "gpt-5"):              1,
		},
	}
	rows := ModelSlices(roll, 177, "")
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[0].Label != "claude-opus-5" || rows[0].Group != "claude/default" {
		t.Fatalf("row 0 = %+v, want the model named and its provider as the group", rows[0])
	}
	// Turns travel: on a flat-rate plan every cost is zero, and "which
	// model do we actually use" is then the only answerable question.
	if rows[0].Turns != 3 {
		t.Fatalf("row 0 turns = %d, want 3", rows[0].Turns)
	}
}

// A provider's own page asks a narrower question, and must not show the
// models another provider ran.
func TestModelSlicesNarrowToOneProvider(t *testing.T) {
	roll := store.UsageRollup{
		ByModel: map[string]store.UsageTotals{
			store.ModelRowKey("claude/default", "claude-opus-5"): {Input: 100},
			store.ModelRowKey("codex/work", "gpt-5"):             {Input: 10},
		},
		ModelTurns: map[string]int{},
	}
	rows := ModelSlices(roll, 110, "codex/work")
	if len(rows) != 1 || rows[0].Label != "gpt-5" {
		t.Fatalf("rows = %+v, want only codex/work's model", rows)
	}
}
