package tags

import (
	"context"
	"testing"
)

// Renaming a provider instance moves its tags and its owner tag along:
// the new path is filtered by the same tag, the owner still owns it, and
// the old path is left bare.
func TestMoveResourceTags(t *testing.T) {
	ctx := context.Background()
	svc := NewService(newTagsSQLite(t))
	oldPath, newPath := "/providers/claude/old", "/providers/claude/new"

	if err := svc.SetSoleOwner(ctx, "provider:claude/old", oldPath, "u-1"); err != nil {
		t.Fatalf("SetSoleOwner: %v", err)
	}
	if err := svc.MoveResourceTags(ctx, map[string]string{oldPath: newPath}, "provider:claude/old", "provider:claude/new"); err != nil {
		t.Fatalf("MoveResourceTags: %v", err)
	}

	ids, err := svc.ToolTagIDs(ctx, []string{oldPath, newPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids[oldPath]) != 0 || len(ids[newPath]) != 1 {
		t.Fatalf("tool tags after move = %v", ids)
	}
	if ok, _ := svc.UserOwnsResource(ctx, "u-1", "provider:claude/new"); !ok {
		t.Error("owner lost the renamed instance")
	}
	if ok, _ := svc.UserOwnsResource(ctx, "u-1", "provider:claude/old"); ok {
		t.Error("old owner tag still resolves")
	}
	// A path with no tags and a missing owner tag are no-ops.
	if err := svc.MoveResourceTags(ctx, map[string]string{"/providers/claude/none": "/providers/claude/x"}, "missing", "other"); err != nil {
		t.Fatal(err)
	}
	// Delete: tags on the paths go, so a new instance with the name starts clean.
	if err := svc.ClearToolPaths(ctx, newPath); err != nil {
		t.Fatal(err)
	}
	if ids, _ := svc.ToolTagIDs(ctx, []string{newPath}); len(ids[newPath]) != 0 {
		t.Fatalf("tags left after ClearToolPaths: %v", ids)
	}
}
