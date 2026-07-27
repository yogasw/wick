package agents

import (
	"testing"

	wick "github.com/yogasw/wick/internal/agents/provider/wick"
)

func dm(ids ...string) []wick.DiscoveredModel {
	out := make([]wick.DiscoveredModel, 0, len(ids))
	for _, id := range ids {
		out = append(out, wick.DiscoveredModel{ID: id})
	}
	return out
}

func TestMarkLiveSetDefault(t *testing.T) {
	// No pin → top of list is default.
	got := markLiveSetDefault(dm("opus", "sonnet", "haiku"), "")
	if !got[0].Default || got[1].Default || got[2].Default {
		t.Errorf("no pin: expected top default, got %+v", got)
	}

	// Pin present → that row is default, not the top.
	got = markLiveSetDefault(dm("opus", "sonnet", "haiku"), "sonnet")
	if got[0].Default || !got[1].Default {
		t.Errorf("pin present: expected sonnet default, got %+v", got)
	}

	// Pin missing (vanished from fresh list) → auto re-select top.
	got = markLiveSetDefault(dm("opus", "haiku"), "sonnet")
	if !got[0].Default {
		t.Errorf("pin vanished: expected fallback to top, got %+v", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Default {
			t.Errorf("pin vanished: only top should be default, got %+v", got)
		}
	}

	// Empty list → no panic, empty out.
	if out := markLiveSetDefault(nil, "x"); len(out) != 0 {
		t.Errorf("empty: expected no choices, got %+v", out)
	}
}
