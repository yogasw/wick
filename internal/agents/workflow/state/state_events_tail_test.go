package state

import (
	"os"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
)

func seedEvents(t *testing.T, s *FileStore, id, runID string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		ev := workflow.RunEvent{Event: workflow.EventNodeCompleted, Node: string(rune('a' + i%26))}
		if err := s.AppendEvent(id, runID, ev); err != nil {
			t.Fatalf("append event %d: %v", i, err)
		}
	}
}

func TestListEventsTail_ReturnsLastNInOrder(t *testing.T) {
	s := New(config.Layout{BaseDir: t.TempDir()})
	id, runID := "wf1", "run-1"
	seedEvents(t, s, id, runID, 10)

	all, err := s.ListEvents(id, runID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(all) != 10 {
		t.Fatalf("ListEvents should return all 10, got %d", len(all))
	}

	events, total, err := s.ListEventsTail(id, runID, 3)
	if err != nil {
		t.Fatalf("ListEventsTail: %v", err)
	}
	if total != 10 {
		t.Fatalf("total = %d, want 10", total)
	}
	if len(events) != 3 {
		t.Fatalf("tail len = %d, want 3", len(events))
	}
	want := []string{all[7].Node, all[8].Node, all[9].Node}
	for i, e := range events {
		if e.Node != want[i] {
			t.Fatalf("tail[%d].Node = %q, want %q (chronological last-3)", i, e.Node, want[i])
		}
	}
}

func TestListEventsTail_LimitZeroReturnsAll(t *testing.T) {
	s := New(config.Layout{BaseDir: t.TempDir()})
	id, runID := "wf1", "run-1"
	seedEvents(t, s, id, runID, 5)

	for _, limit := range []int{0, -1} {
		events, total, err := s.ListEventsTail(id, runID, limit)
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		if total != 5 || len(events) != 5 {
			t.Fatalf("limit %d: events=%d total=%d, want 5/5", limit, len(events), total)
		}
	}
}

func TestListEventsTail_LimitBeyondTotal(t *testing.T) {
	s := New(config.Layout{BaseDir: t.TempDir()})
	id, runID := "wf1", "run-1"
	seedEvents(t, s, id, runID, 3)

	events, total, err := s.ListEventsTail(id, runID, 50)
	if err != nil {
		t.Fatalf("ListEventsTail: %v", err)
	}
	if total != 3 || len(events) != 3 {
		t.Fatalf("events=%d total=%d, want 3/3", len(events), total)
	}
}

func TestListEventsTail_MissingFile(t *testing.T) {
	s := New(config.Layout{BaseDir: t.TempDir()})
	events, total, err := s.ListEventsTail("nope", "nope", 10)
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if total != 0 || len(events) != 0 {
		t.Fatalf("missing file: events=%d total=%d, want 0/0", len(events), total)
	}
}

func TestListEventsTail_SkipsCorruptLines(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	s := New(layout)
	id, runID := "wf1", "run-1"
	seedEvents(t, s, id, runID, 2)

	f, err := os.OpenFile(layout.WorkflowRunEvents(id, runID), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := f.WriteString("{not json}\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()
	seedEvents(t, s, id, runID, 1)

	events, total, err := s.ListEventsTail(id, runID, 10)
	if err != nil {
		t.Fatalf("ListEventsTail: %v", err)
	}
	if total != 3 || len(events) != 3 {
		t.Fatalf("corrupt line must be skipped: events=%d total=%d, want 3/3", len(events), total)
	}
}
