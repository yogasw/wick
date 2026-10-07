package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/login"
)

// roundTrip runs start then stop the way a browser would: the cookies the
// start response set are sent back with the stop request, and the session
// middleware resolves who is asking before the handler runs.
func stopAfterStart(t *testing.T, h *Handler) *httptest.ResponseRecorder {
	t.Helper()
	start := startAs(h, theAdmin, theUser.ID)
	assertSwitchedTo(t, start, theAdmin.ID)

	req := httptest.NewRequest(http.MethodPost, "/admin/impersonate/stop", nil)
	for _, c := range start.Result().Cookies() {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	rec := httptest.NewRecorder()
	h.midd.Session(h.midd.RequireAuth(http.HandlerFunc(h.stopImpersonation))).ServeHTTP(rec, req)
	return rec
}

func TestStopImpersonation_RestoresAdmin(t *testing.T) {
	h := impersonateHandler(t, theAdmin, theUser)
	rec := stopAfterStart(t, h)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want a redirect", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc == "/auth/login" || loc == "/" {
		t.Fatalf("Back to my account landed on %q instead of the admin's page", loc)
	}
	var session *http.Cookie
	cleared := false
	for _, c := range rec.Result().Cookies() {
		switch {
		case c.Name == impersonateCookie:
			cleared = c.MaxAge < 0
		case c.Value != "":
			session = c
		}
	}
	if !cleared {
		t.Error("the return cookie was not cleared")
	}
	if session == nil {
		t.Fatal("no session cookie written for the admin")
	}
	probe := httptest.NewRequest(http.MethodGet, "/", nil)
	probe.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
	var got string
	h.midd.Session(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if u := login.GetUser(r.Context()); u != nil {
			got = u.ID
		}
	})).ServeHTTP(httptest.NewRecorder(), probe)
	if got != theAdmin.ID {
		t.Fatalf("session after Back belongs to %q, want the admin %q", got, theAdmin.ID)
	}
}
