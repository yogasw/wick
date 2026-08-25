package admin

import (
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// impersonate.go lets an admin view wick as another user ("switch user") and
// switch back.
//
// How the return trip works: the session cookie is replaced with the target
// user's, and the ORIGINAL admin id is stored in a second, separate cookie.
// "Back" reads that cookie, restores the admin session, and clears it. Keeping
// the origin outside the session cookie means the impersonated session is a
// completely ordinary one — every downstream permission check sees the target
// user and nothing has to learn about impersonation to be correct.
//
// Guard rails, because this hands one account another's access:
//
//   - Admin-only, enforced by the route wrapper.
//   - Refuses to impersonate another admin or the owner. Otherwise the weakest
//     admin account becomes a path to every other admin, and revoking one
//     admin would not actually contain them.
//   - Both directions are logged with both ids, so the audit trail says who
//     acted as whom rather than showing the target acting alone.

// impersonateCookie holds the impersonating admin's user id while a switch is
// active. Separate from the session cookie so the active session stays a plain
// one and no permission check needs special handling.
const impersonateCookie = "wick_impersonator"

// startImpersonation switches the caller's session to the target user.
func (h *Handler) startImpersonation(w http.ResponseWriter, r *http.Request) {
	admin := login.GetUser(r.Context())
	if admin == nil || !admin.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	targetID := r.PathValue("id")
	if targetID == "" || targetID == admin.ID {
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	target, err := h.repo.GetUser(r.Context(), targetID)
	if err != nil || target == nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	// An admin must not be able to become another admin: that would turn any
	// single admin account into a route to every other one, and make removing
	// an admin's own privileges pointless.
	if target.IsAdmin() {
		http.Error(w, "cannot impersonate another admin", http.StatusForbidden)
		return
	}
	if !target.Approved {
		// An unapproved account cannot do anything anyway; switching into one
		// would just look broken.
		http.Error(w, "cannot impersonate an unapproved account", http.StatusForbidden)
		return
	}

	secure := r.TLS != nil
	http.SetCookie(w, &http.Cookie{
		Name:     impersonateCookie,
		Value:    admin.ID,
		Path:     "/",
		MaxAge:   int(impersonateTTLSeconds),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
	h.midd.SetSessionCookie(w, target.ID, h.auth.GetUserFilterTagIDs(r.Context(), target.ID), secure)

	log.Warn().
		Str("admin_id", admin.ID).
		Str("admin_email", admin.Email).
		Str("target_id", target.ID).
		Str("target_email", target.Email).
		Msg("admin: impersonation started")

	http.Redirect(w, r, "/", http.StatusFound)
}

// stopImpersonation restores the original admin session.
func (h *Handler) stopImpersonation(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(impersonateCookie)
	if err != nil || c.Value == "" {
		// Nothing to return to. Send them home rather than erroring: the most
		// likely cause is a stale tab, not an attack.
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	adminID := c.Value

	adminUser, err := h.repo.GetUser(r.Context(), adminID)
	if err != nil || adminUser == nil || !adminUser.IsAdmin() {
		// The stored id must STILL be an admin. If that account was demoted or
		// deleted mid-session, restoring it would re-grant privileges that were
		// deliberately taken away — so drop the session entirely instead.
		clearImpersonateCookie(w, r)
		h.midd.ClearSessionCookie(w)
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}

	current := login.GetUser(r.Context())
	clearImpersonateCookie(w, r)
	h.midd.SetSessionCookie(w, adminUser.ID,
		h.auth.GetUserFilterTagIDs(r.Context(), adminUser.ID), r.TLS != nil)

	log.Warn().
		Str("admin_id", adminUser.ID).
		Str("was_acting_as", userIDOf(current)).
		Msg("admin: impersonation ended")

	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

// impersonateTTLSeconds bounds how long the return ticket lives. Long enough
// for real debugging, short enough that a forgotten switch expires.
const impersonateTTLSeconds = 2 * 60 * 60

func clearImpersonateCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     impersonateCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
}

// IsImpersonating reports whether the current request runs inside a switch, so
// the UI can show a banner with a way back. Without that, an admin can forget
// they are someone else and misread everything they see.
func IsImpersonating(r *http.Request) bool {
	c, err := r.Cookie(impersonateCookie)
	return err == nil && c.Value != ""
}

func userIDOf(u *entity.User) string {
	if u == nil {
		return ""
	}
	return u.ID
}
