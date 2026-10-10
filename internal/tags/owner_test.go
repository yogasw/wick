package tags

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

// The three owner modes: Add keeps every holder (connector/project/workflow
// shares), Transfer swaps one holder for another and leaves the rest, Sole
// leaves exactly one holder (provider instances) and keeps the path link.
func TestSetOwnerTxModes(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newTagsSQLite(t))
	run := func(res, path, from, user string, mode OwnerMode) {
		t.Helper()
		if err := svc.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return SetOwnerTx(tx, res, path, from, user, mode)
		}); err != nil {
			t.Fatal(err)
		}
	}
	owns := func(res, user string) bool {
		ok, _ := svc.UserOwnsResource(ctx, user, res)
		return ok
	}

	run("wf", "", "", "u1", OwnerAdd)
	run("wf", "", "", "u2", OwnerAdd)
	if !owns("wf", "u1") || !owns("wf", "u2") {
		t.Fatal("OwnerAdd must keep both holders")
	}
	run("wf", "", "u1", "u3", OwnerTransfer)
	if owns("wf", "u1") || !owns("wf", "u2") || !owns("wf", "u3") {
		t.Fatal("OwnerTransfer must drop only the previous owner")
	}

	path := "/providers/claude/x"
	run("provider:claude/x", path, "", "u1", OwnerSole)
	run("provider:claude/x", "", "", "u2", OwnerSole)
	if owns("provider:claude/x", "u1") || !owns("provider:claude/x", "u2") {
		t.Fatal("OwnerSole must leave exactly the new owner")
	}
	if ids, _ := svc.ToolTagIDs(ctx, []string{path}); len(ids[path]) != 1 {
		t.Fatalf("OwnerSole must keep the access-path link, got %v", ids)
	}
	run("provider:claude/x", "", "", "", OwnerSole)
	if owns("provider:claude/x", "u2") {
		t.Fatal("OwnerSole with no user must leave it ownerless")
	}
}
