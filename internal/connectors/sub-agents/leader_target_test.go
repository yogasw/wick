package subagents

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// A leader starts a new tree with every top-level delegate, so the same
// role delegated twice leaves two rows with one handle in different trees.
// The one still working is the one a message means; with none working,
// the newest. Rows arrive newest first (ListByParent orders started_at desc).
func TestLeaderChildPicksTheLiveInstanceAcrossTrees(t *testing.T) {
	rows := []entity.AgentDelegation{
		{ID: "probe", RootID: "probe", Handle: "history-probe", Status: entity.DelegationDone},
		{ID: "impl-new", RootID: "impl-new", Handle: "wick-feature-implementer", Status: entity.DelegationDone},
		{ID: "impl-live", RootID: "impl-live", Handle: "wick-feature-implementer", Status: entity.DelegationRunning},
		{ID: "impl-old", RootID: "impl-old", Handle: "wick-feature-implementer", Status: entity.DelegationDone},
	}
	if got := leaderChild(rows, "wick-feature-implementer"); got == nil || got.ID != "impl-live" {
		t.Fatalf("live instance not picked: %+v", got)
	}
	rows[2].Status = entity.DelegationDone
	if got := leaderChild(rows, "wick-feature-implementer"); got == nil || got.ID != "impl-new" {
		t.Fatalf("newest instance not picked: %+v", got)
	}
	if got := leaderChild(rows, "nobody"); got != nil {
		t.Fatalf("unknown handle resolved to %+v", got)
	}
}
