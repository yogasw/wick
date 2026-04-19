package admin

import (
	"github.com/yogasw/wick/internal/admin/view"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/sso"
	"net/http"

	"github.com/rs/zerolog/log"
)

// ── Configs hub ──────────────────────────────────────────────

func (h *Handler) configsHubPage(w http.ResponseWriter, r *http.Request) {
	view.ConfigsHubPage(login.GetUser(r.Context())).Render(r.Context(), w)
}

// ── SSO ──────────────────────────────────────────────────────

func (h *Handler) ssoPage(w http.ResponseWriter, r *http.Request) {
	rows := h.sso.List()
	callbackURL := h.configs.AppURL() + sso.CallbackPath
	view.SSOPage(rows, callbackURL, login.GetUser(r.Context())).Render(r.Context(), w)
}

func (h *Handler) updateSSO(w http.ResponseWriter, r *http.Request) {
	provider := r.PathValue("provider")
	clientID := r.FormValue("client_id")
	enabled := r.FormValue("enabled") == "true"
	clientSecret := r.FormValue("client_secret")

	// Empty client_secret means "keep the current value" — don't blank
	// out a stored secret just because the form submitted blank.
	if clientSecret == "" {
		if existing, ok := h.sso.Get(provider); ok {
			clientSecret = existing.ClientSecret
		}
	}

	if err := h.sso.Update(r.Context(), provider, clientID, clientSecret, enabled); err != nil {
		log.Ctx(r.Context()).Error().Msgf("update sso %s: %s", provider, err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/configs/sso", http.StatusFound)
}
