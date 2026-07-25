package handlers

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// Timing/parse coverage lives in internal/agents/schedule (ParseWhen);
// this file only covers the handler-local access-scope helper.

func TestScheduleScope(t *testing.T) {
	// nil user (stdio/tests) → unscoped.
	if id, all := scheduleScope(nil); id != "" || !all {
		t.Fatalf("nil user: got (%q,%v) want (\"\",true)", id, all)
	}
	// App owner (CanSeeAllSessions) → all owners.
	owner := &entity.User{ID: "o", Role: entity.RoleAdmin, IsOwner: true}
	if _, all := scheduleScope(owner); !all {
		t.Fatalf("app owner should see all owners")
	}
	// Admin (RoleAdmin, not IsOwner) → all owners. This matches the
	// create/cancel gate (canManageSession = CanSeeAllSessions || IsAdmin):
	// previously admins were scoped to their own id here, which made list
	// return [] for the in-process wick provider's synthetic RoleAdmin
	// principal (it can create/cancel but its id never matches the row's
	// owner_user_id = the real session owner). See scheduleScope doc.
	admin := &entity.User{ID: "a", Role: entity.RoleAdmin}
	if _, all := scheduleScope(admin); !all {
		t.Fatalf("admin should see all owners (match create/cancel gate)")
	}
	// regular user → own scope only.
	u := &entity.User{ID: "u1", Role: entity.RoleUser}
	if id, all := scheduleScope(u); id != "u1" || all {
		t.Fatalf("regular user: got (%q,%v) want (u1,false)", id, all)
	}
}
