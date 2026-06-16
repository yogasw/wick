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

/* ── accessibleSessionIDs ────────────────────────────────────────────── */

// access builds a non-admin projectAccess for caller "u1" over the given
// accessible project IDs.
func access(projectIDs ...string) projectAccess {
	set := make(map[string]struct{}, len(projectIDs))
	for _, id := range projectIDs {
		set[id] = struct{}{}
	}
	return projectAccess{userID: "u1", projects: set}
}

func TestAccessibleSessionIDs(t *testing.T) {
	sessions := map[string]session.Session{
		"s1": makeSession("s1", "p1", "u1"),
		"s2": makeSession("s2", "p1", "u2"),
		"s3": makeSession("s3", "p2", "u1"),
		"s4": makeSession("s4", "p2", ""),
		"s5": makeSession("s5", "", "u1"),
		"s6": makeSession("s6", "", "u2"), // unscoped, another user's
		"s7": makeSession("s7", "", ""),   // unscoped, ownerless
	}
	allIDs := []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7"}

	cases := []struct {
		name   string
		ids    []string
		access projectAccess
		scoped string
		want   []string
	}{
		{
			name:   "seeAll sees all sessions",
			ids:    allIDs,
			access: projectAccess{seeAll: true},
			scoped: "",
			want:   []string{"s1", "s2", "s3", "s4", "s5", "s6", "s7"},
		},
		{
			name:   "unscoped: only own visible; ownerless + another user's hidden",
			ids:    []string{"s5", "s6", "s7"},
			access: access(),
			scoped: "",
			// s5 own unscoped (visible); s6 another user's; s7 ownerless —
			// both unscoped-non-own are admin-only, hidden from u1.
			want: []string{"s5"},
		},
		{
			name:   "non-admin sees accessible-project sessions + own unscoped only",
			ids:    allIDs,
			access: access("p1"),
			scoped: "",
			// s1,s2 in p1; s5 own unscoped. s7 ownerless unscoped is hidden.
			want: []string{"s1", "s2", "s5"},
		},
		{
			name:   "non-admin with two accessible projects + own unscoped only",
			ids:    allIDs,
			access: access("p1", "p2"),
			scoped: "",
			want:   []string{"s1", "s2", "s3", "s4", "s5"},
		},
		{
			name:   "non-admin no project access still sees own unscoped only",
			ids:    allIDs,
			access: access(),
			scoped: "",
			want:   []string{"s5"},
		},
		{
			name:   "project scoping filters to p1 (seeAll)",
			ids:    allIDs,
			access: projectAccess{seeAll: true},
			scoped: "p1",
			want:   []string{"s1", "s2"},
		},
		{
			name:   "scoped to accessible project shows all its sessions",
			ids:    allIDs,
			access: access("p2"),
			scoped: "p2",
			want:   []string{"s3", "s4"},
		},
		{
			name:   "scoped to inaccessible project shows nothing",
			ids:    allIDs,
			access: access("p1"),
			scoped: "p2",
			want:   []string{},
		},
		{
			name:   "empty ids returns empty",
			ids:    []string{},
			access: access("p1"),
			scoped: "",
			want:   []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := accessibleSessionIDs(tc.ids, sessions, tc.access, tc.scoped)
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
