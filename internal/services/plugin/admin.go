package plugin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// TokenView is an access token without its hash.
type TokenView struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Hint      string    `json:"hint"`
	CreatedAt time.Time `json:"created_at"`
	LastUsed  time.Time `json:"last_used,omitempty"`
}

// ServiceView is one service as the admin page shows it.
type ServiceView struct {
	Key             string                    `json:"key"`
	Name            string                    `json:"name"`
	Description     string                    `json:"description,omitempty"`
	Version         string                    `json:"version"`
	Path            string                    `json:"path"`
	Status          Status                    `json:"status"`
	Routes          []wickplugin.ServiceRoute `json:"routes"`
	Capabilities    []string                  `json:"capabilities,omitempty"`
	CallbackScopes  []string                  `json:"callback_scopes,omitempty"`
	CallbackRevoked bool                      `json:"callback_revoked"`
	AutoOff         AutoOffView               `json:"auto_off"`
	Configs         []ConfigField             `json:"configs,omitempty"`
	Tokens          []TokenView               `json:"tokens,omitempty"`
	Logs            []string                  `json:"logs,omitempty"`
}

// View builds s's admin view; full adds tokens and logs.
func (h *Host) View(s *Service, full bool) ServiceView {
	v := ServiceView{
		Key: s.Key, Name: s.Manifest.Meta.Name, Description: s.Manifest.Meta.Description, Version: s.Version,
		Path: "/x/" + s.Key + "/", Status: s.Sup.Status(), Routes: s.Manifest.Routes,
		Capabilities: s.Manifest.Capabilities, CallbackScopes: s.Manifest.CallbackScopes,
		AutoOff: h.autoOffOf(s),
	}
	if h.Tokens != nil {
		v.CallbackRevoked = h.Tokens.CallbackRevoked(s.Key)
	}
	if full {
		v.Configs = h.configFields(s)
		if h.Tokens != nil {
			for _, t := range h.Tokens.List(s.Key) {
				v.Tokens = append(v.Tokens, TokenView{ID: t.ID, Name: t.Name, Hint: t.Hint, CreatedAt: t.CreatedAt, LastUsed: t.LastUsed})
			}
		}
		v.Logs = s.Sup.Logs.Lines()
	}
	return v
}

// RemoteSources lists the services that declare remote_source (the
// "Plugin" choices of the Team Remote agent wizard).
func (h *Host) RemoteSources() []ServiceView {
	out := []ServiceView{}
	for _, s := range h.List() {
		if s.Manifest.Has(wickplugin.CapRemoteSource) {
			out = append(out, h.View(s, false))
		}
	}
	return out
}

// RegisterAdmin mounts the admin API under /manager/api/service-plugins;
// wrap is the admin middleware.
func (h *Host) RegisterAdmin(mux *http.ServeMux, wrap func(http.Handler) http.Handler) {
	const base = "/manager/api/service-plugins"
	hf := func(f http.HandlerFunc) http.Handler { return wrap(f) }
	mux.Handle("GET "+base, hf(func(w http.ResponseWriter, _ *http.Request) {
		out := []ServiceView{}
		for _, s := range h.List() {
			out = append(out, h.View(s, false))
		}
		writeJSON(w, http.StatusOK, out)
	}))
	mux.Handle("GET "+base+"/{key}", hf(h.withService(func(w http.ResponseWriter, _ *http.Request, s *Service) {
		writeJSON(w, http.StatusOK, h.View(s, true))
	})))
	mux.Handle("POST "+base+"/{key}/config", hf(h.withService(h.serveSetConfig)))
	mux.Handle("POST "+base+"/{key}/auto-off", hf(h.withService(h.serveSetAutoOff)))
	mux.Handle("POST "+base+"/{key}/{action}", hf(h.withService(func(w http.ResponseWriter, r *http.Request, s *Service) {
		switch r.PathValue("action") {
		case "start":
			s.Sup.Start()
		case "stop":
			s.Sup.Stop()
		case "restart":
			s.Sup.Restart()
		case "callback-revoke", "callback-allow":
			revoke := r.PathValue("action") == "callback-revoke"
			if err := h.Tokens.SetCallbackRevoked(s.Key, revoke); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
		default:
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, h.View(s, true))
	})))
	mux.Handle("POST "+base+"/{key}/tokens", hf(h.withService(func(w http.ResponseWriter, r *http.Request, s *Service) {
		var body struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		name := strings.TrimSpace(body.Name)
		if name == "" {
			name = "token"
		}
		t, plain, err := h.Tokens.Generate(s.Key, name)
		tokenReply(w, t, plain, err)
	})))
	mux.Handle("POST "+base+"/{key}/tokens/{id}/rotate", hf(h.withService(func(w http.ResponseWriter, r *http.Request, s *Service) {
		t, plain, err := h.Tokens.Rotate(s.Key, r.PathValue("id"))
		tokenReply(w, t, plain, err)
	})))
	mux.Handle("DELETE "+base+"/{key}/tokens/{id}", hf(h.withService(func(w http.ResponseWriter, r *http.Request, s *Service) {
		if err := h.Tokens.Revoke(s.Key, r.PathValue("id")); err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
}

// tokenReply returns the plaintext once, next to the token's view.
func tokenReply(w http.ResponseWriter, t AccessToken, plain string, err error) {
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrTokenNotFound) {
			code = http.StatusNotFound
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":  TokenView{ID: t.ID, Name: t.Name, Hint: t.Hint, CreatedAt: t.CreatedAt},
		"secret": plain,
	})
}

func (h *Host) withService(fn func(http.ResponseWriter, *http.Request, *Service)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, ok := h.Get(r.PathValue("key"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such service plugin"})
			return
		}
		fn(w, r, s)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
