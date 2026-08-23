package channelidentity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

func asUser(r *http.Request, id string) *http.Request {
	u := &entity.User{ID: id, Email: id + "@example.com", Role: entity.RoleUser, Approved: true}
	return r.WithContext(login.WithUser(r.Context(), u, nil))
}

// TestHandlerList_ScopedToCaller is the access rule: a connection names
// someone's Slack account, so one user must never see another's.
func TestHandlerList_ScopedToCaller(t *testing.T) {
	st := NewStore(newDB(t))
	h := NewHandler(st)
	ctx := context.Background()

	if err := st.Link(ctx, ident("u1", "slack:acme", "U1")); err != nil {
		t.Fatalf("link u1: %v", err)
	}
	if err := st.Link(ctx, ident("u2", "slack:acme", "U2")); err != nil {
		t.Fatalf("link u2: %v", err)
	}

	rec := httptest.NewRecorder()
	h.list(rec, asUser(httptest.NewRequest(http.MethodGet, "/api/channel-connections", nil), "u1"))

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got []connectionJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("returned %d connections, want only the caller's", len(got))
	}
	if got[0].ChannelType != "slack" || got[0].InstanceKey != "slack:acme" {
		t.Errorf("unexpected row: %+v", got[0])
	}
}

// TestHandlerList_Unauthenticated must not leak an empty-but-successful list.
func TestHandlerList_Unauthenticated(t *testing.T) {
	h := NewHandler(NewStore(newDB(t)))
	rec := httptest.NewRecorder()
	h.list(rec, httptest.NewRequest(http.MethodGet, "/api/channel-connections", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

// TestHandlerPause_TogglesDelivery covers the round trip the UI drives, and
// checks the pause is visible to the notifier's ActiveForUser query — not just
// stored.
func TestHandlerPause_TogglesDelivery(t *testing.T) {
	st := NewStore(newDB(t))
	h := NewHandler(st)
	ctx := context.Background()

	if err := st.Link(ctx, ident("u1", "slack:acme", "U1")); err != nil {
		t.Fatalf("link: %v", err)
	}
	rows, _ := st.ListForUser(ctx, "u1")
	id := rows[0].ID

	req := httptest.NewRequest(http.MethodPost, "/api/channel-connections/"+id+"/pause", nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.setPaused(true)(rec, asUser(req, "u1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("pause code = %d, body=%s", rec.Code, rec.Body.String())
	}
	if active, _ := st.ActiveForUser(ctx, "u1"); len(active) != 0 {
		t.Fatal("paused connection is still a delivery target")
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/channel-connections/"+id+"/resume", nil)
	req2.SetPathValue("id", id)
	rec2 := httptest.NewRecorder()
	h.setPaused(false)(rec2, asUser(req2, "u1"))
	if rec2.Code != http.StatusOK {
		t.Fatalf("resume code = %d", rec2.Code)
	}
	if active, _ := st.ActiveForUser(ctx, "u1"); len(active) != 1 {
		t.Fatal("resume did not restore delivery")
	}
}

// TestHandlerPause_CannotTouchAnotherUsersConnection: guessing an id must not
// let one account silence another's notifications.
func TestHandlerPause_CannotTouchAnotherUsersConnection(t *testing.T) {
	st := NewStore(newDB(t))
	h := NewHandler(st)
	ctx := context.Background()

	if err := st.Link(ctx, ident("victim", "slack:acme", "U1")); err != nil {
		t.Fatalf("link: %v", err)
	}
	rows, _ := st.ListForUser(ctx, "victim")
	id := rows[0].ID

	req := httptest.NewRequest(http.MethodPost, "/api/channel-connections/"+id+"/pause", nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	h.setPaused(true)(rec, asUser(req, "attacker"))

	// The store scopes by user id, so this is a no-op rather than an error.
	if active, _ := st.ActiveForUser(ctx, "victim"); len(active) != 1 {
		t.Fatal("another user paused a connection they do not own")
	}
}
