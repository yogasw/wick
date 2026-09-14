package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/postgres"
)

// testSecrets is the minimal SecretProvider a Middleware needs to sign a
// session cookie in tests.
type testSecrets struct{}

func (testSecrets) SessionSecret() string      { return "test-secret" }
func (testSecrets) AdminPasswordChanged() bool { return true }

// impersonateHandler builds a Handler with a seeded user table.
func impersonateHandler(t *testing.T, users ...*entity.User) *Handler {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: postgres.NewLogLevel("silent")})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	for _, u := range users {
		if err := db.Create(u).Error; err != nil {
			t.Fatalf("seed %s: %v", u.ID, err)
		}
	}
	svc := login.NewService(db, "")
	// The refusal tests never reach cookie-setting; the success ones do, so
	// the handler needs a real middleware rather than a nil one.
	return &Handler{repo: newRepo(db), auth: svc, midd: login.NewMiddleware(svc, testSecrets{})}
}

func startAs(h *Handler, actor *entity.User, targetID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/admin/users/"+targetID+"/impersonate", nil)
	req.SetPathValue("id", targetID)
	if actor != nil {
		req = req.WithContext(login.WithUser(req.Context(), actor, nil))
	}
	rec := httptest.NewRecorder()
	h.startImpersonation(rec, req)
	return rec
}

// assertSwitchedTo checks the return trip is armed: the impersonation cookie
// has to carry the ACTING admin's id, or "Back" cannot restore them.
func assertSwitchedTo(t *testing.T, rec *httptest.ResponseRecorder, adminID string) {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == impersonateCookie {
			if c.Value != adminID {
				t.Fatalf("return cookie = %q, want the acting admin %q", c.Value, adminID)
			}
			return
		}
	}
	t.Fatal("no return cookie set — Back would strand the admin")
}

var (
	theAdmin  = &entity.User{ID: "a1", Email: "a1@example.com", Name: "Admin", Role: entity.RoleAdmin, Approved: true}
	theUser   = &entity.User{ID: "u1", Email: "u1@example.com", Name: "User", Role: entity.RoleUser, Approved: true}
	theOwner  = &entity.User{ID: "o1", Email: "o1@example.com", Name: "Owner", Role: entity.RoleUser, IsOwner: true, Approved: true}
	otherAdm  = &entity.User{ID: "a2", Email: "a2@example.com", Name: "Admin2", Role: entity.RoleAdmin, Approved: true}
	thePendng = &entity.User{ID: "p1", Email: "p1@example.com", Name: "Pending", Role: entity.RoleUser, Approved: false}
)

// TestImpersonate_AllowsAdminTarget: an admin may switch into another admin.
// This replaces the old containment rule, deliberately — support has to be
// able to reproduce what a colleague with the same role sees. It does mean one
// admin account reaches every other one, which is why both directions are
// logged with both ids.
func TestImpersonate_AllowsAdminTarget(t *testing.T) {
	h := impersonateHandler(t, theAdmin, otherAdm)
	rec := startAs(h, theAdmin, otherAdm.ID)
	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302 for an admin target", rec.Code)
	}
	assertSwitchedTo(t, rec, theAdmin.ID)
}

// TestImpersonate_AllowsOwner: IsOwner counts as admin, and the same widening
// applies — the owner's account is reachable too.
func TestImpersonate_AllowsOwner(t *testing.T) {
	h := impersonateHandler(t, theAdmin, theOwner)
	rec := startAs(h, theAdmin, theOwner.ID)
	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302 for the owner", rec.Code)
	}
	assertSwitchedTo(t, rec, theAdmin.ID)
}

// TestImpersonate_RefusesSelf is the one target left out: switching into
// yourself changes nothing and would only strand the return cookie.
func TestImpersonate_RefusesSelf(t *testing.T) {
	h := impersonateHandler(t, theAdmin)
	rec := startAs(h, theAdmin, theAdmin.ID)
	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want a redirect", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == impersonateCookie && c.Value != "" {
			t.Fatal("self-impersonation set the return cookie")
		}
	}
}

// TestImpersonate_RefusesUnapproved: an unapproved account can do nothing, so
// switching into one would only look broken.
func TestImpersonate_RefusesUnapproved(t *testing.T) {
	h := impersonateHandler(t, theAdmin, thePendng)
	if rec := startAs(h, theAdmin, thePendng.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 for an unapproved target", rec.Code)
	}
}

// TestImpersonate_RefusesNonAdminActor: the route is admin-gated, but the
// handler must not rely on the wrapper alone.
func TestImpersonate_RefusesNonAdminActor(t *testing.T) {
	h := impersonateHandler(t, theUser, thePendng)
	if rec := startAs(h, theUser, thePendng.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 for a non-admin actor", rec.Code)
	}
	if rec := startAs(h, nil, thePendng.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 with no actor", rec.Code)
	}
}

// TestImpersonate_UnknownTarget reports not-found rather than switching into a
// blank session.
func TestImpersonate_UnknownTarget(t *testing.T) {
	h := impersonateHandler(t, theAdmin)
	if rec := startAs(h, theAdmin, "does-not-exist"); rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

// TestIsImpersonating reads the flag the banner depends on. If this ever
// returns false while a switch is active, an admin acts as someone else with no
// warning on screen.
func TestIsImpersonating(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if IsImpersonating(req) {
		t.Error("reported impersonation with no cookie")
	}
	req.AddCookie(&http.Cookie{Name: impersonateCookie, Value: "a1"})
	if !IsImpersonating(req) {
		t.Error("did not report impersonation with the cookie set")
	}

	blank := httptest.NewRequest(http.MethodGet, "/", nil)
	blank.AddCookie(&http.Cookie{Name: impersonateCookie, Value: ""})
	if IsImpersonating(blank) {
		t.Error("an empty cookie counted as an active switch")
	}
}
