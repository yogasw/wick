package managedbin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func fallbackClient(t *testing.T, h http.Handler) (*client, string) {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	c := newClient()
	c.allowed = func(u *url.URL) bool { return u.Host == strings.TrimPrefix(srv.URL, "https://") }
	c.http.Transport = srv.Client().Transport
	return c, srv.URL
}

func TestDownloadAssetFallsBackToAPIURLOn5xx(t *testing.T) {
	old := retryDelay
	retryDelay = 0
	t.Cleanup(func() { retryDelay = old })
	body := "binary bytes"
	var release, api atomic.Int32
	var accept atomic.Value
	c, base := fallbackClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dl/asset":
			release.Add(1)
			http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
		case "/api/assets/1":
			api.Add(1)
			accept.Store(r.Header.Get("Accept"))
			_, _ = w.Write([]byte(body))
		}
	}))
	a := Asset{Name: "asset", URL: base + "/dl/asset", APIURL: base + "/api/assets/1"}
	got, err := c.downloadAsset(context.Background(), a, filepath.Join(t.TempDir(), "dl"), 1<<20, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(body))
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("sha256 %s", got)
	}
	if release.Load() != 2 || api.Load() != 1 {
		t.Fatalf("release tries %d, api tries %d", release.Load(), api.Load())
	}
	if accept.Load() != "application/octet-stream" {
		t.Fatalf("API Accept %q", accept.Load())
	}
}

func TestDownloadAssetDoesNotFallBackOn4xx(t *testing.T) {
	var api atomic.Int32
	c, base := fallbackClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/assets/1" {
			api.Add(1)
		}
		http.NotFound(w, r)
	}))
	a := Asset{Name: "asset", URL: base + "/dl/asset", APIURL: base + "/api/assets/1"}
	if _, err := c.downloadAsset(context.Background(), a, filepath.Join(t.TempDir(), "dl"), 1<<20, nil); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
	if api.Load() != 0 {
		t.Fatal("a 404 must not try the API URL")
	}
}
