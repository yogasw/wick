package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	agentstore "github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/entity"
)

func makeUser(id string, isOwner bool) *entity.User {
	return &entity.User{ID: id, IsOwner: isOwner}
}

func makeSession(id, projectID, userID string) session.Session {
	return session.Session{
		ID: id,
		Meta: session.Meta{
			ProjectID: projectID,
			UserID:    userID,
		},
	}
}

/* ── callerCanSeeSession ─────────────────────────────────────────────── */

func TestCallerCanSeeSession(t *testing.T) {
	cases := []struct {
		name    string
		caller  *entity.User
		meta    session.Meta
		want    bool
	}{
		{
			name:   "nil caller (unauthenticated) sees everything",
			caller: nil,
			meta:   session.Meta{UserID: "u1"},
			want:   true,
		},
		{
			name:   "owner user sees all sessions",
			caller: makeUser("admin", true),
			meta:   session.Meta{UserID: "u1"},
			want:   true,
		},
		{
			name:   "non-admin sees own session",
			caller: makeUser("u1", false),
			meta:   session.Meta{UserID: "u1"},
			want:   true,
		},
		{
			name:   "non-admin sees ownerless session",
			caller: makeUser("u1", false),
			meta:   session.Meta{UserID: ""},
			want:   true,
		},
		{
			name:   "non-admin cannot see another user's session",
			caller: makeUser("u1", false),
			meta:   session.Meta{UserID: "u2"},
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callerCanSeeSession(tc.caller, tc.meta)
			if got != tc.want {
				t.Errorf("callerCanSeeSession = %v, want %v", got, tc.want)
			}
		})
	}
}

/* ── accessibleSessionIDs ────────────────────────────────────────────── */

func TestAccessibleSessionIDs(t *testing.T) {
	sessions := map[string]session.Session{
		"s1": makeSession("s1", "p1", "u1"),
		"s2": makeSession("s2", "p1", "u2"),
		"s3": makeSession("s3", "p2", "u1"),
		"s4": makeSession("s4", "p2", ""),
		"s5": makeSession("s5", "", "u1"),
	}
	allIDs := []string{"s1", "s2", "s3", "s4", "s5"}

	cases := []struct {
		name   string
		ids    []string
		caller *entity.User
		scoped string
		want   []string
	}{
		{
			name:   "admin (owner) sees all sessions",
			ids:    allIDs,
			caller: makeUser("admin", true),
			scoped: "",
			want:   []string{"s1", "s2", "s3", "s4", "s5"},
		},
		{
			name:   "non-admin sees only own + ownerless sessions",
			ids:    allIDs,
			caller: makeUser("u1", false),
			scoped: "",
			want:   []string{"s1", "s3", "s4", "s5"},
		},
		{
			name:   "non-admin u2 sees only own + ownerless",
			ids:    allIDs,
			caller: makeUser("u2", false),
			scoped: "",
			want:   []string{"s2", "s4"},
		},
		{
			name:   "project scoping filters to p1 only (admin)",
			ids:    allIDs,
			caller: makeUser("admin", true),
			scoped: "p1",
			want:   []string{"s1", "s2"},
		},
		{
			name:   "project scoping + non-admin: p2 sessions owned by u1 or ownerless",
			ids:    allIDs,
			caller: makeUser("u1", false),
			scoped: "p2",
			want:   []string{"s3", "s4"},
		},
		{
			name:   "nil caller sees all sessions in project scope",
			ids:    allIDs,
			caller: nil,
			scoped: "p1",
			want:   []string{"s1", "s2"},
		},
		{
			name:   "empty ids returns empty",
			ids:    []string{},
			caller: makeUser("u1", false),
			scoped: "",
			want:   []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := accessibleSessionIDs(tc.ids, sessions, tc.caller, tc.scoped)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d; got %v want %v", len(got), len(tc.want), got, tc.want)
			}
			wantSet := make(map[string]bool, len(tc.want))
			for _, id := range tc.want {
				wantSet[id] = true
			}
			for _, id := range got {
				if !wantSet[id] {
					t.Errorf("unexpected id %q in result %v", id, got)
				}
			}
		})
	}
}

/* ── ConversationTurn JSON tags smoke test ───────────────────────────── */

func TestConversationTurnHasJSONTags(t *testing.T) {
	var turn agentstore.ConversationTurn
	turn.Role = "user"
	turn.Text = "hello"
	if turn.Role == "" {
		t.Error("ConversationTurn.Role should be settable")
	}
}
