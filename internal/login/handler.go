package login

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login/view"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/internal/sso"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"
)

const stateCookieName = "_st_oauth_state"

// appConfig is the subset of configs.Service the handler needs. Kept
// as an interface so tests can stub it, and to avoid a circular import
// between login and configs.
type appConfig interface {
	AppURL() string
	Set(ctx context.Context, key, value string) error
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
	http.Redirect(w, r, "/", http.StatusFound)
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
	http.Redirect(w, r, "/", http.StatusFound)
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
	view.ProfilePage(user, "", success, prefsSaved).Render(r.Context(), w)
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

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r.Context())
	current := r.FormValue("current_password")
	newPw := r.FormValue("new_password")
	confirm := r.FormValue("confirm_password")

	if newPw != confirm {
		view.ProfilePage(user, "New passwords do not match.", false, false).Render(r.Context(), w)
		return
	}
	if len(newPw) < 8 {
		view.ProfilePage(user, "Password must be at least 8 characters.", false, false).Render(r.Context(), w)
		return
	}

	if err := h.svc.SetPassword(r.Context(), user.ID, current, newPw); err != nil {
		view.ProfilePage(user, err.Error(), false, false).Render(r.Context(), w)
		return
	}
	_ = h.cfg.Set(r.Context(), "admin_password_changed", "true")
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
