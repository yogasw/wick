package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

func newCtx(t *testing.T, base string, input map[string]string) *connector.Ctx {
	t.Helper()
	return connector.NewCtx(t.Context(), "test", map[string]string{"base_url": base, "token": "tok"}, input, http.DefaultClient, nil, nil)
}

func TestGetMRUsesEscapedPathAndToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "tok" {
			t.Errorf("missing token header")
		}
		if r.URL.EscapedPath() != "/api/v4/projects/acme%2Fweb/merge_requests/12" {
			t.Errorf("unexpected path %s", r.URL.EscapedPath())
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"iid": 12, "state": "merged", "author": map[string]any{"username": "dev"}, "secret_field": "x"})
	}))
	defer srv.Close()

	out, err := getMR(newCtx(t, srv.URL, map[string]string{"project": "acme/web", "mr_iid": "12"}))
	if err != nil {
		t.Fatal(err)
	}
	m := out.(map[string]any)
	if m["state"] != "merged" || m["author_username"] != "dev" {
		t.Fatalf("unexpected shape: %v", m)
	}
	if _, ok := m["secret_field"]; ok {
		t.Fatal("pick should drop unknown fields")
	}
}

func TestAPIErrorMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"404 Project Not Found"}`))
	}))
	defer srv.Close()

	_, err := getProject(newCtx(t, srv.URL+"/api/v4", map[string]string{"project": "acme/none"}))
	if err == nil || err.Error() != "gitlab 404: 404 Project Not Found" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProjectRequired(t *testing.T) {
	if _, err := listMRs(newCtx(t, "http://unused", nil)); err == nil {
		t.Fatal("missing project must fail")
	}
}
