package pwa

import (
	"encoding/json"
	"net/http"

	"github.com/yogasw/wick/internal/login"
)

type PushHandler struct {
	svc  *PushService
	auth *login.Service
}

func NewPushHandler(svc *PushService, auth *login.Service) *PushHandler {
	return &PushHandler{svc: svc, auth: auth}
}

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
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	sent, err := h.svc.SendTest(r.Context(), user.ID, req.Endpoint)
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
