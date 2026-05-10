package registry

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workspace"
)

func containsName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

func newLayout(t *testing.T) config.Layout {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestReloadEmpty(t *testing.T) {
	layout := newLayout(t)
	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(r.WorkspaceNames()) != 0 {
		t.Fatal("expected empty workspaces")
	}
	if len(r.SessionIDs()) != 0 {
		t.Fatal("expected empty sessions")
	}
}

func TestReloadAfterMutate(t *testing.T) {
	layout := newLayout(t)
	_, _ = workspace.Create(layout, workspace.CreateOptions{Name: "p"})
	_, _ = session.Create(context.Background(), layout, session.CreateOptions{ID: "S1", Origin: session.OriginUI})
	_ = preset.Create(layout, "rev", "x")

	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := r.WorkspaceNames(); len(got) != 1 || got[0] != "p" {
		t.Fatalf("workspaces: %v", got)
	}
	if got := r.SessionIDs(); len(got) != 1 || got[0] != "S1" {
		t.Fatalf("sessions: %v", got)
	}
	if !r.HasPreset("rev") {
		t.Fatal("preset not seen")
	}
}

func TestReloadResetsRunningStatus(t *testing.T) {
	layout := newLayout(t)
	s, _ := session.Create(context.Background(), layout, session.CreateOptions{ID: "S2", Origin: session.OriginUI})
	// Simulate a session that was mid-run when wick crashed.
	s.Meta.Status = session.StatusRunning
	_ = session.SaveMeta(layout, "S2", s.Meta)

	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	got, ok := r.Session("S2")
	if !ok {
		t.Fatal("session missing")
	}
	if got.Meta.Status != session.StatusIdle {
		t.Fatalf("status not reset: %q", got.Meta.Status)
	}
	disk, _ := session.Load(layout, "S2")
	if disk.Meta.Status != session.StatusIdle {
		t.Fatalf("disk status not reset: %q", disk.Meta.Status)
	}
}

func TestManagerCreateDelete(t *testing.T) {
	layout := newLayout(t)
	mgr, err := Bootstrap(layout)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateWorkspace(context.Background(), workspace.CreateOptions{Name: "p"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateSession(context.Background(), session.CreateOptions{ID: "S1", Workspace: "p", Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Registry().WorkspaceNames(); !containsName(got, "p") {
		t.Fatalf("workspaces missing p: %v", got)
	}
	if got := mgr.Registry().SessionIDs(); len(got) != 1 {
		t.Fatalf("sessions: %v", got)
	}

	if err := mgr.DeleteSession(context.Background(), "S1"); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Registry().SessionIDs(); len(got) != 0 {
		t.Fatalf("sessions after delete: %v", got)
	}

	if err := mgr.DeleteWorkspace(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Registry().WorkspaceNames(); containsName(got, "p") {
		t.Fatalf("workspace p still present after delete: %v", got)
	}
}

func TestManagerDeleteWorkspaceDetachesSessions(t *testing.T) {
	layout := newLayout(t)
	mgr, _ := Bootstrap(layout)
	_, _ = mgr.CreateWorkspace(context.Background(), workspace.CreateOptions{Name: "p"})
	_, _ = mgr.CreateSession(context.Background(), session.CreateOptions{ID: "S1", Workspace: "p", Origin: session.OriginUI})

	if err := mgr.DeleteWorkspace(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	got, ok := mgr.Registry().Session("S1")
	if !ok {
		t.Fatal("session removed unexpectedly")
	}
	if got.Meta.Workspace != "" {
		t.Fatalf("session not detached: %q", got.Meta.Workspace)
	}
}

func TestBootstrapIdempotent(t *testing.T) {
	layout := newLayout(t)
	if _, err := Bootstrap(layout); err != nil {
		t.Fatal(err)
	}
	if _, err := Bootstrap(layout); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
}
