package router9

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestProxyAPICapturesWhenSubscribed proves the full capture chain: with a
// subscriber connected, a request through proxyAPI is forwarded to the
// backend AND a ReqEvent carrying both bodies is published. This is the
// exact path the Requests tab depends on.
func TestProxyAPICapturesWhenSubscribed(t *testing.T) {
	// Fake 9router backend: echoes a JSON body and 200.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = body
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","model":"cc/opus"}`))
	}))
	defer backend.Close()

	m := New()
	// Point the API proxy at the fake backend instead of the real 20128.
	bu, _ := url.Parse(backend.URL)
	m.apiProxy = httputil.NewSingleHostReverseProxy(bu)

	// Subscribe first — this is what flips hasSubscribers() true.
	ch, unsub := m.bcast.subscribe()
	defer unsub()
	if !m.bcast.hasSubscribers() {
		t.Fatal("subscribe did not register")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"cc/opus","stream":false}`))
	req.RemoteAddr = "127.0.0.1:5000"
	req.Header.Set("Authorization", "Bearer sk_9router_secret")

	m.proxyAPI(rec, req, false)

	// Response reached the client untouched.
	if rec.Code != http.StatusOK {
		t.Fatalf("proxy status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "msg_1") {
		t.Fatalf("backend body not forwarded: %q", rec.Body.String())
	}

	// Event published with both bodies + redacted auth + sniffed model.
	select {
	case e := <-ch:
		if e.Path != "/v1/messages" || e.Method != "POST" {
			t.Errorf("event route = %s %s", e.Method, e.Path)
		}
		if e.Model != "cc/opus" {
			t.Errorf("model = %q, want cc/opus", e.Model)
		}
		if !strings.Contains(e.ReqBody, "stream") {
			t.Errorf("req body not captured: %q", e.ReqBody)
		}
		if !strings.Contains(e.RespBody, "msg_1") {
			t.Errorf("resp body not captured: %q", e.RespBody)
		}
		if e.Auth != "sk_9r…" {
			t.Errorf("auth = %q, want redacted sk_9r…", e.Auth)
		}
		if e.External {
			t.Error("loopback caller marked external")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event published to subscriber")
	}
}

// TestProxyAPISkipsCaptureWhenNoSubscriber proves the fast path: with no
// watcher, the request is still forwarded but no event is published (and
// the body is never buffered).
func TestProxyAPISkipsCaptureWhenNoSubscriber(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	m := New()
	bu, _ := url.Parse(backend.URL)
	m.apiProxy = httputil.NewSingleHostReverseProxy(bu)

	// A subscriber that we immediately unsubscribe → count back to 0.
	_, unsub := m.bcast.subscribe()
	unsub()
	if m.bcast.hasSubscribers() {
		t.Fatal("expected no subscribers")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.RemoteAddr = "127.0.0.1:5000"
	m.proxyAPI(rec, req, false)

	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Fatalf("fast-path proxy failed: %d %q", rec.Code, rec.Body.String())
	}
}
