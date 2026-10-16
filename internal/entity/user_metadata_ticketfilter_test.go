package entity

import (
	"encoding/json"
	"testing"
)

// A filter saved before multi-select keeps its single pick, now as a list.
func TestTicketFilterMigratedFoldsTheOldAssignee(t *testing.T) {
	var old TicketFilter
	if err := json.Unmarshal([]byte(`{"assignee":"me","view_mode":"card"}`), &old); err != nil {
		t.Fatal(err)
	}
	got := old.Migrated()
	if len(got.Assignees) != 1 || got.Assignees[0] != "me" || got.ViewMode != "card" {
		t.Fatalf("migrated = %+v, want assignees [me], view kept", got)
	}
	// Assignees already set win; a stale single value is not kept.
	got = TicketFilter{Assignee: "u1", Assignees: []string{"u2", "none"}}.Migrated()
	if len(got.Assignees) != 2 || got.Assignees[0] != "u2" || got.Assignee != "" {
		t.Fatalf("migrated = %+v, want assignees untouched, no assignee", got)
	}
	if got = (TicketFilter{}).Migrated(); len(got.Assignees) != 0 || got.Assignee != "" {
		t.Fatalf("empty filter grew assignees: %+v", got)
	}
}

// A single pick an older build understands is mirrored into Assignee, so a
// rollback still reads it; "none" is not, since an old build would take it
// for a user id and show nothing.
func TestTicketFilterMigratedKeepsADowngradeReadableAssignee(t *testing.T) {
	if got := (TicketFilter{Assignees: []string{"u7"}}).Migrated(); got.Assignee != "u7" {
		t.Fatalf("single pick: assignee = %q, want u7", got.Assignee)
	}
	if got := (TicketFilter{Assignees: []string{"none"}}).Migrated(); got.Assignee != "" {
		t.Fatalf("none: assignee = %q, want empty", got.Assignee)
	}
	if got := (TicketFilter{Assignees: []string{"me", "u2"}}).Migrated(); got.Assignee != "" {
		t.Fatalf("several: assignee = %q, want empty", got.Assignee)
	}
}
