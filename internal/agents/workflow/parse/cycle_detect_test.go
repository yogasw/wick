package parse

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// helpers

func mkGraph(nodes []string, edges [][2]string) workflow.Graph {
	g := workflow.Graph{}
	for _, id := range nodes {
		g.Nodes = append(g.Nodes, workflow.Node{ID: id})
	}
	for _, e := range edges {
		g.Edges = append(g.Edges, workflow.Edge{From: e[0], To: e[1]})
	}
	return g
}

// --- hasCycle (DFS) tests -------------------------------------------------

// TestHasCycle_LinearGraph checks that a simple chain A→B→C has no cycle.
func TestHasCycle_LinearGraph(t *testing.T) {
	g := mkGraph(
		[]string{"a", "b", "c"},
		[][2]string{{"a", "b"}, {"b", "c"}},
	)
	if hasCycle(g) {
		t.Fatal("hasCycle: expected false for linear graph A→B→C, got true")
	}
}

// TestHasCycle_SelfLoop checks that a self-loop A→A is detected as a cycle.
func TestHasCycle_SelfLoop(t *testing.T) {
	g := mkGraph(
		[]string{"a"},
		[][2]string{{"a", "a"}},
	)
	if !hasCycle(g) {
		t.Fatal("hasCycle: expected true for self-loop A→A, got false")
	}
}

// TestHasCycle_SimpleCycle checks that A→B→A is detected as a cycle.
func TestHasCycle_SimpleCycle(t *testing.T) {
	g := mkGraph(
		[]string{"a", "b"},
		[][2]string{{"a", "b"}, {"b", "a"}},
	)
	if !hasCycle(g) {
		t.Fatal("hasCycle: expected true for cycle A→B→A, got false")
	}
}

// TestHasCycle_Diamond checks that a diamond (A→B, A→C, B→D, C→D) has no cycle.
func TestHasCycle_Diamond(t *testing.T) {
	g := mkGraph(
		[]string{"a", "b", "c", "d"},
		[][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}},
	)
	if hasCycle(g) {
		t.Fatal("hasCycle: expected false for diamond graph, got true")
	}
}

// --- DetectCycle (Kahn's) tests -------------------------------------------

// TestDetectCycle_LinearGraph verifies that a linear chain produces no cycle report.
func TestDetectCycle_LinearGraph(t *testing.T) {
	g := mkGraph(
		[]string{"a", "b", "c"},
		[][2]string{{"a", "b"}, {"b", "c"}},
	)
	if got := DetectCycle(g); got != nil {
		t.Fatalf("DetectCycle: expected nil for linear graph, got %v", got)
	}
}

// TestDetectCycle_SelfLoop verifies that a self-loop is reported.
func TestDetectCycle_SelfLoop(t *testing.T) {
	g := mkGraph(
		[]string{"a"},
		[][2]string{{"a", "a"}},
	)
	if got := DetectCycle(g); got == nil {
		t.Fatal("DetectCycle: expected non-nil for self-loop A→A, got nil")
	}
}

// TestDetectCycle_SimpleCycle verifies A→B→A is reported.
func TestDetectCycle_SimpleCycle(t *testing.T) {
	g := mkGraph(
		[]string{"a", "b"},
		[][2]string{{"a", "b"}, {"b", "a"}},
	)
	if got := DetectCycle(g); got == nil {
		t.Fatal("DetectCycle: expected non-nil for cycle A→B→A, got nil")
	}
}

// TestDetectCycle_Diamond verifies a diamond graph produces no cycle report.
func TestDetectCycle_Diamond(t *testing.T) {
	g := mkGraph(
		[]string{"a", "b", "c", "d"},
		[][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}},
	)
	if got := DetectCycle(g); got != nil {
		t.Fatalf("DetectCycle: expected nil for diamond graph, got %v", got)
	}
}

// TestBothDetectors_Agree verifies that hasCycle and DetectCycle always agree.
func TestBothDetectors_Agree(t *testing.T) {
	cases := []struct {
		name  string
		g     workflow.Graph
		cycle bool
	}{
		{"empty", mkGraph(nil, nil), false},
		{"single node", mkGraph([]string{"a"}, nil), false},
		{"linear", mkGraph([]string{"a", "b", "c"}, [][2]string{{"a", "b"}, {"b", "c"}}), false},
		{"self-loop", mkGraph([]string{"a"}, [][2]string{{"a", "a"}}), true},
		{"two-cycle", mkGraph([]string{"a", "b"}, [][2]string{{"a", "b"}, {"b", "a"}}), true},
		{"diamond", mkGraph([]string{"a", "b", "c", "d"}, [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}}), false},
		{"longer cycle", mkGraph([]string{"a", "b", "c", "d"}, [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d", "b"}}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dfs := hasCycle(tc.g)
			kahn := DetectCycle(tc.g) != nil
			if dfs != tc.cycle {
				t.Errorf("hasCycle(%s) = %v; want %v", tc.name, dfs, tc.cycle)
			}
			if kahn != tc.cycle {
				t.Errorf("DetectCycle(%s) = cycle:%v; want %v", tc.name, kahn, tc.cycle)
			}
		})
	}
}
