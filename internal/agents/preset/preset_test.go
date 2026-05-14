package preset

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
)

func newLayout(t *testing.T) config.Layout {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestCRUD(t *testing.T) {
	layout := newLayout(t)

	if err := Create(layout, "backend", "# backend agent\n"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := Create(layout, "backend", "x"); err == nil {
		t.Fatal("duplicate create should error")
	}

	p, err := Load(layout, "backend")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if p.Body != "# backend agent\n" {
		t.Fatalf("body mismatch: %q", p.Body)
	}

	if err := Update(layout, "backend", "# updated\n"); err != nil {
		t.Fatalf("update: %v", err)
	}
	p, _ = Load(layout, "backend")
	if p.Body != "# updated\n" {
		t.Fatalf("after update: %q", p.Body)
	}

	names, err := List(layout)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "backend" {
		t.Fatalf("list: %v", names)
	}

	if err := Delete(layout, "backend"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	names, _ = List(layout)
	if len(names) != 0 {
		t.Fatalf("after delete: %v", names)
	}
}

func TestEnsureDefault(t *testing.T) {
	layout := newLayout(t)
	if err := EnsureDefault(layout); err != nil {
		t.Fatal(err)
	}
	p, err := Load(layout, "default")
	if err != nil {
		t.Fatalf("load default: %v", err)
	}
	if p.Body == "" {
		t.Fatal("default preset body empty")
	}
	body := p.Body
	if err := EnsureDefault(layout); err != nil {
		t.Fatal(err)
	}
	p, _ = Load(layout, "default")
	if p.Body != body {
		t.Fatal("second EnsureDefault clobbered body")
	}
}

func TestDeleteDefaultBlocked(t *testing.T) {
	layout := newLayout(t)
	if err := EnsureDefault(layout); err != nil {
		t.Fatal(err)
	}
	if err := Delete(layout, DefaultName); err == nil {
		t.Fatal("deleting default preset should error")
	}
	if _, err := Load(layout, DefaultName); err != nil {
		t.Fatalf("default still present after blocked delete: %v", err)
	}
}

func TestInvalidName(t *testing.T) {
	layout := newLayout(t)
	if err := Create(layout, "../escape", "x"); err == nil {
		t.Fatal("path traversal name should error")
	}
}
