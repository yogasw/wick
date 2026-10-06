package aigen

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// serve routes a request through a mux carrying the handler's routes,
// with the caller injected as the signed-in user.
func serve(t *testing.T, h *Handler, user, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/ai-gen", h.submit)
	mux.HandleFunc("GET /api/ai-gen/{id}", h.get)
	mux.HandleFunc("POST /api/ai-gen/{id}/cancel", h.cancel)
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req = req.WithContext(login.WithUser(req.Context(), &entity.User{ID: user}, nil))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandlerLifecycle(t *testing.T) {
	prov := &fakeProv{name: "claude", typ: "claude", structd: true, block: make(chan struct{})}
	defer close(prov.block)
	s := newSvc(t, prov, &slotGate{cap: 0}, nil)
	h := NewHandler(s)

	rec := serve(t, h, "alice", "POST", "/api/ai-gen", map[string]any{"kind": "echo", "input": map[string]any{"text": "hi"}})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit = %d %s", rec.Code, rec.Body)
	}
	var j Job
	_ = json.Unmarshal(rec.Body.Bytes(), &j)
	if j.ID == "" || j.Status != StatusQueued || j.Position != 1 {
		t.Fatalf("submit body = %+v", j)
	}

	if rec := serve(t, h, "bob", "GET", "/api/ai-gen/"+j.ID, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other user read = %d", rec.Code)
	}
	if rec := serve(t, h, "bob", "POST", "/api/ai-gen/"+j.ID+"/cancel", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("other user cancel = %d", rec.Code)
	}
	if rec := serve(t, h, "alice", "GET", "/api/ai-gen/"+j.ID, nil); rec.Code != http.StatusOK {
		t.Fatalf("owner read = %d", rec.Code)
	}
	rec = serve(t, h, "alice", "POST", "/api/ai-gen/"+j.ID+"/cancel", nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &j)
	if rec.Code != http.StatusOK || j.Status != StatusCanceled {
		t.Fatalf("cancel = %d %+v", rec.Code, j)
	}

	if rec := serve(t, h, "alice", "POST", "/api/ai-gen", map[string]any{"kind": "nope"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind = %d", rec.Code)
	}
}
