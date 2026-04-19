package health

import (
	"github.com/yogasw/wick/internal/pkg/api/resp"
	"net/http"
)

type httpHandler struct {
	svc *Service
}

func NewHttpHandler(svc *Service) *httpHandler {
	return &httpHandler{
		svc: svc,
	}
}

func (h *httpHandler) Check(w http.ResponseWriter, r *http.Request) {
	healthComponent, isHealthy := h.svc.Check(r.Context())

	statusCode := http.StatusOK
	if !isHealthy {
		statusCode = http.StatusInternalServerError
	}

	resp.WriteJSON(w, statusCode, healthComponent)
}
