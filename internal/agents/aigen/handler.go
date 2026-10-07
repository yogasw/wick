package aigen

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/yogasw/wick/internal/login"
)

// Handler exposes the job API:
//
//	POST /api/ai-gen               {kind, input} → job
//	GET  /api/ai-gen/{id}          → job (owner only)
//	POST /api/ai-gen/{id}/cancel   → job (owner only)
//
// Clients poll GET about once a second; a job is small and short-lived,
// so a stream would add a connection per button for no gain.
type Handler struct {
	svc *Service
}

// NewHandler wraps svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register mounts the routes behind auth.
func (h *Handler) Register(mux *http.ServeMux, midd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler {
		return midd.RequireAuth(next)
	}
	mux.Handle("POST /api/ai-gen", auth(h.submit))
	mux.Handle("GET /api/ai-gen/{id}", auth(h.get))
	mux.Handle("POST /api/ai-gen/{id}/cancel", auth(h.cancel))
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind  string `json:"kind"`
		Input Input  `json:"input"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body: " + err.Error()})
		return
	}
	j, err := h.svc.Submit(r.Context(), callerID(r), body.Kind, body.Input)
	if err != nil {
		status := http.StatusUnprocessableEntity
		switch {
		case errors.Is(err, ErrBusy):
			status = http.StatusTooManyRequests
		case errors.Is(err, ErrUnknownKind):
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, j)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	h.reply(w)(h.svc.Get(callerID(r), r.PathValue("id")))
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	h.reply(w)(h.svc.Cancel(callerID(r), r.PathValue("id")))
}

func (h *Handler) reply(w http.ResponseWriter) func(Job, error) {
	return func(j Job, err error) {
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, j)
	}
}

func callerID(r *http.Request) string {
	if u := login.GetUser(r.Context()); u != nil {
		return u.ID
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
