package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// scheduleMonitorVM is the single access gate for the Scheduled page — both
// the listing and the by-id actions run through it — so its scope branch is
// what keeps a project job visible/manageable while still not leaking rows
// across users.
func TestScheduleMonitorVM_Scope(t *testing.T) {
	sessions := map[string]session.Session{
		"sess-mine": {ID: "sess-mine", Meta: session.Meta{UserID: "u1", ProjectID: "p1", Label: "My chat"}},
		"sess-thei": {ID: "sess-thei", Meta: session.Meta{UserID: "u2", ProjectID: "p2", Label: "Their chat"}},
		"sch-abc-1": {ID: "sch-abc-1", Meta: session.Meta{ProjectID: "p1", Label: "Weekly report"}},
	}
	// A plain user who can reach project p1 only.
	acc := projectAccess{userID: "u1", projects: map[string]struct{}{"p1": {}}}

	tests := []struct {
		name     string
		row      entity.ScheduledMessage
		wantOK   bool
		wantVM   func(*testing.T, scheduleVM)
		explains string
	}{
		{
			name:   "session-scoped row in a reachable session is visible",
			row:    entity.ScheduledMessage{ID: "sm_1", SessionID: "sess-mine"},
			wantOK: true,
			wantVM: func(t *testing.T, vm scheduleVM) {
				if vm.SessionLabel != "My chat" {
					t.Fatalf("session label = %q", vm.SessionLabel)
				}
			},
		},
		{
			name:   "session-scoped row in someone else's session is hidden",
			row:    entity.ScheduledMessage{ID: "sm_2", SessionID: "sess-thei"},
			wantOK: false,
		},
		{
			name:   "session-scoped row whose session left the registry is hidden",
			row:    entity.ScheduledMessage{ID: "sm_3", SessionID: "ghost"},
			wantOK: false,
		},
		{
			// The regression this guards: a project-scoped row has NO
			// session_id, so gating on the session map would hide it forever.
			name: "project-scoped row in a reachable project is visible",
			row: entity.ScheduledMessage{
				ID: "sm_4", ProjectID: "p1", SessionMode: entity.ScheduledSessionNew,
				LastSessionID: "sch-abc-1",
			},
			wantOK: true,
			wantVM: func(t *testing.T, vm scheduleVM) {
				if vm.SessionMode != entity.ScheduledSessionNew {
					t.Fatalf("mode = %q", vm.SessionMode)
				}
				if vm.LastSessionLabel != "Weekly report" {
					t.Fatalf("last session label = %q, want the last run's label", vm.LastSessionLabel)
				}
			},
		},
		{
			name: "project-scoped row in an unreachable project is hidden",
			row: entity.ScheduledMessage{
				ID: "sm_5", ProjectID: "p2", SessionMode: entity.ScheduledSessionNew,
			},
			wantOK: false,
		},
		{
			// A project job that has never fired has no last session; it must
			// still be listed (previously nothing anchored it at all).
			name: "project-scoped row that has never fired is still visible",
			row: entity.ScheduledMessage{
				ID: "sm_6", ProjectID: "p1", SessionMode: entity.ScheduledSessionTemplate,
				SessionTemplate: "daily-{date}",
			},
			wantOK: true,
			wantVM: func(t *testing.T, vm scheduleVM) {
				if vm.LastSessionLabel != "" {
					t.Fatalf("unfired row should have no last session label, got %q", vm.LastSessionLabel)
				}
				if vm.SessionTemplate != "daily-{date}" {
					t.Fatalf("template = %q", vm.SessionTemplate)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			vm, ok := scheduleMonitorVM(tc.row, sessions, acc)
			if ok != tc.wantOK {
				t.Fatalf("visible = %v, want %v", ok, tc.wantOK)
			}
			if ok && tc.wantVM != nil {
				tc.wantVM(t, vm)
			}
		})
	}
}

// An admin with see-all reaches both scopes without any project grant.
func TestScheduleMonitorVM_SeeAll(t *testing.T) {
	sessions := map[string]session.Session{
		"sess-thei": {ID: "sess-thei", Meta: session.Meta{UserID: "u2", ProjectID: "p2"}},
	}
	acc := projectAccess{seeAll: true}

	if _, ok := scheduleMonitorVM(entity.ScheduledMessage{SessionID: "sess-thei"}, sessions, acc); !ok {
		t.Fatal("see-all admin cannot see a session-scoped row")
	}
	row := entity.ScheduledMessage{ProjectID: "p-unknown", SessionMode: entity.ScheduledSessionNew}
	if _, ok := scheduleMonitorVM(row, sessions, acc); !ok {
		t.Fatal("see-all admin cannot see a project-scoped row")
	}
}

// The session panel lists a project's jobs from every session in that project,
// so it must also be allowed to act on them — otherwise a row is visible but
// its buttons 404. This must stay in lockstep with Store.ListFiltered.
func TestScheduleBelongsToSession(t *testing.T) {
	tests := []struct {
		name       string
		row        entity.ScheduledMessage
		sid        string
		sessionPrj string
		want       bool
	}{
		{
			name: "session-scoped row targeting this session",
			row:  entity.ScheduledMessage{SessionID: "s1"},
			sid:  "s1", want: true,
		},
		{
			name: "session-scoped row targeting another session",
			row:  entity.ScheduledMessage{SessionID: "s2"},
			sid:  "s1", want: false,
		},
		{
			// The cross-session rule: a job belongs to the project, so a
			// sibling session that never created it can still manage it.
			name: "project job of this session's project, created elsewhere",
			row: entity.ScheduledMessage{
				SessionMode: entity.ScheduledSessionNew, ProjectID: "p1", SourceSessionID: "s9",
			},
			sid: "s1", sessionPrj: "p1", want: true,
		},
		{
			name: "project job of a different project",
			row: entity.ScheduledMessage{
				SessionMode: entity.ScheduledSessionNew, ProjectID: "p2", SourceSessionID: "s9",
			},
			sid: "s1", sessionPrj: "p1", want: false,
		},
		{
			// Fallback for a session with no project binding: it can still
			// manage a job it created itself.
			name: "unbound session, project job it created",
			row: entity.ScheduledMessage{
				SessionMode: entity.ScheduledSessionNew, ProjectID: "p1", SourceSessionID: "s1",
			},
			sid: "s1", want: true,
		},
		{
			name: "unbound session, project job created elsewhere",
			row: entity.ScheduledMessage{
				SessionMode: entity.ScheduledSessionNew, ProjectID: "p1", SourceSessionID: "s2",
			},
			sid: "s1", want: false,
		},
		{
			// source_session_id must NOT be a back door for session-scoped
			// rows: those are addressed by their target only.
			name: "session-scoped row merely created from this session",
			row:  entity.ScheduledMessage{SessionID: "s2", SourceSessionID: "s1"},
			sid:  "s1", sessionPrj: "p1", want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scheduleBelongsToSession(tc.row, tc.sid, tc.sessionPrj); got != tc.want {
				t.Fatalf("belongs = %v, want %v", got, tc.want)
			}
		})
	}
}
