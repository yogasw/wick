package handlers

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// Moving a schedule between session and project scope used to be refused,
// while the tool description advertised it as allowed — a doc/behavior
// mismatch. It is now allowed, and these tests pin the two things that make it
// safe: the new target is authorized, and ownership follows the new scope.

func scopeMoveFixture(t *testing.T) (agentconfig.Layout, string, string) {
	t.Helper()
	layout := agentconfig.NewLayout(t.TempDir())
	sess, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "sess-1", Origin: session.OriginUI, UserID: "u1",
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	p, err := project.Create(layout, project.CreateOptions{
		ID: "proj-1", Name: "Reports", OwnerUserID: "u1",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return layout, sess.ID, p.ID()
}

func TestScheduleTargetPatch_SessionToProject(t *testing.T) {
	layout, sid, pid := scopeMoveFixture(t)
	user := &entity.User{ID: "u1"}
	r := httptest.NewRequest("POST", "/mcp", nil)

	row := entity.ScheduledMessage{
		ID: "sm_1", SessionID: sid, SessionMode: entity.ScheduledSessionExisting,
		OwnerUserID: "u1", Kind: entity.ScheduledKindRecurring,
	}
	var patch schedule.SchedulePatch
	// Naming only a project_id is enough — the mode is inferred as "new",
	// the same rule create uses.
	args := map[string]any{"project_id": pid}
	if err := scheduleTargetPatch(r, layout, user, row, args, time.Now(), &patch); err != nil {
		t.Fatalf("session→project move refused: %v", err)
	}
	if patch.SessionMode == nil || *patch.SessionMode != entity.ScheduledSessionNew {
		t.Fatalf("mode = %v, want new", patch.SessionMode)
	}
	if patch.ProjectID == nil || *patch.ProjectID != pid {
		t.Fatalf("project = %v, want %q", patch.ProjectID, pid)
	}
	// A project-scoped row has no fixed target session.
	if patch.SessionID == nil || *patch.SessionID != "" {
		t.Fatalf("session_id = %v, want cleared", patch.SessionID)
	}
	// Ownership must follow the new scope or the row drops out of its lists.
	if patch.OwnerUserID == nil {
		t.Fatal("owner not re-stamped on a scope move")
	}
}

func TestScheduleTargetPatch_ProjectToSession(t *testing.T) {
	layout, sid, pid := scopeMoveFixture(t)
	user := &entity.User{ID: "u1"}
	r := httptest.NewRequest("POST", "/mcp", nil)

	row := entity.ScheduledMessage{
		ID: "sm_2", ProjectID: pid, SessionMode: entity.ScheduledSessionNew,
		SessionTemplate: "daily-{date}", OwnerUserID: "u1",
	}
	var patch schedule.SchedulePatch
	args := map[string]any{"session_id": sid}
	if err := scheduleTargetPatch(r, layout, user, row, args, time.Now(), &patch); err != nil {
		t.Fatalf("project→session move refused: %v", err)
	}
	if patch.SessionMode == nil || *patch.SessionMode != entity.ScheduledSessionExisting {
		t.Fatalf("mode = %v, want existing", patch.SessionMode)
	}
	if patch.SessionID == nil || *patch.SessionID != sid {
		t.Fatalf("session_id = %v, want %q", patch.SessionID, sid)
	}
	// Session scope carries no project/template config; a leftover template
	// would resurrect if the row were later moved back.
	if patch.ProjectID == nil || *patch.ProjectID != "" {
		t.Fatalf("project = %v, want cleared", patch.ProjectID)
	}
	if patch.SessionTemplate == nil || *patch.SessionTemplate != "" {
		t.Fatalf("template = %v, want cleared", patch.SessionTemplate)
	}
	if patch.OwnerUserID == nil || *patch.OwnerUserID != "u1" {
		t.Fatalf("owner = %v, want the session's owner u1", patch.OwnerUserID)
	}
}

func TestScheduleTargetPatch_UnreachableTargetRefused(t *testing.T) {
	layout, sid, _ := scopeMoveFixture(t)
	// A different, non-admin user: the project is owned by u1 and carries no
	// tags, so u2 cannot reach it.
	other := &entity.User{ID: "u2"}
	r := httptest.NewRequest("POST", "/mcp", nil)

	row := entity.ScheduledMessage{
		ID: "sm_3", SessionID: sid, SessionMode: entity.ScheduledSessionExisting, OwnerUserID: "u1",
	}
	var patch schedule.SchedulePatch
	err := scheduleTargetPatch(r, layout, other, row, map[string]any{"project_id": "proj-1"}, time.Now(), &patch)
	if err == nil {
		t.Fatal("reschedule let a caller park work in a project they cannot reach")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want a not-found style refusal (no existence leak)", err)
	}
}

func TestScheduleTargetPatch_MissingProjectRefused(t *testing.T) {
	layout, sid, _ := scopeMoveFixture(t)
	user := &entity.User{ID: "u1"}
	r := httptest.NewRequest("POST", "/mcp", nil)

	row := entity.ScheduledMessage{
		ID: "sm_4", SessionID: sid, SessionMode: entity.ScheduledSessionExisting, OwnerUserID: "u1",
	}
	var patch schedule.SchedulePatch
	if err := scheduleTargetPatch(r, layout, user, row, map[string]any{"project_id": "ghost"}, time.Now(), &patch); err == nil {
		t.Fatal("move to a nonexistent project should be refused")
	}
}

func TestScheduleTargetPatch_NoTargetArgsIsNoop(t *testing.T) {
	layout, sid, _ := scopeMoveFixture(t)
	user := &entity.User{ID: "u1"}
	r := httptest.NewRequest("POST", "/mcp", nil)

	row := entity.ScheduledMessage{
		ID: "sm_5", SessionID: sid, SessionMode: entity.ScheduledSessionExisting, OwnerUserID: "u1",
	}
	var patch schedule.SchedulePatch
	// A timing-only reschedule must not touch the target at all.
	if err := scheduleTargetPatch(r, layout, user, row, map[string]any{"every": "5m"}, time.Now(), &patch); err != nil {
		t.Fatal(err)
	}
	if patch.SessionMode != nil || patch.ProjectID != nil || patch.SessionID != nil || patch.OwnerUserID != nil {
		t.Fatalf("timing-only edit rewrote the target: %+v", patch)
	}
}
