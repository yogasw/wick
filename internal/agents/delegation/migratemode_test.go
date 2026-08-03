package delegation

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

func seedRoleWithMode(t *testing.T, r *Repo, key, mode string) {
	t.Helper()
	p := &entity.AgentProfile{
		ID: key + "-id", Key: key, Name: key,
		Provider: "claude", DefaultMaxTurns: 12, DefaultMode: mode,
	}
	if err := r.SaveProfile(context.Background(), p); err != nil {
		t.Fatalf("seed %s: %v", key, err)
	}
}

func modeOf(t *testing.T, r *Repo, key string) string {
	t.Helper()
	p, err := r.GetProfileExact(context.Background(), "", key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	return p.DefaultMode
}

// Roles created before the flip carry sync because that was the only value
// create_agent ever wrote. Leaving them alone would apply the new default
// to nothing an existing team actually uses.
func TestMigrateMovesOldRolesToBackground(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	seedRoleWithMode(t, r, "history-player-a", ModeSync)
	seedRoleWithMode(t, r, "already-background", ModeAsync)

	if err := MigrateModeDefault(ctx, r, &memMarker{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if got := modeOf(t, r, "history-player-a"); got != ModeAsync {
		t.Fatalf("old role mode = %q, want async", got)
	}
	if got := modeOf(t, r, "already-background"); got != ModeAsync {
		t.Fatalf("background role changed to %q", got)
	}
}

// The two seeded roles that answer FOR the leader keep blocking: waking a
// leader to read a checker's verdict or a customer draft buys nothing when
// the very next sentence is about that answer.
func TestMigrateKeepsTheCheckerAndDrafterInForeground(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	for _, key := range foregroundRoleKeys {
		seedRoleWithMode(t, r, key, ModeSync)
	}

	if err := MigrateModeDefault(ctx, r, &memMarker{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, key := range foregroundRoleKeys {
		if got := modeOf(t, r, key); got != ModeSync {
			t.Fatalf("%s mode = %q, want sync", key, got)
		}
	}
}

// Once is once: an operator who puts a role back to foreground must keep
// that setting across restarts.
func TestMigrateRunsOnlyOnce(t *testing.T) {
	r := testRepo(t)
	ctx := context.Background()
	marker := &memMarker{}
	seedRoleWithMode(t, r, "researcher", ModeSync)

	if err := MigrateModeDefault(ctx, r, marker); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	seedRoleWithMode(t, r, "researcher", ModeSync) // operator's deliberate choice
	if err := MigrateModeDefault(ctx, r, marker); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got := modeOf(t, r, "researcher"); got != ModeSync {
		t.Fatalf("re-run overwrote an operator's choice: mode = %q", got)
	}
}
