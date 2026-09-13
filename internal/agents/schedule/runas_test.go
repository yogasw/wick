package schedule

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// A project-scoped fire must stamp the schedule's owner onto the session it
// mints. Without it the session is ownerless, no per-user MCP credential is
// minted, and the spawn runs as the synthetic internal principal — which
// carries no access tags, so the job quietly sees far less than the person who
// created it. That was the bug: a report that worked by hand came back empty
// on a timer.
func TestRunner_ProjectScopedFireRunsAsOwner(t *testing.T) {
	s := newTestStore(t)
	layout, _ := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		ProjectID:   "proj-1",
		SessionMode: entity.ScheduledSessionNew,
		OwnerUserID: "user-ada",
		Message:     "weekly report",
		RunAt:       time.Now().Add(-time.Second),
	})
	r.tick(ctx, zerologLogger{})

	target := "sch-" + shortScheduleID(m.ID) + "-1"
	sess, err := session.Load(layout, target)
	if err != nil {
		t.Fatalf("load minted session: %v", err)
	}
	if sess.Meta.UserID != "user-ada" {
		t.Fatalf("session owner = %q, want user-ada — an ownerless fire falls back to the internal principal", sess.Meta.UserID)
	}
}

// The admin override wins over the owner. This is the only way to widen a
// schedule, so it has to actually take effect.
func TestRunner_RunAsOverridesOwner(t *testing.T) {
	s := newTestStore(t)
	layout, _ := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		ProjectID:   "proj-1",
		SessionMode: entity.ScheduledSessionNew,
		OwnerUserID: "user-ada",
		RunAsUserID: "user-bob",
		Message:     "weekly report",
		RunAt:       time.Now().Add(-time.Second),
	})
	r.tick(ctx, zerologLogger{})

	target := "sch-" + shortScheduleID(m.ID) + "-1"
	sess, _ := session.Load(layout, target)
	if sess.Meta.UserID != "user-bob" {
		t.Fatalf("session owner = %q, want user-bob", sess.Meta.UserID)
	}
}

// Pointing a schedule at a session somebody else already owns must NOT hand
// that session's identity over. Ownership decides whose access every later
// spawn runs with, so a schedule that could re-stamp it would be a way to move
// a whole conversation's access sideways.
func TestRunner_ExistingSessionKeepsItsOwner(t *testing.T) {
	s := newTestStore(t)
	layout, _ := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout)
	ctx := context.Background()

	sess, _ := session.Load(layout, "sess-live")
	sess.Meta.UserID = "user-ada"
	if err := session.SaveMeta(layout, "sess-live", sess.Meta); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	if _, err := s.Create(ctx, &entity.ScheduledMessage{
		SessionID:   "sess-live",
		RunAsUserID: "user-mallory",
		Message:     "nudge",
		RunAt:       time.Now().Add(-time.Second),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	r.tick(ctx, zerologLogger{})

	after, _ := session.Load(layout, "sess-live")
	if after.Meta.UserID != "user-ada" {
		t.Fatalf("session owner = %q, want user-ada — a schedule must not take over an owned session", after.Meta.UserID)
	}
}

// A run-as user who has been removed or un-approved stops the schedule. The
// tempting alternative — carry on without them — is exactly the failure this
// change exists to remove: the fire would silently run as the internal
// principal, under an identity nobody chose, on a timer, forever.
func TestRunner_RevokedRunAsUserFailsInsteadOfDowngrading(t *testing.T) {
	s := newTestStore(t)
	layout, _ := newRunnerLayout(t)
	sender := &fakeSender{layout: layout}
	r := NewRunner(s, sender, layout).WithRunAsCheck(func(userID string) bool {
		return userID == "user-ada" // bob has been disabled
	})
	ctx := context.Background()

	m, _ := s.Create(ctx, &entity.ScheduledMessage{
		ProjectID:   "proj-1",
		SessionMode: entity.ScheduledSessionNew,
		OwnerUserID: "user-ada",
		RunAsUserID: "user-bob",
		Message:     "weekly report",
		RunAt:       time.Now().Add(-time.Second),
	})
	r.tick(ctx, zerologLogger{})

	if len(sender.calls) != 0 {
		t.Fatalf("delivered %v; a revoked run-as user must stop the fire", sender.calls)
	}
	got, _ := s.Get(ctx, m.ID)
	if got.Status != entity.ScheduledStatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !strings.Contains(got.LastError, "user-bob") {
		t.Fatalf("last_error = %q, want it to name the user", got.LastError)
	}
}

// EffectiveRunAsUser is what every surface should read. Empty means the
// schedule is attached to nobody — the state that needs to be visible rather
// than inferred from a missing field.
func TestEffectiveRunAsUser(t *testing.T) {
	cases := []struct {
		name  string
		owner string
		runAs string
		want  string
	}{
		{"owner only", "user-ada", "", "user-ada"},
		{"override wins", "user-ada", "user-bob", "user-bob"},
		{"attached to nobody", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := entity.ScheduledMessage{OwnerUserID: tc.owner, RunAsUserID: tc.runAs}
			if got := m.EffectiveRunAsUser(); got != tc.want {
				t.Fatalf("EffectiveRunAsUser() = %q, want %q", got, tc.want)
			}
		})
	}
}
