package session

import (
	"context"
	"testing"
)

func TestSetEffort(t *testing.T) {
	layout := newLayout(t)
	_, _ = Create(context.Background(), layout, CreateOptions{ID: "S1", Origin: OriginUI})
	if err := SetEffort(layout, "S1", "main", "high"); err != nil {
		t.Fatalf("set (create): %v", err)
	}
	s, _ := Load(layout, "S1")
	if len(s.Agents) != 1 || s.Agents[0].Effort != "high" {
		t.Fatalf("want Effort=high, got %+v", s.Agents)
	}
	if err := SetEffort(layout, "S1", "main", "default"); err != nil {
		t.Fatal(err)
	}
	if err := SetEffort(layout, "S1", "main", ""); err != nil {
		t.Fatal(err)
	}
	s, _ = Load(layout, "S1")
	if len(s.Agents) != 1 || s.Agents[0].Effort != "" {
		t.Fatalf("clearing must leave one agent with no effort, got %+v", s.Agents)
	}
	if err := SetEffort(layout, "S1", "", "high"); err != ErrNoAgentName {
		t.Fatalf("empty agent name: err=%v", err)
	}
}
