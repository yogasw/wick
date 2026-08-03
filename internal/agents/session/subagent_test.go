package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// mkSub creates a sub-agent session under parentID and returns its ID.
func mkSub(t *testing.T, layout config.Layout, parentID, seg string) string {
	t.Helper()
	id := config.SubSessionID(parentID, seg)
	if _, err := Create(context.Background(), layout, CreateOptions{
		ID:              id,
		Origin:          OriginUI,
		ParentSessionID: parentID,
	}); err != nil {
		t.Fatalf("create sub %q: %v", id, err)
	}
	return id
}

// A sub-agent's folder is materialised inside its parent's, and Load
// finds it there without being told who the parent is.
func TestCreateSubSessionNests(t *testing.T) {
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{ID: "root", Origin: OriginUI}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	child := mkSub(t, layout, "root", "9f2c81ab40de")

	want := filepath.Join(layout.SessionsDir(), "root", "subagents", "9f2c81ab40de")
	if !storage.PathExists(filepath.Join(want, "meta.json")) {
		t.Fatalf("child meta.json not at %s", want)
	}
	if storage.PathExists(filepath.Join(layout.SessionsDir(), child)) {
		t.Fatal("child must not also exist as a flat top-level folder")
	}

	got, err := Load(layout, child)
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	if got.Meta.ParentSessionID != "root" {
		t.Fatalf("parent link = %q, want %q", got.Meta.ParentSessionID, "root")
	}
}

// The conversation list scans the top level only, so nesting is what
// keeps sub-agents out of the sidebar — no filtering required.
func TestListSkipsSubSessions(t *testing.T) {
	layout := newLayout(t)
	for _, id := range []string{"root", "other"} {
		if _, err := Create(context.Background(), layout, CreateOptions{ID: id, Origin: OriginUI}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mkSub(t, layout, "root", "9f2c81ab40de")

	ids, err := List(layout)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, id := range ids {
		if config.ParentOfSubSession(id) != "" {
			t.Fatalf("List returned a sub-agent session: %q", id)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("List = %v, want the 2 top-level sessions", ids)
	}
}

// Deleting a conversation takes its whole delegation tree with it. This
// is the cascade cleanup the nesting exists for: no orphan sweep, no
// bookkeeping that can drift from what is on disk.
func TestDeleteParentRemovesDescendants(t *testing.T) {
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{ID: "root", Origin: OriginUI}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	child := mkSub(t, layout, "root", "9f2c81ab40de")
	grandchild := mkSub(t, layout, child, "71ee03cc95a1")

	// A sibling conversation must be left alone.
	if _, err := Create(context.Background(), layout, CreateOptions{ID: "other", Origin: OriginUI}); err != nil {
		t.Fatalf("create other: %v", err)
	}

	if err := Delete(context.Background(), layout, "root"); err != nil {
		t.Fatalf("delete root: %v", err)
	}
	for _, id := range []string{"root", child, grandchild} {
		if _, err := Load(layout, id); !os.IsNotExist(err) {
			t.Fatalf("load %q after delete: err = %v, want not-exist", id, err)
		}
	}
	if _, err := Load(layout, "other"); err != nil {
		t.Fatalf("unrelated session was collateral damage: %v", err)
	}
}

// Deleting one sub-agent leaves its siblings and its parent intact.
func TestDeleteSubSessionLeavesParent(t *testing.T) {
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{ID: "root", Origin: OriginUI}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	a := mkSub(t, layout, "root", "9f2c81ab40de")
	b := mkSub(t, layout, "root", "71ee03cc95a1")

	if err := Delete(context.Background(), layout, a); err != nil {
		t.Fatalf("delete sub: %v", err)
	}
	if _, err := Load(layout, a); !os.IsNotExist(err) {
		t.Fatalf("deleted sub still loads: %v", err)
	}
	if _, err := Load(layout, b); err != nil {
		t.Fatalf("sibling sub was removed too: %v", err)
	}
	if _, err := Load(layout, "root"); err != nil {
		t.Fatalf("parent was removed too: %v", err)
	}
}
