package login

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/initcreds"
	"github.com/yogasw/wick/internal/login/view"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/internal/sso"
	"io"
	"net/http"
	"os"
	"strings"

	"golang.org/x/oauth2"
)

const stateCookieName = "_st_oauth_state"

// afterLoginCookie carries a same-origin URL set by callers that want
// the login flow to return the user to a specific page (e.g.
// /oauth/authorize). The cookie is one-shot — read and cleared on the
// next successful login. Path-only redirects are accepted; absolute
// URLs and protocol-relative URLs are rejected on read to avoid
// open-redirect.
const afterLoginCookie = "_st_after_login"

// SetAfterLoginRedirect stores a path the next successful login should
// land on, instead of "/". Paths must start with "/" and not "//".
// Used by /oauth/authorize when the user isn't logged in yet.
func SetAfterLoginRedirect(w http.ResponseWriter, r *http.Request, path string) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     afterLoginCookie,
		Value:    path,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   strings.HasPrefix(r.URL.Scheme, "https") || r.TLS != nil,
	})
}

// consumeAfterLogin reads the after-login redirect and immediately
// clears the cookie. Returns "/" when no cookie is set or the value
// would be unsafe (open-redirect protection).
func (h *Handler) consumeAfterLogin(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(afterLoginCookie)
	if err != nil {
		return "/"
	}
	http.SetCookie(w, &http.Cookie{Name: afterLoginCookie, Path: "/", MaxAge: -1})
	if !strings.HasPrefix(c.Value, "/") || strings.HasPrefix(c.Value, "//") {
		return "/"
	}
	return c.Value
}

// appConfig is the subset of configs.Service the handler needs. Kept
// as an interface so tests can stub it, and to avoid a circular import
// between login and configs.
type appConfig interface {
	AppURL() string
	Set(ctx context.Context, key, value string) error
	AdminPasswordChanged() bool
}

type Handler struct {
	svc      *Service
	midd     *Middleware
	sso      *sso.Service
	cfg      appConfig
}

// NewHandler wires the login routes. The handler reads SSO config from
// the sso.Service on every request so admin edits take effect without
// a restart.
func NewHandler(svc *Service, midd *Middleware, ssoSvc *sso.Service, cfg appConfig) *Handler {
	return &Handler{
		svc:  svc,
		midd: midd,
		sso:  ssoSvc,
		cfg:  cfg,
	}
}

func (h *Handler) secureCookie() bool {
	return strings.HasPrefix(h.cfg.AppURL(), "https")
}

// googleOAuth returns the current Google oauth2.Config built from DB
// state + the current app_url. (nil, false) means Google login is
// currently disabled — callers should redirect back to /auth/login.
func (h *Handler) googleOAuth() (*oauth2.Config, bool) {
	return h.sso.OAuthConfig(entity.SSOProviderGoogle, h.cfg.AppURL())
}

func (h *Handler) Register(mux *http.ServeMux, sessionMidd *Middleware) {
	mux.Handle("GET /auth/login", http.HandlerFunc(h.loginPage))
	mux.Handle("GET /auth/login-google", http.HandlerFunc(h.loginGoogle))
	mux.Handle("GET /auth/callback", http.HandlerFunc(h.callback))
	mux.Handle("POST /auth/login-password", http.HandlerFunc(h.loginPassword))
	mux.Handle("POST /auth/logout", http.HandlerFunc(h.logout))
	mux.Handle("GET /auth/pending", http.HandlerFunc(h.pending))
	mux.Handle("GET /profile", sessionMidd.RequireAuth(http.HandlerFunc(h.profilePage)))
	mux.Handle("GET /profile/setup", sessionMidd.RequireAuth(http.HandlerFunc(h.setupPage)))
	mux.Handle("POST /profile/setup", sessionMidd.RequireAuth(http.HandlerFunc(h.setupSubmit)))
	mux.Handle("POST /profile/password", sessionMidd.RequireAuth(http.HandlerFunc(h.changePassword)))
	mux.Handle("POST /profile/preferences", sessionMidd.RequireAuth(http.HandlerFunc(h.updatePreferences)))
	mux.Handle("POST /theme", http.HandlerFunc(h.updateTheme))
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user != nil && user.Approved {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	errMsg := r.URL.Query().Get("error")
	view.LoginPage(errMsg, h.sso.AnyEnabled()).Render(r.Context(), w)
}

func (h *Handler) loginGoogle(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user != nil && user.Approved {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	oauthCfg, ok := h.googleOAuth()
	if !ok {
		http.Redirect(w, r, "/auth/login?error=Google+login+is+disabled.", http.StatusFound)
		return
	}
	state := randomHex(16)
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.secureCookie(),
	})
	url := oauthCfg.AuthCodeURL(state, oauth2.AccessTypeOnline)
	http.Redirect(w, r, url, http.StatusFound)
}

func (h *Handler) loginPassword(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	user, err := h.svc.LoginWithPassword(r.Context(), email, password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			view.LoginPage("Invalid email or password.", h.sso.AnyEnabled()).Render(r.Context(), w)
			return
		}
		view.LoginPage("Something went wrong. Please try again.", h.sso.AnyEnabled()).Render(r.Context(), w)
		return
	}

	tagIDs := h.svc.GetUserFilterTagIDs(r.Context(), user.ID)
	h.midd.SetSessionCookie(w, user.ID, tagIDs, h.secureCookie())
	h.syncThemeCookie(w, user)

	if !user.Approved {
		http.Redirect(w, r, "/auth/pending", http.StatusFound)
		return
	}
	// First-login: admin still on the auto-generated password lands on
	// the setup form before anything else. Home (/) isn't auth-gated so
	// the RequireAuth redirect would never fire for this case.
	if user.IsAdmin() && !h.cfg.AdminPasswordChanged() {
		http.Redirect(w, r, "/profile/setup", http.StatusFound)
		return
	}
	http.Redirect(w, r, h.consumeAfterLogin(w, r), http.StatusFound)
}

func (h *Handler) callback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(stateCookieName)
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookieName, MaxAge: -1, Path: "/"})

	oauthCfg, ok := h.googleOAuth()
	if !ok {
		http.Error(w, "Google login is disabled", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	token, err := oauthCfg.Exchange(context.Background(), code)
	if err != nil {
		http.Error(w, "failed to exchange token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	info, err := googleUserInfo(oauthCfg, token)
	if err != nil {
		http.Error(w, "failed to get user info: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if !h.sso.IsEmailAllowed(entity.SSOProviderGoogle, info.Email) {
		http.Redirect(w, r, "/auth/login?error=Your+email+domain+is+not+allowed.", http.StatusFound)
		return
	}

	user, err := h.svc.UpsertUser(r.Context(), info.Email, info.Name, info.Picture)
	if err != nil {
		http.Error(w, "failed to save user: "+err.Error(), http.StatusInternalServerError)
		return
	}

	tagIDs := h.svc.GetUserFilterTagIDs(r.Context(), user.ID)
	h.midd.SetSessionCookie(w, user.ID, tagIDs, h.secureCookie())
	h.syncThemeCookie(w, user)

	if !user.Approved {
		http.Redirect(w, r, "/auth/pending", http.StatusFound)
		return
	}
	http.Redirect(w, r, h.consumeAfterLogin(w, r), http.StatusFound)
}

func (h *Handler) syncThemeCookie(w http.ResponseWriter, user *entity.User) {
	http.SetCookie(w, &http.Cookie{
		Name:     guestThemeCookie,
		Value:    encodeGuestThemeCookie(ui.GuestTheme{Current: user.Metadata.Theme, Light: user.Metadata.LightTheme, Dark: user.Metadata.DarkTheme}),
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   h.secureCookie(),
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	h.midd.ClearSessionCookie(w)
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

func (h *Handler) pending(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	if user.Approved {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	view.PendingPage(user).Render(r.Context(), w)
}

func (h *Handler) profilePage(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	success := r.URL.Query().Get("success") == "1"
	prefsSaved := r.URL.Query().Get("prefs") == "1"
	view.ProfilePage(user, "", success, prefsSaved, minPasswordLen()).Render(r.Context(), w)
}

func (h *Handler) updateTheme(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	themeID := r.FormValue("theme")
	t := ui.ThemeByID(themeID)

	var light, dark string
	if user != nil {
		light, dark = user.Metadata.LightTheme, user.Metadata.DarkTheme
		if err := h.svc.SetTheme(r.Context(), user.ID, themeID); err != nil {
			http.Error(w, "failed to save theme", http.StatusInternalServerError)
			return
		}
	} else {
		g := ui.GuestThemeFromContext(r.Context())
		light, dark = g.Light, g.Dark
	}
	if t.ID != "" {
		if t.IsDark {
			dark = t.ID
		} else {
			light = t.ID
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     guestThemeCookie,
		Value:    encodeGuestThemeCookie(ui.GuestTheme{Current: themeID, Light: light, Dark: dark}),
		Path:     "/",
		MaxAge:   365 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   h.secureCookie(),
		SameSite: http.SameSiteLaxMode,
	})
	redirect := r.FormValue("redirect")
	if redirect == "" || !strings.HasPrefix(redirect, "/") {
		redirect = "/"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (h *Handler) updatePreferences(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if err := h.svc.SetHomeView(r.Context(), user.ID, r.FormValue("home_view")); err != nil {
		http.Error(w, "failed to save preferences", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/profile?prefs=1", http.StatusFound)
}

// setupPage renders the first-login setup form. Reachable for any
// authenticated admin while admin_password_changed is still false; the
// middleware redirects there from anywhere else, so this handler does
// not have to gate access — RequireAuth already covers the unauth case.
func (h *Handler) setupPage(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	// Once setup is complete, /profile/setup is dead UI — bounce to /profile.
	if h.cfg.AdminPasswordChanged() {
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}
	view.SetupPage(user, "", minPasswordLen()).Render(r.Context(), w)
}

// setupSubmit applies the first-login changes atomically from the
// caller's POV: validate everything first, then password, then email,
// then mark setup complete + clear INITIAL_CREDENTIALS. If the email
// rename fails (UNIQUE conflict) the password change still stands —
// re-rendering the page lets the admin pick another address without
// losing the new password.
func (h *Handler) setupSubmit(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	if user == nil {
		http.Redirect(w, r, "/auth/login", http.StatusFound)
		return
	}
	if h.cfg.AdminPasswordChanged() {
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	current := r.FormValue("current_password")
	newPw := r.FormValue("new_password")
	confirm := r.FormValue("confirm_password")

	if email == "" || !strings.Contains(email, "@") {
		view.SetupPage(user, "Enter a valid email address.", minPasswordLen()).Render(r.Context(), w)
		return
	}
	if newPw != confirm {
		view.SetupPage(user, "New passwords do not match.", minPasswordLen()).Render(r.Context(), w)
		return
	}
	min := minPasswordLen()
	if len(newPw) < min {
		view.SetupPage(user, fmt.Sprintf("Password must be at least %d characters.", min), min).Render(r.Context(), w)
		return
	}

	if err := h.svc.SetPassword(r.Context(), user.ID, current, newPw); err != nil {
		view.SetupPage(user, err.Error(), minPasswordLen()).Render(r.Context(), w)
		return
	}
	if !strings.EqualFold(email, user.Email) {
		if err := h.svc.SetEmail(r.Context(), user.ID, email); err != nil {
			view.SetupPage(user, "Could not update email — that address may already be in use.", minPasswordLen()).Render(r.Context(), w)
			return
		}
	}
	_ = h.cfg.Set(r.Context(), "admin_password_changed", "true")
	clearInitialCredentials()
	http.Redirect(w, r, "/", http.StatusFound)
}

// minPasswordLen returns the password-length floor for the setup
// form. Tray builds spawn the server in-process and set WICK_TRAY=1 to
// flag a local single-user install; we relax to 1 char there because
// "admin1" on a laptop is a UX hassle, not a real attack surface. CLI
// `wick server` runs (multi-user, network-reachable) keep the 8-char
// floor.
func minPasswordLen() int {
	if os.Getenv("WICK_TRAY") == "1" {
		return 1
	}
	return 8
}

// clearInitialCredentials removes ~/.<appName>/INITIAL_CREDENTIALS.txt
// when present. APP_NAME is set by the system tray (and by `wick run`
// for CLI installs); empty means "fall back to binary basename" —
// initcreds.Clear handles both via userconfig.Dir.
func clearInitialCredentials() {
	appName := strings.TrimSpace(os.Getenv("APP_NAME"))
	if err := initcreds.Clear(appName); err != nil {
		// File-system errors are non-fatal — at worst the operator
		// sees a stale credentials file with a password that no
		// longer works.
		_ = err
	}
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	current := r.FormValue("current_password")
	newPw := r.FormValue("new_password")
	confirm := r.FormValue("confirm_password")

	if newPw != confirm {
		view.ProfilePage(user, "New passwords do not match.", false, false, minPasswordLen()).Render(r.Context(), w)
		return
	}
	min := minPasswordLen()
	if len(newPw) < min {
		view.ProfilePage(user, fmt.Sprintf("Password must be at least %d characters.", min), false, false, min).Render(r.Context(), w)
		return
	}

	if err := h.svc.SetPassword(r.Context(), user.ID, current, newPw); err != nil {
		view.ProfilePage(user, err.Error(), false, false, minPasswordLen()).Render(r.Context(), w)
		return
	}
	_ = h.cfg.Set(r.Context(), "admin_password_changed", "true")
	clearInitialCredentials()
	http.Redirect(w, r, "/profile?success=1", http.StatusFound)
}

type googleUserInfoResponse struct {
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

func googleUserInfo(cfg *oauth2.Config, token *oauth2.Token) (*googleUserInfoResponse, error) {
	client := cfg.Client(context.Background(), token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google api error: %s", body)
	}
	var info googleUserInfoResponse
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
