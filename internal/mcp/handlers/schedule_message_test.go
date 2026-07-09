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
	// Plain admin (no admin_see_all on this transport) → own scope only,
	// matching the UI monitor's default.
	admin := &entity.User{ID: "a", Role: entity.RoleAdmin}
	if id, all := scheduleScope(admin); id != "a" || all {
		t.Fatalf("plain admin: got (%q,%v) want (a,false)", id, all)
	}
	// regular user → own scope only.
	u := &entity.User{ID: "u1", Role: entity.RoleUser}
	if id, all := scheduleScope(u); id != "u1" || all {
		t.Fatalf("regular user: got (%q,%v) want (u1,false)", id, all)
	}
}
