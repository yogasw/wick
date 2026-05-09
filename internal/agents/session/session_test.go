package session

import (
	"context"
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

func TestCreateNoWorkspace(t *testing.T) {
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
	if s.Meta.Workspace != "" {
		t.Fatalf("workspace ref should be empty, got %q", s.Meta.Workspace)
	}
}

func TestWithWorkspace(t *testing.T) {
	layout := newLayout(t)
	if _, err := workspace.Create(layout, workspace.CreateOptions{Name: "frontend"}); err != nil {
		t.Fatal(err)
	}
	s, err := Create(context.Background(), layout, CreateOptions{
		ID:        "T999",
		Workspace: "frontend",
		Origin:    OriginSlack,
	})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if s.Meta.Workspace != "frontend" {
		t.Fatalf("workspace: %q", s.Meta.Workspace)
	}
	// Workspace folder lives under workspaces/, not sessions/.
	if !storage.PathExists(layout.WorkspaceManagedPath("frontend")) {
		t.Fatal("managed workspace files dir missing")
	}
}

func TestCreateUnknownWorkspaceRejected(t *testing.T) {
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{
		ID:        "T_bad",
		Workspace: "ghost",
		Origin:    OriginUI,
	}); err == nil {
		t.Fatal("expected error for unknown workspace")
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
	_, _ = workspace.Create(layout, workspace.CreateOptions{Name: "p"})
	_, _ = Create(context.Background(), layout, CreateOptions{ID: "S2", Workspace: "p", Origin: OriginUI})

	if err := Delete(context.Background(), layout, "S2"); err != nil {
		t.Fatal(err)
	}
	if storage.PathExists(layout.SessionDir("S2")) {
		t.Fatal("session dir still exists")
	}
	// Deleting a session must NOT delete the shared workspace.
	if !storage.PathExists(layout.WorkspaceDir("p")) {
		t.Fatal("workspace deleted by session delete — must stay")
	}
}

func TestSwitchWorkspace(t *testing.T) {
	layout := newLayout(t)
	_, _ = workspace.Create(layout, workspace.CreateOptions{Name: "a"})
	_, _ = workspace.Create(layout, workspace.CreateOptions{Name: "b"})
	_, _ = Create(context.Background(), layout, CreateOptions{ID: "S3", Workspace: "a", Origin: OriginUI})

	if err := SwitchWorkspace(context.Background(), layout, "S3", "b"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	s, _ := Load(layout, "S3")
	if s.Meta.Workspace != "b" {
		t.Fatalf("workspace after switch: %q", s.Meta.Workspace)
	}
}

func TestInvalidID(t *testing.T) {
	layout := newLayout(t)
	_, err := Create(context.Background(), layout, CreateOptions{ID: "../escape"})
	if err == nil {
		t.Fatal("bad id accepted")
	}
}
