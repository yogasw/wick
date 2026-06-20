package home

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRootRedirect_RedirectsRootToAgents(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	h.RootRedirect(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("GET / status = %d, want %d", rec.Code, http.StatusFound)
	}
	if loc := rec.Header().Get("Location"); loc != "/tools/agents/" {
		t.Fatalf("GET / Location = %q, want %q", loc, "/tools/agents/")
	}
}

func TestRootRedirect_NotFoundForOtherPaths(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/bogus", nil)
	rec := httptest.NewRecorder()

	h.RootRedirect(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /bogus status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
