package project

import (
	"context"
	"os/exec"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
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

func TestCreateNoRepo(t *testing.T) {
	gitAvailable(t)
	layout := newLayout(t)

	p, err := Create(context.Background(), layout, CreateOptions{Name: "frontend"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Meta.DefaultPreset != "default" {
		t.Fatalf("default_preset: %q", p.Meta.DefaultPreset)
	}
	if !storage.PathExists(layout.ProjectWorkspace("frontend")) {
		t.Fatal("workspace not created")
	}
	if !storage.PathExists(layout.ProjectWorkspace("frontend") + "/.git") {
		t.Fatal(".git missing — git init failed silently")
	}
}

func TestDuplicateRejected(t *testing.T) {
	gitAvailable(t)
	layout := newLayout(t)
	if _, err := Create(context.Background(), layout, CreateOptions{Name: "abc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(context.Background(), layout, CreateOptions{Name: "abc"}); err == nil {
		t.Fatal("duplicate create should error")
	}
}

func TestLoadList(t *testing.T) {
	gitAvailable(t)
	layout := newLayout(t)
	for _, n := range []string{"alpha", "beta"} {
		if _, err := Create(context.Background(), layout, CreateOptions{Name: n}); err != nil {
			t.Fatalf("create %s: %v", n, err)
		}
	}
	names, _ := List(layout)
	if len(names) != 2 {
		t.Fatalf("list: %v", names)
	}
	p, err := Load(layout, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "alpha" {
		t.Fatalf("name: %q", p.Name)
	}
}

func TestInvalidName(t *testing.T) {
	layout := newLayout(t)
	_, err := Create(context.Background(), layout, CreateOptions{Name: "../bad"})
	if err == nil {
		t.Fatal("bad name accepted")
	}
}

func TestSaveMeta(t *testing.T) {
	gitAvailable(t)
	layout := newLayout(t)
	p, _ := Create(context.Background(), layout, CreateOptions{Name: "abc"})
	p.Meta.Description = "edited"
	if err := SaveMeta(layout, "abc", p.Meta); err != nil {
		t.Fatal(err)
	}
	p2, _ := Load(layout, "abc")
	if p2.Meta.Description != "edited" {
		t.Fatalf("desc not persisted: %q", p2.Meta.Description)
	}
}
