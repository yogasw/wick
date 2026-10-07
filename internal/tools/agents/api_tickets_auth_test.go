package agents

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

type stubTokens struct{ uid string }

func (s stubTokens) Authenticate(context.Context, string) (string, error) { return s.uid, nil }

type stubUsers struct {
	u    *entity.User
	tags []string
}

func (s stubUsers) GetUserByID(context.Context, string) (*entity.User, error) { return s.u, nil }
func (s stubUsers) GetUserFilterTagIDs(context.Context, string) []string      { return s.tags }

// A bearer request must carry the user's filter tags, exactly like the login
// cookie does. With nil tags every tag-share check fails, so a project the
// user opens fine in the browser answers 404 to their own token — the
// local-works-prod-404 bug.
func TestTicketAPIAuthMWCarriesUserTags(t *testing.T) {
	prevTok, prevUsers := ticketAPITokens, ticketAPIUsers
	defer func() { ticketAPITokens, ticketAPIUsers = prevTok, prevUsers }()

	u := &entity.User{ID: "u1", Email: "u1@abc.com", Approved: true}
	ticketAPITokens = stubTokens{uid: "u1"}
	ticketAPIUsers = stubUsers{u: u, tags: []string{"tag-a", "tag-b"}}

	var gotUser *entity.User
	var gotTags []string
	h := TicketAPIAuthMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser = login.GetUser(r.Context())
		gotTags = login.GetUserTagIDs(r.Context())
	}))

	r := httptest.NewRequest("GET", "/tools/agents/api/tickets", nil)
	r.Header.Set("Authorization", "Bearer tok")
	h.ServeHTTP(httptest.NewRecorder(), r)

	if gotUser == nil || gotUser.ID != "u1" {
		t.Fatalf("user = %+v, want u1 stamped in context", gotUser)
	}
	if len(gotTags) != 2 || gotTags[0] != "tag-a" || gotTags[1] != "tag-b" {
		t.Fatalf("tags = %v, want [tag-a tag-b] — token must carry the user's filter tags", gotTags)
	}
}

// The root /api mount must land on the tool-internal route, and a path
// outside the ticket surface must answer JSON 404 without ever reaching the
// gated chain — a machine caller must never see a login redirect.
func TestTicketRESTShim(t *testing.T) {
	var gotPath string
	gated := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	h := TicketRESTShim(gated)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/projects/p1/tickets?rows=0", nil))
	if want := "/tools/agents/api/projects/p1/tickets"; gotPath != want {
		t.Fatalf("rewritten path = %q, want %q", gotPath, want)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	gotPath = ""
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/sessions", nil))
	if gotPath != "" {
		t.Fatalf("non-ticket path reached the gated chain: %q", gotPath)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
}
