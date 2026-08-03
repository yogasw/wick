package delegation

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// The precedence rule the whole router rests on: an agent already doing
// the work beats spawning a second one of the same role.
func TestResolvePrefersLiveAgentOverRole(t *testing.T) {
	r := &Resolver{
		handles: map[string]bool{"reviewer": true, "reviewer-2": true},
		roles:   map[string]bool{"reviewer": true, "researcher": true},
	}

	if got := r.Resolve("reviewer"); got.Kind != TargetAgent || got.Handle != "reviewer" {
		t.Fatalf("reviewer → %+v, want a live agent", got)
	}
	if got := r.Resolve("researcher"); got.Kind != TargetRole || got.Role != "researcher" {
		t.Fatalf("researcher → %+v, want a role", got)
	}
	if got := r.Resolve("nobody"); got.Kind != TargetUnknown {
		t.Fatalf("nobody → %+v, want unknown", got)
	}
}

func TestNewResolverExcludesTerminalHandlesAndDisabledRoles(t *testing.T) {
	s, repo := serialService(t, &scriptedStream{})
	ctx := context.Background()

	live := seedDelegation(t, repo, "d1", "root-1", entity.DelegationRunning, 0)
	live.Handle = "worker"
	if err := repo.SaveDelegationForTest(ctx, live); err != nil {
		t.Fatalf("seed live: %v", err)
	}
	gone := seedDelegation(t, repo, "d2", "root-1", entity.DelegationDone, 1)
	gone.Handle = "worker-2"
	if err := repo.SaveDelegationForTest(ctx, gone); err != nil {
		t.Fatalf("seed gone: %v", err)
	}
	ghost := seedProfile(t, repo, "ghost")
	ghost.Disabled = true
	if err := repo.SaveProfile(ctx, ghost); err != nil {
		t.Fatalf("disable ghost: %v", err)
	}

	res, err := s.NewResolver(ctx, "root-1", "")
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}

	if got := res.Resolve("worker"); got.Kind != TargetAgent {
		t.Fatalf("worker → %+v, want a live agent", got)
	}
	// A finished instance has no process; a message to it would queue for
	// a reader that never comes.
	if got := res.Resolve("worker-2"); got.Kind != TargetUnknown {
		t.Fatalf("worker-2 → %+v, want unknown (its agent is gone)", got)
	}
	if got := res.Resolve("researcher"); got.Kind != TargetRole {
		t.Fatalf("researcher → %+v, want a role", got)
	}
	if got := res.Resolve("ghost"); got.Kind != TargetUnknown {
		t.Fatalf("ghost → %+v, want unknown (the role is disabled)", got)
	}
}

// The conversation owner must always be addressable, or a sub-agent has
// no way to report back without discovering an address first.
func TestResolverAlwaysCarriesTheLeaderHandle(t *testing.T) {
	s, repo := serialService(t, &scriptedStream{})
	ctx := context.Background()
	seedDelegation(t, repo, "d1", "root-1", entity.DelegationRunning, 0)

	res, err := s.NewResolver(ctx, "root-1", "")
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if got := res.Resolve(entity.LeaderHandle); got.Kind != TargetAgent {
		t.Fatalf("%s → %+v, want a live agent", entity.LeaderHandle, got)
	}
}

// A conversation that has never delegated has no tree; every mention in
// it can only be a role.
func TestNewResolverWithoutATreeStillOffersRoles(t *testing.T) {
	s, _ := serialService(t, &scriptedStream{})

	res, err := s.NewResolver(context.Background(), "", "")
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if got := res.Resolve("researcher"); got.Kind != TargetRole {
		t.Fatalf("researcher → %+v, want a role", got)
	}
	if got := res.Resolve(entity.LeaderHandle); got.Kind != TargetUnknown {
		t.Fatalf("leader handle resolved with no tree: %+v", got)
	}
}

// SpawnableRoles must not offer a role that already has a live instance —
// that is the case the router turns into a message, not a spawn.
func TestSpawnableRolesExcludeLiveOnes(t *testing.T) {
	r := &Resolver{
		handles:     map[string]bool{"reviewer": true},
		roles:       map[string]bool{"reviewer": true, "researcher": true},
		handleOrder: []string{"reviewer"},
		roleOrder:   []string{"researcher", "reviewer"},
	}
	got := r.SpawnableRoles()
	if len(got) != 1 || got[0] != "researcher" {
		t.Fatalf("spawnable = %v, want [researcher]", got)
	}
	// AllNames must not list a name twice, or ParseMentions does needless
	// duplicate work and the roster block reads oddly.
	names := r.AllNames()
	if len(names) != 2 {
		t.Fatalf("names = %v, want two distinct entries", names)
	}
}
