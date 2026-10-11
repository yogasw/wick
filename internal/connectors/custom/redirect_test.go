package custom

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMCPClientRedirectStripsAuthCrossOrigin: the default MCP client keeps
// X-Wick-User and Authorization on a same-origin redirect, drops both when
// the redirect lands on another origin, and still stops at 10 redirects.
func TestMCPClientRedirectStripsAuthCrossOrigin(t *testing.T) {
	type seen struct{ user, auth string }
	got := map[string]seen{}
	record := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			got[name] = seen{r.Header.Get("X-Wick-User"), r.Header.Get("Authorization")}
		}
	}
	evil := httptest.NewServer(record("evil"))
	defer evil.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/cross", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil.URL+"/steal", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/same", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/landed", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/landed", record("same"))
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusTemporaryRedirect)
	})
	origin := httptest.NewServer(mux)
	defer origin.Close()

	client := New(Deps{}).http
	do := func(path string) error {
		req, _ := http.NewRequest(http.MethodGet, origin.URL+path, nil)
		req.Header.Set("X-Wick-User", "jwt-token")
		req.Header.Set("Authorization", "Bearer secret")
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}

	if err := do("/same"); err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
	if s := got["same"]; s.user != "jwt-token" || s.auth != "Bearer secret" {
		t.Errorf("same-origin redirect lost headers: %+v", s)
	}
	if err := do("/cross"); err != nil {
		t.Fatalf("cross-origin redirect: %v", err)
	}
	s, ok := got["evil"]
	if !ok {
		t.Fatal("cross-origin redirect not followed")
	}
	if s.user != "" || s.auth != "" {
		t.Errorf("cross-origin redirect leaked headers: %+v", s)
	}
	if err := do("/loop"); err == nil || !strings.Contains(err.Error(), "10 redirects") {
		t.Errorf("redirect loop: err %v, want stop after 10 redirects", err)
	}
}
