package pool

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

func newBufferLayout(t *testing.T, sessID string) config.Layout {
	t.Helper()
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID:     sessID,
		Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestBufferAppendDrain(t *testing.T) {
	layout := newBufferLayout(t, "S1")
	b, err := NewBuffer(layout, "S1")
	if err != nil {
		t.Fatal(err)
	}
	if b.Len() != 0 {
		t.Fatalf("initial len: %d", b.Len())
	}
	if err := b.Append("a"); err != nil {
		t.Fatal(err)
	}
	if err := b.Append("b"); err != nil {
		t.Fatal(err)
	}
	got, err := b.Drain()
	if err != nil {
		t.Fatal(err)
	}
	if got != "a\nb" {
		t.Fatalf("drained: %q", got)
	}
	if b.Len() != 0 {
		t.Fatalf("after drain len: %d", b.Len())
	}
}

func TestBufferPersistsToDisk(t *testing.T) {
	layout := newBufferLayout(t, "S1")
	b, _ := NewBuffer(layout, "S1")
	if err := b.Append("survive me"); err != nil {
		t.Fatal(err)
	}
	sess, _ := session.Load(layout, "S1")
	if len(sess.Meta.PendingInput) != 1 || sess.Meta.PendingInput[0] != "survive me" {
		t.Fatalf("pending_input: %v", sess.Meta.PendingInput)
	}
}

func TestBufferReloadsFromDisk(t *testing.T) {
	layout := newBufferLayout(t, "S1")
	// Pre-populate as if we crashed mid-queue.
	sess, _ := session.Load(layout, "S1")
	sess.Meta.PendingInput = []string{"x", "y"}
	if err := session.SaveMeta(layout, "S1", sess.Meta); err != nil {
		t.Fatal(err)
	}

	b, err := NewBuffer(layout, "S1")
	if err != nil {
		t.Fatal(err)
	}
	if b.Len() != 2 {
		t.Fatalf("reloaded len: %d", b.Len())
	}
	got, _ := b.Drain()
	if got != "x\ny" {
		t.Fatalf("drained: %q", got)
	}
}

func TestBufferDrainEmpty(t *testing.T) {
	layout := newBufferLayout(t, "S1")
	b, _ := NewBuffer(layout, "S1")
	got, err := b.Drain()
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("drained: %q", got)
	}
}
