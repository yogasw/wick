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
	return &Handler{repo: newRepo(db), auth: login.NewService(db, "")}
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

var (
	theAdmin  = &entity.User{ID: "a1", Email: "a1@example.com", Name: "Admin", Role: entity.RoleAdmin, Approved: true}
	theUser   = &entity.User{ID: "u1", Email: "u1@example.com", Name: "User", Role: entity.RoleUser, Approved: true}
	theOwner  = &entity.User{ID: "o1", Email: "o1@example.com", Name: "Owner", Role: entity.RoleUser, IsOwner: true, Approved: true}
	otherAdm  = &entity.User{ID: "a2", Email: "a2@example.com", Name: "Admin2", Role: entity.RoleAdmin, Approved: true}
	thePendng = &entity.User{ID: "p1", Email: "p1@example.com", Name: "Pending", Role: entity.RoleUser, Approved: false}
)

// TestImpersonate_RefusesAdminTarget is the containment rule: if one admin can
// become another, the weakest admin account is a route to every other, and
// removing an admin's privileges stops meaning anything.
func TestImpersonate_RefusesAdminTarget(t *testing.T) {
	h := impersonateHandler(t, theAdmin, otherAdm)
	if rec := startAs(h, theAdmin, otherAdm.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 for an admin target", rec.Code)
	}
}

// TestImpersonate_RefusesOwner: IsOwner counts as admin, so the same rule holds
// even when the role column says "user".
func TestImpersonate_RefusesOwner(t *testing.T) {
	h := impersonateHandler(t, theAdmin, theOwner)
	if rec := startAs(h, theAdmin, theOwner.ID); rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 for the owner", rec.Code)
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
