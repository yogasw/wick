package delegation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/entity"
)

func testRepo(t *testing.T) *Repo {
	t.Helper()
	// A named shared-cache DB rather than plain ":memory:": the latter
	// gives every pooled connection its own empty database, so a query
	// issued from another goroutine finds no tables. Unique name per
	// test keeps them isolated from one another.
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql handle: %v", err)
	}
	// Keep at least one connection alive for the test's duration —
	// a shared-cache memory DB is dropped when the last one closes.
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(
		&entity.AgentProfile{}, &entity.AgentDelegation{},
		&entity.AgentSquad{}, &entity.AgentBoard{}, &entity.AgentTask{},
		&entity.AgentMessage{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewRepo(db)
}

func seedProfile(t *testing.T, r *Repo, key string) *entity.AgentProfile {
	t.Helper()
	p := &entity.AgentProfile{
		ID: key + "-id", Key: key, Name: key,
		Provider: "claude", DefaultMaxTurns: 12,
	}
	if err := r.SaveProfile(context.Background(), p); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	return p
}

func seedDelegation(t *testing.T, r *Repo, id, rootID, status string, turns int) *entity.AgentDelegation {
	t.Helper()
	d := &entity.AgentDelegation{
		ID: id, RootID: rootID, ParentSessionID: "parent", ProfileKey: "researcher",
		ChildSessionID: "sub-" + id, ChildAgent: "claude",
		Status: status, TurnsUsed: turns, TriggeredBy: "user-1",
		StartedAt: time.Now().UTC(),
	}
	if err := r.Create(context.Background(), d); err != nil {
		t.Fatalf("seed delegation: %v", err)
	}
	return d
}

func asRefusal(t *testing.T, err error) *Refusal {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("expected *Refusal, got %v", err)
	}
	return r
}

func TestAdmitRejectsBeyondMaxDepth(t *testing.T) {
	r := testRepo(t)
	p := seedProfile(t, r, "researcher")
	lim := DefaultLimits()

	if err := r.Admit(context.Background(), lim, p, "", lim.MaxDepth, nil); err != nil {
		t.Fatalf("depth at the limit must be allowed: %v", err)
	}
	err := r.Admit(context.Background(), lim, p, "", lim.MaxDepth+1, nil)
	if got := asRefusal(t, err).Reason; got != RefusedDepth {
		t.Fatalf("reason = %q, want %q", got, RefusedDepth)
	}
}

// A zero-valued limits struct must fall back to defaults, never to
// "unlimited" — a half-filled config row must not silently disable a brake.
func TestZeroLimitsFallBackToDefaultsNotUnlimited(t *testing.T) {
	r := testRepo(t)
	p := seedProfile(t, r, "researcher")

	err := r.Admit(context.Background(), Limits{}, p, "", DefaultMaxDepth+1, nil)
	if err == nil {
		t.Fatal("zero-valued Limits disabled the depth brake")
	}
	if got := asRefusal(t, err).Reason; got != RefusedDepth {
		t.Fatalf("reason = %q, want %q", got, RefusedDepth)
	}
}

func TestAdmitRejectsCycle(t *testing.T) {
	r := testRepo(t)
	p := seedProfile(t, r, "researcher")

	err := r.Admit(context.Background(), DefaultLimits(), p, "", 1, []string{"coder", "researcher"})
	if got := asRefusal(t, err).Reason; got != RefusedCycle {
		t.Fatalf("reason = %q, want %q", got, RefusedCycle)
	}
	if err := r.Admit(context.Background(), DefaultLimits(), p, "", 1, []string{"coder"}); err != nil {
		t.Fatalf("non-cyclic chain must be allowed: %v", err)
	}
}

func TestAdmitRejectsWhenRootBudgetExhausted(t *testing.T) {
	r := testRepo(t)
	p := seedProfile(t, r, "researcher")
	lim := DefaultLimits()
	lim.RootBudget = 10

	seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 6)
	seedDelegation(t, r, "d2", "root-1", entity.DelegationDone, 4)

	err := r.Admit(context.Background(), lim, p, "root-1", 1, nil)
	ref := asRefusal(t, err)
	if ref.Reason != RefusedBudget {
		t.Fatalf("reason = %q, want %q", ref.Reason, RefusedBudget)
	}
	// Budget exhaustion is its own terminal status, distinct from a
	// generic failure, so operators can tell a ceiling from an error.
	if got := ref.Status(); got != entity.DelegationStoppedBudget {
		t.Fatalf("status = %q, want %q", got, entity.DelegationStoppedBudget)
	}
}

func TestAdmitRejectsBeyondMaxParallel(t *testing.T) {
	r := testRepo(t)
	p := seedProfile(t, r, "researcher")
	lim := DefaultLimits()
	lim.MaxParallel = 2

	seedDelegation(t, r, "d1", "root-1", entity.DelegationRunning, 0)
	seedDelegation(t, r, "d2", "root-1", entity.DelegationQueued, 0)

	err := r.Admit(context.Background(), lim, p, "root-1", 1, nil)
	if got := asRefusal(t, err).Reason; got != RefusedParallel {
		t.Fatalf("reason = %q, want %q", got, RefusedParallel)
	}
}

// Finished siblings must free their parallel slot, otherwise a tree
// wedges permanently after max_parallel total delegations.
func TestTerminalSiblingsDoNotConsumeParallelSlots(t *testing.T) {
	r := testRepo(t)
	p := seedProfile(t, r, "researcher")
	lim := DefaultLimits()
	lim.MaxParallel = 2

	seedDelegation(t, r, "d1", "root-1", entity.DelegationDone, 1)
	seedDelegation(t, r, "d2", "root-1", entity.DelegationInterrupted, 1)
	seedDelegation(t, r, "d3", "root-1", entity.DelegationRunning, 0)

	if err := r.Admit(context.Background(), lim, p, "root-1", 1, nil); err != nil {
		t.Fatalf("terminal siblings must not hold slots: %v", err)
	}
}

func TestKillSwitchRefusesEverything(t *testing.T) {
	r := testRepo(t)
	p := seedProfile(t, r, "researcher")
	lim := DefaultLimits()
	lim.Disabled = true

	err := r.Admit(context.Background(), lim, p, "", 0, nil)
	if got := asRefusal(t, err).Reason; got != RefusedDisabled {
		t.Fatalf("reason = %q, want %q", got, RefusedDisabled)
	}
}

func TestEffectiveMaxTurnsPrecedenceAndClamp(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxTurnsCap = 20
	p := &entity.AgentProfile{DefaultMaxTurns: 8}

	tests := []struct {
		name      string
		requested int
		profile   *entity.AgentProfile
		want      int
	}{
		{"caller request wins", 5, p, 5},
		{"falls back to profile default", 0, p, 8},
		{"falls back to global default", 0, &entity.AgentProfile{}, DefaultMaxTurns},
		{"clamped to ceiling, not rejected", 999, p, 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveMaxTurns(tc.requested, tc.profile, lim); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAdmitMessage(t *testing.T) {
	lim := Limits{MaxHops: 3}
	if err := AdmitMessage(lim, 2); err != nil {
		t.Fatalf("hop 2 of 3 must be allowed: %v", err)
	}
	err := AdmitMessage(lim, 3)
	if err == nil {
		t.Fatal("hop 3 of 3 must be refused")
	}
	var ref *Refusal
	if !errors.As(err, &ref) || ref.Reason != RefusedHops {
		t.Fatalf("want RefusedHops refusal, got %T %v", err, err)
	}
	// A refusal that only says no leaves the agent looping on retries.
	if !strings.Contains(ref.Message, "report back to the user") {
		t.Fatalf("refusal gives no next step: %q", ref.Message)
	}
}

func TestAdmitMessageRespectsKillSwitch(t *testing.T) {
	if err := AdmitMessage(Limits{Disabled: true, MaxHops: 10}, 0); err == nil {
		t.Fatal("the kill-switch must stop messages too, not only spawns")
	}
}

// A zero hop cap from a half-filled config row must mean "use the
// default", never "unlimited" — the same rule the other brakes follow.
func TestZeroHopCapFallsBackToTheDefault(t *testing.T) {
	if err := AdmitMessage(Limits{MaxHops: 0}, DefaultMaxHops); err == nil {
		t.Fatal("a zero cap must normalize to the default, not disable the brake")
	}
}

func TestConfigDefaultMatchesGovernorDefault(t *testing.T) {
	if config.DefaultGeneralConfig().SubAgentsMaxHops != DefaultMaxHops {
		t.Fatalf("config default %d drifted from governor default %d",
			config.DefaultGeneralConfig().SubAgentsMaxHops, DefaultMaxHops)
	}
}
