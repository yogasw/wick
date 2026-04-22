package login

import (
	"context"
	"net/http"
	"net/url"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/ui"
)

type contextKey string

const contextKeyUser contextKey = "login_user"
const contextKeyUserTagIDs contextKey = "login_user_tag_ids"

// GetUser retrieves the authenticated user from context. Returns nil if not logged in.
func GetUser(ctx context.Context) *entity.User {
	u, _ := ctx.Value(contextKeyUser).(*entity.User)
	return u
}

// GetUserTagIDs retrieves the filter tag IDs for the authenticated user from
// context. Populated by the Session middleware from the encrypted cookie.
func GetUserTagIDs(ctx context.Context) []string {
	ids, _ := ctx.Value(contextKeyUserTagIDs).([]string)
	return ids
}

// SecretProvider is the minimal interface Middleware needs to read the
// current session-signing secret. configs.Service satisfies it.
type SecretProvider interface {
	SessionSecret() string
}

type Middleware struct {
	svc     *Service
	secrets SecretProvider
}

func NewMiddleware(svc *Service, secrets SecretProvider) *Middleware {
	return &Middleware{svc: svc, secrets: secrets}
}

const cookieName = "_st_sess"
const guestThemeCookie = "_st_theme"

// Session decrypts the AES-GCM session cookie and populates the user and
// their filter tag IDs into the request context. Tampered or expired cookies
// are wiped. For guests (no session), it reads the guest theme cookie.
func (m *Middleware) Session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			var g ui.GuestTheme
			if tc, err2 := r.Cookie(guestThemeCookie); err2 == nil {
				g = parseGuestThemeCookie(tc.Value)
			} else {
				g = ui.GuestTheme{
					Current: ui.DefaultTheme,
					Light:   ui.DefaultLightTheme,
					Dark:    ui.DefaultDarkTheme,
				}
			}
			ctx := ui.WithGuestTheme(r.Context(), g)
			ctx = ui.WithTheme(ctx, g.Current)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		userID, tagIDs, err := decryptSession(m.secrets.SessionSecret(), cookie.Value)
		if err != nil {
			clearCookie(w)
			next.ServeHTTP(w, r)
			return
		}
		user, err := m.svc.GetUserByID(r.Context(), userID)
		if err != nil {
			clearCookie(w)
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), contextKeyUser, user)
		ctx = context.WithValue(ctx, contextKeyUserTagIDs, tagIDs)
		ctx = ui.WithTheme(ctx, ui.EffectiveTheme(user.Metadata.Theme))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth redirects to /auth/login if there is no authenticated, approved user.
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r.Context())
		if user == nil {
			http.Redirect(w, r, "/auth/login", http.StatusFound)
			return
		}
		if !user.Approved {
			http.Redirect(w, r, "/auth/pending", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ToolMeta is the minimal info RequireToolAccess needs about each tool.
// Declared here so the login package doesn't import ui (avoids a cycle).
type ToolMeta struct {
	Path              string
	DefaultVisibility entity.ToolVisibility
}

// RequireToolAccess enforces per-tool visibility on requests under /tools/.
// For each request it finds the tool whose Path is the longest prefix of
// the request URL, then calls Service.CanAccessTool. Public tools let
// anyone through; Private tools require an approved login, plus a
// matching tag when the tool has required tags set. Session must have
// already populated the user in context.
func (m *Middleware) RequireToolAccess(tools []ToolMeta) func(http.Handler) http.Handler {
	byPath := make(map[string]ToolMeta, len(tools))
	for _, t := range tools {
		byPath[t.Path] = t
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := GetUser(r.Context())
			tool, ok := matchTool(r.URL.Path, byPath, tools)
			if !ok {
				// Unknown /tools/... path: render the same custom 404
				// as the rest of the app instead of Go's default plain
				// text http.NotFound from toolsMux.
				ui.RenderNotFound(w, r, user, http.StatusNotFound)
				return
			}
			if m.svc.CanAccessTool(r.Context(), user, tool.Path, tool.DefaultVisibility) {
				next.ServeHTTP(w, r)
				return
			}
			if user == nil {
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}
			if !user.Approved {
				http.Redirect(w, r, "/auth/pending", http.StatusFound)
				return
			}
			// Approved user without matching tags — render the same
			// 404 page as a missing tool so existence can't be inferred.
			ui.RenderNotFound(w, r, user, http.StatusNotFound)
		})
	}
}

func matchTool(urlPath string, byPath map[string]ToolMeta, tools []ToolMeta) (ToolMeta, bool) {
	if t, ok := byPath[urlPath]; ok {
		return t, true
	}
	var best ToolMeta
	var bestLen int
	for _, t := range tools {
		if len(t.Path) > bestLen && hasToolPrefix(urlPath, t.Path) {
			best = t
			bestLen = len(t.Path)
		}
	}
	return best, bestLen > 0
}

func hasToolPrefix(s, prefix string) bool {
	if len(s) <= len(prefix) {
		return false
	}
	if s[:len(prefix)] != prefix {
		return false
	}
	return s[len(prefix)] == '/'
}

// RequireJobAccess enforces per-job visibility on /jobs/{key} requests.
// It reuses the ToolPermission table (stored under "/jobs/{key}") and
// CanAccessTool logic. Session + RequireAuth must have run first.
func (m *Middleware) RequireJobAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r.Context())
		key := r.PathValue("key")
		if !m.svc.CanAccessTool(r.Context(), user, "/jobs/"+key, entity.VisibilityPrivate) {
			ui.RenderNotFound(w, r, user, http.StatusNotFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin returns 403 if the user is not an admin.
func (m *Middleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetUser(r.Context())
		if user == nil || !user.IsAdmin() {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SetSessionCookie encrypts {userID, tagIDs} with AES-256-GCM and writes
// the result as an HttpOnly cookie. Callers should fetch filter tag IDs via
// svc.GetUserFilterTagIDs before calling this.
func (m *Middleware) SetSessionCookie(w http.ResponseWriter, userID string, tagIDs []string, secure bool) {
	value, err := encryptSession(m.secrets.SessionSecret(), userID, tagIDs, sessionTTL)
	if err != nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie wipes the session cookie.
func (m *Middleware) ClearSessionCookie(w http.ResponseWriter) { clearCookie(w) }

func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, MaxAge: -1, Path: "/"})
}

func parseGuestThemeCookie(value string) ui.GuestTheme {
	v, err := url.ParseQuery(value)
	if err != nil || v.Get("c") == "" {
		// Backwards compat: old cookie was a bare theme ID.
		return ui.GuestTheme{Current: value}
	}
	return ui.GuestTheme{Current: v.Get("c"), Light: v.Get("l"), Dark: v.Get("d")}
}

func encodeGuestThemeCookie(g ui.GuestTheme) string {
	v := url.Values{}
	if g.Current != "" {
		v.Set("c", g.Current)
	}
	if g.Light != "" {
		v.Set("l", g.Light)
	}
	if g.Dark != "" {
		v.Set("d", g.Dark)
	}
	return v.Encode()
}
