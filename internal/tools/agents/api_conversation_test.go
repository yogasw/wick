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

// Sub-agent sessions are real sessions the caller owns, so every access
// check passes them; only the parent link keeps them out of the
// conversation list. Both the JSON list and the templ sidebar route
// through this helper, so this is the single place the rule holds.
func TestAccessibleSessionIDsDropsSubAgents(t *testing.T) {
	child := makeSession("s2", "p1", "u1")
	child.Meta.ParentSessionID = "s1"
	sessions := map[string]session.Session{
		"s1": makeSession("s1", "p1", "u1"),
		"s2": child,
	}
	ids := []string{"s1", "s2"}

	for _, tc := range []struct {
		name   string
		access projectAccess
	}{
		{"scoped caller", access("p1")},
		// Even an unrestricted view: seeing everything means seeing every
		// conversation, not every session object.
		{"see-all caller", projectAccess{seeAll: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := accessibleSessionIDs(ids, sessions, tc.access, "")
			if len(got) != 1 || got[0] != "s1" {
				t.Fatalf("got %v, want only the parent conversation [s1]", got)
			}
		})
	}
}

/* ── pageTurns ───────────────────────────────────────────────────────── */

func turnsWithIDs(ids ...string) []agentstore.ConversationTurn {
	out := make([]agentstore.ConversationTurn, 0, len(ids))
	for _, id := range ids {
		out = append(out, agentstore.ConversationTurn{TurnID: id})
	}
	return out
}

func idsOf(turns []agentstore.ConversationTurn) []string {
	out := make([]string, 0, len(turns))
	for _, t := range turns {
		out = append(out, t.TurnID)
	}
	return out
}

func TestPageTurns(t *testing.T) {
	all := turnsWithIDs("t1", "t2", "t3", "t4", "t5")

	cases := []struct {
		name     string
		before   string
		limit    int
		want     []string
		wantMore bool
	}{
		{"no limit returns all", "", 0, []string{"t1", "t2", "t3", "t4", "t5"}, false},
		{"limit returns latest window", "", 2, []string{"t4", "t5"}, true},
		{"limit covering all has no more", "", 5, []string{"t1", "t2", "t3", "t4", "t5"}, false},
		{"limit beyond len clamps", "", 99, []string{"t1", "t2", "t3", "t4", "t5"}, false},
		{"before walks back one window", "t4", 2, []string{"t2", "t3"}, true},
		{"before reaching start exhausts", "t2", 2, []string{"t1"}, false},
		{"before first turn returns empty", "t1", 2, []string{}, false},
		{"unknown before falls back to latest window", "nope", 2, []string{"t4", "t5"}, true},
		{"before without limit returns all older", "t4", 0, []string{"t1", "t2", "t3"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, more := pageTurns(all, tc.before, tc.limit)
			gotIDs := idsOf(got)
			if len(gotIDs) != len(tc.want) {
				t.Fatalf("page = %v, want %v", gotIDs, tc.want)
			}
			for i := range tc.want {
				if gotIDs[i] != tc.want[i] {
					t.Fatalf("page = %v, want %v", gotIDs, tc.want)
				}
			}
			if more != tc.wantMore {
				t.Errorf("hasMore = %v, want %v", more, tc.wantMore)
			}
		})
	}
}

func TestBackfillTurnIDs(t *testing.T) {
	turns := []agentstore.ConversationTurn{
		{TurnID: ""},
		{TurnID: "1787411830493006700"},
		{TurnID: ""},
	}
	backfillTurnIDs(turns)
	if turns[0].TurnID != "turn-0" || turns[2].TurnID != "turn-2" {
		t.Errorf("missing ids not backfilled by index: %q, %q", turns[0].TurnID, turns[2].TurnID)
	}
	if turns[1].TurnID != "1787411830493006700" {
		t.Errorf("existing id overwritten: %q", turns[1].TurnID)
	}
}

func TestPageTurnsEmpty(t *testing.T) {
	got, more := pageTurns(nil, "", 20)
	if len(got) != 0 || more {
		t.Fatalf("got %v more=%v, want empty and no more", got, more)
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
