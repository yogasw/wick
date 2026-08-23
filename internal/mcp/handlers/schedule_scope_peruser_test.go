package handlers

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// TestScheduleScope_PerUserIdentity checks the one place flagged as a silent-
// regression risk when top-level spawns stopped authenticating as a synthetic
// admin.
//
// scheduleScope keys on CanSeeAllSessions/IsAdmin, NOT on the synthetic
// principal's id, so it keeps working unchanged:
//   - an ADMIN who chats still enumerates every owner (the per-session token is
//     minted with stripAdmin=false, so their role survives);
//   - the synthetic admin fallback (cron / ownerless sessions) still does too;
//   - a PLAIN user is now correctly scoped to their own rows, which is the
//     entire point of per-user identity.
func TestScheduleScope_PerUserIdentity(t *testing.T) {
	cases := []struct {
		name    string
		user    *entity.User
		wantOwn string
		wantAll bool
	}{
		{
			name:    "nil user (stdio/tests) stays unscoped",
			user:    nil,
			wantAll: true,
		},
		{
			name:    "synthetic admin fallback still sees all owners",
			user:    &entity.User{ID: "wick-agent-internal", Role: entity.RoleAdmin, Approved: true},
			wantAll: true,
		},
		{
			name:    "real admin chatting keeps cross-owner listing",
			user:    &entity.User{ID: "user-b", Role: entity.RoleAdmin, Approved: true},
			wantAll: true,
		},
		{
			name:    "plain user is scoped to their own rows",
			user:    &entity.User{ID: "user-a", Role: entity.RoleUser, Approved: true},
			wantOwn: "user-a",
			wantAll: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner, all := scheduleScope(tc.user)
			if all != tc.wantAll || owner != tc.wantOwn {
				t.Fatalf("scheduleScope = (%q, %v), want (%q, %v)",
					owner, all, tc.wantOwn, tc.wantAll)
			}
		})
	}
}
