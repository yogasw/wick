package session

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/storage"
)

func newLayout(t *testing.T) config.Layout {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return layout
}

// mkProject creates a managed project with the given id+name for tests.
func mkProject(t *testing.T, layout config.Layout, id, name string) {
	t.Helper()
	if _, err := project.Create(layout, project.CreateOptions{ID: id, Name: name}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateNoProject(t *testing.T) {
	layout := newLayout(t)
	s, err := Create(context.Background(), layout, CreateOptions{
		ID:     "T123",
		Origin: OriginSlack,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if s.Meta.Status != StatusIdle {
		t.Fatalf("status: %q", s.Meta.Status)
	}
	if !storage.PathExists(layout.SessionMeta("T123")) {
		t.Fatal("meta.json missing")
	}
	if !storage.PathExists(layout.SessionAgents("T123")) {
		t.Fatal("agents.json missing")
	}
	if s.Meta.ProjectID != "" {
		t.Fatalf("project ref should be empty, got %q", s.Meta.ProjectID)
	}
}

func TestWithProject(t *testing.T) {
	layout := newLayout(t)
	mkProject(t, layout, "p-frontend", "frontend")
	s, err := Create(context.Background(), layout, CreateOptions{
		ID:        "T999",
		ProjectID: "p-frontend",
		Origin:    OriginSlack,
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if s.Meta.ProjectID != "p-frontend" {
		t.Fatalf("project: %q", s.Meta.ProjectID)
	}
	// Project folder lives under projects/, not sessions/.
	if !storage.PathExists(layout.ProjectManagedPath("p-frontend")) {
		t.Fatal("managed project files dir missing")
	}
}

func TestCreateUnknownProjectRejected(t *testing.T) {
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{
		ID:        "T_bad",
		ProjectID: "ghost",
		Origin:    OriginUI,
	}); err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestAddAgent(t *testing.T) {
	layout := newLayout(t)
	_, _ = Create(context.Background(), layout, CreateOptions{ID: "S1", Origin: OriginUI})

	if err := AddAgent(layout, "S1", "backend", "claude"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := AddAgent(layout, "S1", "backend", "claude"); err == nil {
		t.Fatal("duplicate agent should error")
	}
	if err := SetActiveAgent(layout, "S1", "backend"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := SetActiveAgent(layout, "S1", "ghost"); err == nil {
		t.Fatal("activating unknown agent should error")
	}
	s, _ := Load(layout, "S1")
	if s.Meta.ActiveAgent != "backend" {
		t.Fatalf("active: %q", s.Meta.ActiveAgent)
	}
	if len(s.Agents) != 1 {
		t.Fatalf("agents: %v", s.Agents)
	}
}

func TestDelete(t *testing.T) {
	layout := newLayout(t)
	mkProject(t, layout, "p-del", "p")
	_, _ = Create(context.Background(), layout, CreateOptions{ID: "S2", ProjectID: "p-del", Origin: OriginUI})

	if err := Delete(context.Background(), layout, "S2"); err != nil {
		t.Fatal(err)
	}
	if storage.PathExists(layout.SessionDir("S2")) {
		t.Fatal("session dir still exists")
	}
	// Deleting a session must NOT delete the shared project.
	if !storage.PathExists(layout.ProjectDir("p-del")) {
		t.Fatal("project deleted by session delete — must stay")
	}
}

func TestSetProject(t *testing.T) {
	layout := newLayout(t)
	mkProject(t, layout, "p-a", "a")
	mkProject(t, layout, "p-b", "b")
	_, _ = Create(context.Background(), layout, CreateOptions{ID: "S3", ProjectID: "p-a", Origin: OriginUI})

	if err := SetProject(context.Background(), layout, "S3", "p-b"); err != nil {
		t.Fatalf("move: %v", err)
	}
	s, _ := Load(layout, "S3")
	if s.Meta.ProjectID != "p-b" {
		t.Fatalf("project after move: %q", s.Meta.ProjectID)
	}
}

func TestInvalidID(t *testing.T) {
	layout := newLayout(t)
	_, err := Create(context.Background(), layout, CreateOptions{ID: "../escape"})
	if err == nil {
		t.Fatal("bad id accepted")
	}
}

func TestSetMaxTurns(t *testing.T) {
	layout := newLayout(t)
	_, _ = Create(context.Background(), layout, CreateOptions{ID: "S1", Origin: OriginUI})

	// Upsert: creates the agent entry when none exists yet.
	if err := SetMaxTurns(layout, "S1", "main", 4); err != nil {
		t.Fatalf("set (create): %v", err)
	}
	s, _ := Load(layout, "S1")
	if len(s.Agents) != 1 || s.Agents[0].MaxTurns != 4 {
		t.Fatalf("after create want 1 agent maxTurns=4, got %+v", s.Agents)
	}

	// Updates the existing entry in place (no duplicate row).
	if err := SetMaxTurns(layout, "S1", "main", 9); err != nil {
		t.Fatalf("set (update): %v", err)
	}
	s, _ = Load(layout, "S1")
	if len(s.Agents) != 1 || s.Agents[0].MaxTurns != 9 {
		t.Fatalf("after update want 1 agent maxTurns=9, got %+v", s.Agents)
	}
}
