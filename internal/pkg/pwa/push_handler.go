package pwa

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/login"
)

type PushHandler struct {
	svc  *PushService
	auth *login.Service
	// sendTest and after are the seams the handler tests replace; they
	// default to the service and time.AfterFunc.
	sendTest func(ctx context.Context, userID, endpoint string) (int, error)
	after    func(d time.Duration, f func())
}

func NewPushHandler(svc *PushService, auth *login.Service) *PushHandler {
	return &PushHandler{
		svc:      svc,
		auth:     auth,
		sendTest: svc.SendTest,
		after:    func(d time.Duration, f func()) { time.AfterFunc(d, f) },
	}
}

// maxTestDelay caps a delayed test push: long enough to close the window
// and look at the OS, short enough that nobody forgets they asked.
const maxTestDelay = 30

// testSendTimeout bounds the delayed send, which runs after the request
// (and its context) is gone.
const testSendTimeout = 30 * time.Second

func (h *PushHandler) Register(mux *http.ServeMux, midd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler {
		return midd.RequireAuth(next)
	}
	mux.Handle("GET /api/push/vapid-public-key", auth(h.publicKey))
	mux.Handle("GET /api/push/subscriptions", auth(h.subscriptions))
	mux.Handle("POST /api/push/subscribe", auth(h.subscribe))
	mux.Handle("POST /api/push/unsubscribe", auth(h.unsubscribe))
	mux.Handle("POST /api/push/test", auth(h.test))
	mux.Handle("POST /api/push/permission", auth(h.permission))
}

func (h *PushHandler) publicKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"enabled":   h.svc.PublicKey() != "",
		"publicKey": h.svc.PublicKey(),
	})
}

func (h *PushHandler) subscriptions(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	devices, err := h.svc.Devices(r.Context(), user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"push_id": h.svc.UserPushID(user.ID),
		"devices": devices,
	})
}

func (h *PushHandler) subscribe(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	var req BrowserSubscription
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.svc.Subscribe(r.Context(), user.ID, r.UserAgent(), req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *PushHandler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := h.svc.Unsubscribe(r.Context(), user.ID, req.Endpoint); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *PushHandler) test(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Endpoint     string `json:"endpoint"`
		DelaySeconds int    `json:"delay_seconds"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	// A delay lets the user close the window first and see the OS
	// notification land. It has to run here: a closed tab runs no JS.
	if delay := min(max(req.DelaySeconds, 0), maxTestDelay); delay > 0 {
		userID, endpoint := user.ID, req.Endpoint
		h.after(time.Duration(delay)*time.Second, func() {
			ctx, cancel := context.WithTimeout(context.Background(), testSendTimeout)
			defer cancel()
			if sent, err := h.sendTest(ctx, userID, endpoint); err != nil && sent == 0 {
				log.Warn().Err(err).Str("user", userID).Msg("pwa: delayed test push failed")
			}
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"scheduled": true, "delay_seconds": delay})
		return
	}
	sent, err := h.sendTest(r.Context(), user.ID, req.Endpoint)
	if err != nil && sent == 0 {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"sent": sent})
}

func (h *PushHandler) permission(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	var req struct {
		Permission string `json:"permission"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if h.auth != nil {
		if err := h.auth.SetPushPermission(r.Context(), user.ID, req.Permission); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
