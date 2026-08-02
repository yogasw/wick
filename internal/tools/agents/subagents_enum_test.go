package agents

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// The TypeScript SubAgentStatus union and the Go status list must stay
// identical. This is the exact failure multica shipped: their DB allowed
// eight statuses, their TS union listed seven, and the missing one
// rendered as the wrong label in the UI forever. Comparing the two lists
// in a test is the cheap fix.
func TestSubAgentStatusUnionMatchesGo(t *testing.T) {
	path := filepath.Join("..", "..", "..", "fe", "agents", "conversation", "src", "lib", "types", "agents.ts")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("front-end types not available: %v", err)
	}

	block := regexp.MustCompile(`(?s)export type SubAgentStatus\s*=(.*?);`).FindSubmatch(src)
	if block == nil {
		t.Fatal("SubAgentStatus union not found in agents.ts — did it get renamed?")
	}
	var ts []string
	for _, m := range regexp.MustCompile(`"([a-z_]+)"`).FindAllSubmatch(block[1], -1) {
		ts = append(ts, string(m[1]))
	}

	got := append([]string{}, ts...)
	want := append([]string{}, entity.DelegationStatuses...)
	sort.Strings(got)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("status count differs:\n  TS = %v\n  Go = %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("status lists differ:\n  TS = %v\n  Go = %v", got, want)
		}
	}
}

func TestSubAgentStatusesJSONExposesEveryStatus(t *testing.T) {
	if len(subAgentStatusesJSON()) != len(entity.DelegationStatuses) {
		t.Fatal("subAgentStatusesJSON drifted from entity.DelegationStatuses")
	}
}
