package registry

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
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

func mkProject(t *testing.T, layout config.Layout, id, name string) {
	t.Helper()
	if _, err := project.Create(layout, project.CreateOptions{ID: id, Name: name}); err != nil {
		t.Fatal(err)
	}
}

func TestReloadEmpty(t *testing.T) {
	layout := newLayout(t)
	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(r.ProjectIDs()) != 0 {
		t.Fatal("expected empty projects")
	}
	if len(r.SessionIDs()) != 0 {
		t.Fatal("expected empty sessions")
	}
}

func TestReloadAfterMutate(t *testing.T) {
	layout := newLayout(t)
	mkProject(t, layout, "p1", "p")
	_, _ = session.Create(context.Background(), layout, session.CreateOptions{ID: "S1", Origin: session.OriginUI})
	_ = preset.Create(layout, "rev", "x")

	r := New(layout)
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := r.ProjectIDs(); len(got) != 1 || got[0] != "p1" {
		t.Fatalf("projects: %v", got)
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
	p, err := mgr.CreateProject(context.Background(), project.CreateOptions{ID: "p1", Name: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateSession(context.Background(), session.CreateOptions{ID: "S1", ProjectID: p.Meta.ID, Origin: session.OriginUI}); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Registry().ProjectIDs(); !containsName(got, "p1") {
		t.Fatalf("projects missing p1: %v", got)
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

	if err := mgr.DeleteProject(context.Background(), "p1"); err != nil {
		t.Fatal(err)
	}
	if got := mgr.Registry().ProjectIDs(); containsName(got, "p1") {
		t.Fatalf("project p1 still present after delete: %v", got)
	}
}

func TestManagerDeleteProjectUnscopesSessions(t *testing.T) {
	layout := newLayout(t)
	mgr, _ := Bootstrap(layout)
	_, _ = mgr.CreateProject(context.Background(), project.CreateOptions{ID: "p1", Name: "p"})
	_, _ = mgr.CreateSession(context.Background(), session.CreateOptions{ID: "S1", ProjectID: "p1", Origin: session.OriginUI})

	if err := mgr.DeleteProject(context.Background(), "p1"); err != nil {
		t.Fatal(err)
	}
	got, ok := mgr.Registry().Session("S1")
	if !ok {
		t.Fatal("session removed unexpectedly")
	}
	if got.Meta.ProjectID != "" {
		t.Fatalf("session not unscoped: %q", got.Meta.ProjectID)
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

func TestBootstrapSeedsDefaultProject(t *testing.T) {
	layout := newLayout(t)
	mgr, err := Bootstrap(layout)
	if err != nil {
		t.Fatal(err)
	}
	ids := mgr.Registry().ProjectIDs()
	if len(ids) != 1 {
		t.Fatalf("expected 1 default project, got %v", ids)
	}
	p, _ := mgr.Registry().Project(ids[0])
	if p.Meta.Name != project.DefaultName {
		t.Fatalf("default project name: %q", p.Meta.Name)
	}
}