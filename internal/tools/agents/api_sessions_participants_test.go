package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// A Slack thread is shared work: whoever replies into it did that work too,
// so it must appear under THEIR "Yours" — without taking the session away
// from the person who started it (the owner still decides which identity a
// spawn runs as).
func TestApiSessionList_ParticipantCountsAsMine(t *testing.T) {
	withSessionWorld(t, []seededSession{
		{id: "own", userID: "admin", participants: []string{"admin"}},
		{id: "shared", userID: "bob", participants: []string{"bob", "admin"}},
		{id: "bobs", userID: "bob", participants: []string{"bob"}},
	})
	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin}

	got := fetchSessionList(t, admin, "/api/sessions?project=p1&owner=me")
	if got.Total != 2 {
		t.Fatalf("total=%d, want 2 (own + the thread admin replied in)", got.Total)
	}
	rows := map[string]int{}
	for _, s := range got.Sessions {
		rows[s.ID] = s.Participants
	}
	if _, ok := rows["shared"]; !ok {
		t.Fatalf("owner=me is missing the shared thread: %v", rows)
	}
	if _, ok := rows["bobs"]; ok {
		t.Fatalf("a thread admin never spoke in must not count as mine: %v", rows)
	}
	if rows["shared"] != 2 {
		t.Fatalf("participants for the shared thread = %d, want 2 — the UI marker reads this", rows["shared"])
	}
	if rows["own"] != 1 {
		t.Fatalf("participants for a solo chat = %d, want 1", rows["own"])
	}
}

// Visibility follows the same rule as the list: a participant can open the
// session, including an UNSCOPED one that no project grant covers. Without
// this, a thread would show in someone's list and 403 when they clicked it.
func TestSessionAccess_ParticipantOfUnscopedSession(t *testing.T) {
	withSessionWorld(t, []seededSession{
		{id: "unscoped-shared", userID: "bob", participants: []string{"bob", "carol"}},
	})
	// Strip the project binding so no project access can explain the result.
	sess, ok := globalMgr.Registry().Session("unscoped-shared")
	if !ok {
		t.Fatal("seeded session missing from registry")
	}
	sess.Meta.ProjectID = ""

	carol := &entity.User{ID: "carol", Role: entity.RoleUser}
	_, c := userCtx(t, carol, "/api/sessions/unscoped-shared/meta", map[string]string{"id": "unscoped-shared"})
	if !ownsSession(c, sess) {
		t.Fatal("carol spoke in this thread; she must be able to open it")
	}

	dave := &entity.User{ID: "dave", Role: entity.RoleUser}
	_, c2 := userCtx(t, dave, "/api/sessions/unscoped-shared/meta", map[string]string{"id": "unscoped-shared"})
	if ownsSession(c2, sess) {
		t.Fatal("dave never spoke in this thread and it is unscoped — he must not reach it")
	}
}
