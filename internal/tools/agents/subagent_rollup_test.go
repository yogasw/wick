package agents

import "testing"

// rollUpSubAgentWork attributes a child's liveness to the top-level
// conversation that owns it. Sub-agents nest (a sub-agent can delegate
// again), and only the root has a sidebar row, so the walk has to climb
// past intermediate children rather than stop at the first parent.
func TestRollUpSubAgentWork(t *testing.T) {
	// root ── childA ── grandchild
	//      └─ childB
	parents := map[string]string{
		"childA":     "root",
		"childB":     "root",
		"grandchild": "childA",
	}

	cases := []struct {
		name string
		live map[string]string // sessionID → lifecycle, as the pool reports it
		want map[string]string // rolled up onto root sessions
	}{
		{
			name: "direct child working reaches the root",
			live: map[string]string{"childA": "working"},
			want: map[string]string{"root": "working"},
		},
		{
			// The bug this exists for: a nested child's work has to climb
			// two levels, because childA has no sidebar row either.
			name: "nested child working climbs to the root",
			live: map[string]string{"grandchild": "working"},
			want: map[string]string{"root": "working"},
		},
		{
			name: "spawning counts as work",
			live: map[string]string{"childB": "spawning"},
			want: map[string]string{"root": "spawning"},
		},
		{
			// An idle child is a live process that is not doing anything;
			// reporting it would light the row up permanently.
			name: "idle child is not work",
			live: map[string]string{"childA": "idle"},
			want: map[string]string{},
		},
		{
			// Working beats spawning beats nothing, so one busy child is
			// not masked by a quieter sibling regardless of map order.
			name: "working wins over a spawning sibling",
			live: map[string]string{"childA": "spawning", "childB": "working"},
			want: map[string]string{"root": "working"},
		},
		{
			// The leader's own process is not a sub-agent of itself.
			name: "root's own lifecycle is not rolled up",
			live: map[string]string{"root": "working"},
			want: map[string]string{},
		},
		{
			name: "nothing live",
			live: map[string]string{},
			want: map[string]string{},
		},
		{
			// A child whose parent is unknown (registry evicted it, or a
			// stale row) must not panic or invent a root.
			name: "orphaned child is dropped",
			live: map[string]string{"ghost": "working"},
			want: map[string]string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := rollUpSubAgentWork(tc.live, func(id string) (string, bool) {
				p, ok := parents[id]
				return p, ok || id == "root"
			})
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("root %q = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

// A cycle can only come from corrupt metadata, but it must not hang the
// page render.
func TestRollUpSubAgentWorkSurvivesCycle(t *testing.T) {
	parents := map[string]string{"a": "b", "b": "a"}
	done := make(chan map[string]string, 1)
	go func() {
		done <- rollUpSubAgentWork(
			map[string]string{"a": "working"},
			func(id string) (string, bool) { p, ok := parents[id]; return p, ok },
		)
	}()
	select {
	case got := <-done:
		if len(got) != 0 {
			t.Errorf("a cycle has no root, got %v", got)
		}
	default:
		// Fall through to a blocking receive so a hang fails via the test
		// timeout rather than a flaky race on scheduling.
		if got := <-done; len(got) != 0 {
			t.Errorf("a cycle has no root, got %v", got)
		}
	}
}
