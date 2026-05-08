package session

import (
	"context"
	"os/exec"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/storage"
)

func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
}

func newLayout(t *testing.T) config.Layout {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return layout
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
	if storage.PathExists(layout.SessionWorkspace("T123")) {
		t.Fatal("workspace should be absent without project")
	}
}

func TestWithProject(t *testing.T) {
	gitAvailable(t)
	layout := newLayout(t)
	if _, err := project.Create(context.Background(), layout, project.CreateOptions{Name: "frontend"}); err != nil {
		t.Fatal(err)
	}
	s, err := Create(context.Background(), layout, CreateOptions{
		ID:      "T999",
		Project: "frontend",
		Origin:  OriginSlack,
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if s.Meta.Project != "frontend" {
		t.Fatalf("project: %q", s.Meta.Project)
	}
	if !storage.PathExists(layout.SessionWorkspace("T999")) {
		t.Fatal("worktree missing")
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
	gitAvailable(t)
	layout := newLayout(t)
	_, _ = project.Create(context.Background(), layout, project.CreateOptions{Name: "p"})
	_, _ = Create(context.Background(), layout, CreateOptions{ID: "S2", Project: "p", Origin: OriginUI})

	if err := Delete(context.Background(), layout, "S2"); err != nil {
		t.Fatal(err)
	}
	if storage.PathExists(layout.SessionDir("S2")) {
		t.Fatal("session dir still exists")
	}
}

func TestSwitchProject(t *testing.T) {
	gitAvailable(t)
	layout := newLayout(t)
	_, _ = project.Create(context.Background(), layout, project.CreateOptions{Name: "a"})
	_, _ = project.Create(context.Background(), layout, project.CreateOptions{Name: "b"})
	_, _ = Create(context.Background(), layout, CreateOptions{ID: "S3", Project: "a", Origin: OriginUI})

	if err := SwitchProject(context.Background(), layout, "S3", "b"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	s, _ := Load(layout, "S3")
	if s.Meta.Project != "b" {
		t.Fatalf("project after switch: %q", s.Meta.Project)
	}
}

func TestInvalidID(t *testing.T) {
	layout := newLayout(t)
	_, err := Create(context.Background(), layout, CreateOptions{ID: "../escape"})
	if err == nil {
		t.Fatal("bad id accepted")
	}
}
