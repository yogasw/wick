package omp

import (
	"slices"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestBuildArgsEffort(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeOMP, Name: "acme"}
	got := buildArgs(ins, provider.SpawnOptions{Effort: "max", ResumeID: "r1"}, "", "", []string{"--thinking", "low"})
	i := slices.Index(got, "max")
	if i < 1 || got[i-1] != "--thinking" {
		t.Fatalf("want --thinking max, got %q", got)
	}
	if j := slices.Index(got, "low"); j > i {
		t.Fatalf("the pick must come after the instance's --thinking, got %q", got)
	}
	if slices.Contains(buildArgs(ins, provider.SpawnOptions{}, "", "", nil), "--thinking") {
		t.Fatal("no effort must add no --thinking")
	}
}
