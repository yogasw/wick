package bookmark

import (
	"encoding/json"
	"github.com/yogasw/wick/internal/login"
	"net/http"
	"strings"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Register(mux *http.ServeMux, midd *login.Middleware) {
	auth := func(next http.HandlerFunc) http.Handler {
		return midd.RequireAuth(next)
	}
	mux.Handle("POST /api/bookmarks/toggle", auth(h.toggle))
}

func (h *Handler) toggle(w http.ResponseWriter, r *http.Request) {
	user := login.GetUser(r.Context())
	toolPath := strings.TrimSpace(r.FormValue("tool_path"))
	if !strings.HasPrefix(toolPath, "/tools/") {
		http.Error(w, "invalid tool_path", http.StatusBadRequest)
		return
	}
	bookmarked, err := h.svc.Toggle(r.Context(), user.ID, toolPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"bookmarked": bookmarked})
}
