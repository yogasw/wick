package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/workspace"
)

func newLayout(t *testing.T) config.Layout {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestCreateManaged(t *testing.T) {
	layout := newLayout(t)
	p, err := Create(layout, CreateOptions{ID: "p1", Name: "Backend"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Meta.Icon != "📁" {
		t.Fatalf("default icon: %q", p.Meta.Icon)
	}
	if p.Meta.Defaults.Preset != "default" {
		t.Fatalf("default preset: %q", p.Meta.Defaults.Preset)
	}
	if !storage.PathExists(layout.ProjectManagedPath("p1")) {
		t.Fatal("managed files/ dir not created")
	}
	if !Exists(layout, "p1") {
		t.Fatal("Exists false after create")
	}
}

func TestCreateCustomPath(t *testing.T) {
	layout := newLayout(t)
	custom := t.TempDir()
	p, err := Create(layout, CreateOptions{ID: "p2", Name: "Repo", CustomPath: custom})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Meta.CustomPath != custom {
		t.Fatalf("custom path: %q", p.Meta.CustomPath)
	}
	// Managed files/ must NOT be created for custom-path projects.
	if storage.PathExists(layout.ProjectManagedPath("p2")) {
		t.Fatal("managed files/ created for custom project — should not")
	}
	got, err := ResolvePath(layout, "p2")
	if err != nil || got != custom {
		t.Fatalf("resolve custom: %q err=%v", got, err)
	}
}

func TestCreateRejectsMissingCustomPath(t *testing.T) {
	layout := newLayout(t)
	_, err := Create(layout, CreateOptions{ID: "p3", Name: "X", CustomPath: filepath.Join(t.TempDir(), "nope")})
	if err == nil {
		t.Fatal("expected error for non-existent custom path")
	}
}

func TestResolvePathManaged(t *testing.T) {
	layout := newLayout(t)
	_, _ = Create(layout, CreateOptions{ID: "p4", Name: "M"})
	got, err := ResolvePath(layout, "p4")
	if err != nil {
		t.Fatal(err)
	}
	if got != layout.ProjectManagedPath("p4") {
		t.Fatalf("managed resolve: %q", got)
	}
}

func TestSaveMetaBumpsUpdatedAt(t *testing.T) {
	layout := newLayout(t)
	p, _ := Create(layout, CreateOptions{ID: "p5", Name: "Old"})
	orig := p.Meta.UpdatedAt
	m := p.Meta
	m.Name = "New"
	if err := SaveMeta(layout, "p5", m); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := Load(layout, "p5")
	if reloaded.Meta.Name != "New" {
		t.Fatalf("name not saved: %q", reloaded.Meta.Name)
	}
	if !reloaded.Meta.UpdatedAt.After(orig) {
		t.Fatal("UpdatedAt not bumped")
	}
}

func TestDeleteManagedRemovesFiles(t *testing.T) {
	layout := newLayout(t)
	_, _ = Create(layout, CreateOptions{ID: "p6", Name: "Doomed"})
	if err := Delete(layout, "p6"); err != nil {
		t.Fatal(err)
	}
	if storage.PathExists(layout.ProjectDir("p6")) {
		t.Fatal("project dir still exists after delete")
	}
}

func TestDeleteCustomLeavesExternalFolder(t *testing.T) {
	layout := newLayout(t)
	custom := t.TempDir()
	_, _ = Create(layout, CreateOptions{ID: "p7", Name: "Ext", CustomPath: custom})
	if err := Delete(layout, "p7"); err != nil {
		t.Fatal(err)
	}
	if storage.PathExists(layout.ProjectDir("p7")) {
		t.Fatal("project meta dir still exists")
	}
	// External folder must be untouched.
	if !storage.PathExists(custom) {
		t.Fatal("external custom folder deleted — must stay")
	}
}

func TestDeleteDefaultRejected(t *testing.T) {
	layout := newLayout(t)
	_, _ = Create(layout, CreateOptions{ID: "p8", Name: DefaultName})
	if err := Delete(layout, "p8"); err == nil {
		t.Fatal("deleting default project should error")
	}
}

func TestEnsureDefaultIdempotent(t *testing.T) {
	layout := newLayout(t)
	seq := 0
	newID := func() string { seq++; return "def" }
	if err := EnsureDefault(layout, newID); err != nil {
		t.Fatal(err)
	}
	// Second call must be a no-op (a project already exists).
	if err := EnsureDefault(layout, newID); err != nil {
		t.Fatal(err)
	}
	ids, _ := List(layout)
	if len(ids) != 1 {
		t.Fatalf("expected 1 project, got %v", ids)
	}
	p, _ := Load(layout, ids[0])
	if p.Meta.Name != DefaultName {
		t.Fatalf("default name: %q", p.Meta.Name)
	}
}

// ── Migration ──────────────────────────────────────────────────────

func TestMigrateWorkspacesToProjects(t *testing.T) {
	layout := newLayout(t)

	// Seed two legacy workspaces: one managed (with a file inside files/),
	// one custom-path.
	if _, err := workspace.Create(layout, workspace.CreateOptions{
		Name:          "wick",
		DefaultPreset: "engineer",
		Description:   "main",
	}); err != nil {
		t.Fatal(err)
	}
	// Drop a marker file in the managed files/ dir so we can assert the move.
	marker := filepath.Join(layout.WorkspaceManagedPath("wick"), "README.md")
	if err := os.WriteFile(marker, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	customDir := t.TempDir()
	if _, err := workspace.Create(layout, workspace.CreateOptions{
		Name:       "docs",
		CustomPath: customDir,
	}); err != nil {
		t.Fatal(err)
	}

	seq := 0
	newID := func() string { seq++; return "mig-" + string(rune('a'+seq-1)) }

	relinked := map[string]string{}
	relink := func(ws, pid string) error { relinked[ws] = pid; return nil }

	if err := MigrateWorkspacesToProjects(layout, newID, relink); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ids, _ := List(layout)
	if len(ids) != 2 {
		t.Fatalf("expected 2 migrated projects, got %v", ids)
	}
	if len(relinked) != 2 {
		t.Fatalf("expected 2 relink calls, got %v", relinked)
	}

	// Find the managed project (name "wick") and assert files moved.
	var wickPID string
	for _, id := range ids {
		p, _ := Load(layout, id)
		if p.Meta.Name == "wick" {
			wickPID = id
			if p.Meta.Defaults.Preset != "engineer" {
				t.Fatalf("preset not carried: %q", p.Meta.Defaults.Preset)
			}
		}
		if p.Meta.Name == "docs" && p.Meta.CustomPath != customDir {
			t.Fatalf("custom path not carried: %q", p.Meta.CustomPath)
		}
	}
	if wickPID == "" {
		t.Fatal("managed project not found")
	}
	moved := filepath.Join(layout.ProjectManagedPath(wickPID), "README.md")
	if !storage.PathExists(moved) {
		t.Fatalf("managed files not moved to %s", moved)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	layout := newLayout(t)
	_, _ = workspace.Create(layout, workspace.CreateOptions{Name: "a"})

	newID := func() string { return "fixed-id" }
	calls := 0
	relink := func(_, _ string) error { calls++; return nil }

	if err := MigrateWorkspacesToProjects(layout, newID, relink); err != nil {
		t.Fatal(err)
	}
	// Second run: a project already exists → must be a no-op.
	if err := MigrateWorkspacesToProjects(layout, newID, relink); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("relink called %d times — migration not idempotent", calls)
	}
	ids, _ := List(layout)
	if len(ids) != 1 {
		t.Fatalf("expected 1 project after 2 migrations, got %v", ids)
	}
}

func TestMigrateNoWorkspacesNoop(t *testing.T) {
	layout := newLayout(t)
	if err := MigrateWorkspacesToProjects(layout, func() string { return "x" }, func(_, _ string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ids, _ := List(layout)
	if len(ids) != 0 {
		t.Fatalf("expected 0 projects (no workspaces to migrate), got %v", ids)
	}
}

func TestRelinkSessions(t *testing.T) {
	layout := newLayout(t)
	// Write a legacy session meta with a workspace field + other fields
	// that must be preserved through the raw-map round-trip.
	sid := "S1"
	if err := os.MkdirAll(layout.SessionDir(sid), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := map[string]any{
		"workspace": "wick",
		"origin":    "ui",
		"preset":    "engineer",
		"status":    "idle",
	}
	if err := storage.WriteJSON(layout.SessionMeta(sid), &raw); err != nil {
		t.Fatal(err)
	}

	if err := RelinkSessions(layout, "wick", "proj-1"); err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := storage.ReadJSON(layout.SessionMeta(sid), &got); err != nil {
		t.Fatal(err)
	}
	if got["project_id"] != "proj-1" {
		t.Fatalf("project_id not set: %v", got["project_id"])
	}
	if _, ok := got["workspace"]; ok {
		t.Fatal("legacy workspace field not dropped")
	}
	// Unrelated fields preserved.
	if got["preset"] != "engineer" {
		t.Fatalf("preset clobbered: %v", got["preset"])
	}
}
