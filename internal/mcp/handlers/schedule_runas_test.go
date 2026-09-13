package handlers

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// run_as_user_id decides WHOSE access a fire runs with, so a non-admin must
// not be able to set it. If a schedule's own owner could name the run-as user,
// every schedule becomes a way to borrow somebody else's access.
func TestScheduleRunAsArg_AdminOnly(t *testing.T) {
	plain := &entity.User{ID: "user-ada", Role: entity.RoleUser, Approved: true}
	if _, _, err := scheduleRunAsArg(map[string]any{"run_as_user_id": "user-bob"}, plain); err == nil {
		t.Fatal("a non-admin set run_as_user_id; that is privilege escalation")
	}

	admin := &entity.User{ID: "user-root", Role: entity.RoleAdmin, Approved: true}
	got, present, err := scheduleRunAsArg(map[string]any{"run_as_user_id": "user-bob"}, admin)
	if err != nil || !present || got != "user-bob" {
		t.Fatalf("admin set = (%q, %v, %v), want (user-bob, true, nil)", got, present, err)
	}
}

// Not mentioning the field must leave an existing override alone — "absent"
// and "cleared" are different requests.
func TestScheduleRunAsArg_AbsentIsNotClear(t *testing.T) {
	admin := &entity.User{ID: "user-root", Role: entity.RoleAdmin, Approved: true}
	if _, present, err := scheduleRunAsArg(map[string]any{}, admin); present || err != nil {
		t.Fatalf("absent = (present %v, err %v), want (false, nil)", present, err)
	}
	// An explicit empty string IS a clear: back to the owner.
	got, present, err := scheduleRunAsArg(map[string]any{"run_as_user_id": ""}, admin)
	if err != nil || !present || got != "" {
		t.Fatalf("clear = (%q, %v, %v), want (\"\", true, nil)", got, present, err)
	}
}

// The synthetic internal principal is not a real account and holds no access
// tags. Naming it would be asking for precisely the ownerless-fire behaviour
// this field exists to fix, so it is rejected outright.
func TestScheduleRunAsArg_RejectsInternalPrincipal(t *testing.T) {
	admin := &entity.User{ID: "user-root", Role: entity.RoleAdmin, Approved: true}
	if _, _, err := scheduleRunAsArg(map[string]any{"run_as_user_id": internalAgentUserID}, admin); err == nil {
		t.Fatal("accepted the internal principal as a run-as identity")
	}
}
