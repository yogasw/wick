package pool

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
)

// TestMarkStatus_RefreshesRegistry reproduces the stale sidebar age: the
// sidebar ages a session from the registry cache once its process leaves the
// pool. markStatus wrote the new LastActive to disk only, so the cache kept the
// old one and the age snapped back to e.g. "4h" right after a turn ended.
func TestMarkStatus_RefreshesRegistry(t *testing.T) {
	p, layout, refreshed := ownerTestPool(t, "s-status")

	if err := p.markStatus("s-status", session.StatusRunning); err != nil {
		t.Fatalf("markStatus running: %v", err)
	}
	if err := p.markStatus("s-status", session.StatusIdle); err != nil {
		t.Fatalf("markStatus idle: %v", err)
	}

	sess, err := session.Load(layout, "s-status")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if sess.Meta.Status != session.StatusIdle {
		t.Fatalf("status on disk = %q, want idle", sess.Meta.Status)
	}
	if len(*refreshed) != 2 || (*refreshed)[0] != "s-status" || (*refreshed)[1] != "s-status" {
		t.Fatalf("registry refresh = %v, want one per status change; without it "+
			"the sidebar keeps the cached LastActive", *refreshed)
	}
}

// TestMarkStatus_SameStatusNoRefresh: an unchanged status writes nothing, so
// there is nothing for the registry to pick up — no spurious refresh/broadcast.
func TestMarkStatus_SameStatusNoRefresh(t *testing.T) {
	p, _, refreshed := ownerTestPool(t, "s-same")

	if err := p.markStatus("s-same", session.StatusRunning); err != nil {
		t.Fatalf("markStatus running: %v", err)
	}
	if err := p.markStatus("s-same", session.StatusRunning); err != nil {
		t.Fatalf("markStatus running again: %v", err)
	}
	if len(*refreshed) != 1 {
		t.Fatalf("registry refresh = %v, want exactly 1 (repeat status is a no-op)", *refreshed)
	}
}
