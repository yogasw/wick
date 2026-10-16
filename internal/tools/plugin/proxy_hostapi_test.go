package plugin

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

	"google.golang.org/grpc"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

type fakeBridge struct{ got []string }

func (f *fakeBridge) GRPCServer(string, string) func([]grpc.ServerOption) *grpc.Server { return nil }
func (f *fakeBridge) ProcessEnv(string, string) ([]string, func())                     { return nil, func() {} }
func (f *fakeBridge) BeginCall(key, kind, userID, sessionID string) (string, func()) {
	return f.BeginCallFrom(key, kind, userID, "caller", sessionID)
}
func (f *fakeBridge) BeginCallFrom(key, kind, userID, source, _ string) (string, func()) {
	f.got = append(f.got, key+"/"+kind+"/"+userID+"/"+source)
	return "cid-" + userID, func() {}
}

// §7: a webhook request carrying a valid wick token reaches the plugin with
// X-Wick-Token-User-* (client-sent copies dropped), and its Host API calls
// run as the token owner.
func TestProxyTokenUserHeadersAndCallID(t *testing.T) {
	fb := &fakeBridge{}
	wickplugin.SetHostBridge(fb)
	t.Cleanup(func() { wickplugin.SetHostBridge(nil) })

	h := http.Header{}
	h.Set(wickplugin.HeaderTokenUserID, "forged")
	injectHeaders(h, "/tools/demo", nil)
	if h.Get(wickplugin.HeaderTokenUserID) != "" {
		t.Fatal("client-sent X-Wick-Token-User-Id survived")
	}
	u := &tool.User{ID: "u7", Email: "u7@example.com", Name: "Seven", IsAdmin: true, Tags: []string{"ops"}}
	injectTokenUser(h, u)
	if h.Get(wickplugin.HeaderTokenUserID) != "u7" || h.Get(wickplugin.HeaderTokenUserRole) != "admin" ||
		h.Get(wickplugin.HeaderTokenUserTags) != "ops" || h.Get(wickplugin.HeaderUserID) != "" {
		t.Fatalf("headers = %v", h)
	}

	p := &Proxy{key: "demo"}
	if id, _ := p.beginCall(request{tokenUser: u}); id != "cid-u7" {
		t.Fatalf("token call id = %q", id)
	}
	if id, _ := p.beginCall(request{user: &tool.User{ID: "u1"}, tokenUser: u}); id != "cid-u1" {
		t.Fatalf("session user must win, got %q", id)
	}
	if id, _ := p.beginCall(request{}); id != "" {
		t.Fatalf("anonymous webhook got call id %q", id)
	}
	if len(fb.got) != 2 || fb.got[0] != "demo/tool/u7/token" || fb.got[1] != "demo/tool/u1/caller" {
		t.Fatalf("bridge calls = %v", fb.got)
	}
}

// The ticket webhook's delivery headers reach the plugin as sent, so it can
// verify the HMAC itself; identity headers are still dropped or overwritten.
func TestProxyPassesDeliveryHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("X-Wick-Signature", "sha256=abc")
	h.Set("X-Wick-Event", "ticket.status_changed")
	h.Set("X-Wick-Delivery", "d-1")
	h.Set(wickplugin.HeaderUserID, "forged")
	h.Set(wickplugin.HeaderUserRole, "admin")
	h.Set(wickplugin.HeaderTokenUserID, "forged")
	h.Set(wickplugin.HeaderTokenUserSource, "system")
	h.Set("X-Wick-Anything-Else", "x")
	injectHeaders(h, "/tools/demo", &tool.User{ID: "u1", Name: "One"})
	injectTokenUser(h, nil)

	if h.Get("X-Wick-Signature") != "sha256=abc" || h.Get("X-Wick-Event") != "ticket.status_changed" || h.Get("X-Wick-Delivery") != "d-1" {
		t.Fatalf("delivery headers changed: %v", h)
	}
	if h.Get(wickplugin.HeaderUserID) != "u1" || h.Get(wickplugin.HeaderUserRole) != "user" {
		t.Fatalf("session headers not overwritten: %v", h)
	}
	if h.Get(wickplugin.HeaderTokenUserID) != "" || h.Get(wickplugin.HeaderTokenUserSource) != "" || h.Get("X-Wick-Anything-Else") != "" {
		t.Fatalf("other X-Wick-* survived: %v", h)
	}
}

// Every injection names its credential, a forged source header from the
// client is dropped with the rest of X-Wick-*, and wick's system identity
// is passed on (role system, no email) without a Host API call as a user.
func TestProxyTokenUserSourceAndSystem(t *testing.T) {
	fb := &fakeBridge{}
	wickplugin.SetHostBridge(fb)
	t.Cleanup(func() { wickplugin.SetHostBridge(nil) })

	h := http.Header{}
	h.Set(wickplugin.HeaderTokenUserSource, "system")
	h.Set(wickplugin.HeaderTokenUserRole, "admin")
	injectHeaders(h, "/tools/demo", nil)
	if h.Get(wickplugin.HeaderTokenUserSource) != "" || h.Get(wickplugin.HeaderTokenUserRole) != "" {
		t.Fatalf("client-sent token-user headers survived: %v", h)
	}

	injectTokenUser(h, &tool.User{ID: "u7"}) // a PAT owner from an older resolver
	if h.Get(wickplugin.HeaderTokenUserSource) != wickplugin.TokenUserSourcePAT {
		t.Fatalf("PAT source = %q", h.Get(wickplugin.HeaderTokenUserSource))
	}

	sso := &tool.User{ID: "u8", Email: "u8@example.com", Source: tool.TokenSourceSSO}
	h = http.Header{}
	injectTokenUser(h, sso)
	if h.Get(wickplugin.HeaderTokenUserSource) != wickplugin.TokenUserSourceSSO || h.Get(wickplugin.HeaderTokenUserID) != "u8" {
		t.Fatalf("sso headers = %v", h)
	}

	sys := &tool.User{ID: tool.SystemUserID, Name: "wick", Source: tool.TokenSourceSystem}
	h = http.Header{}
	injectTokenUser(h, sys)
	if h.Get(wickplugin.HeaderTokenUserID) != wickplugin.TokenUserSystemID ||
		h.Get(wickplugin.HeaderTokenUserRole) != "system" ||
		h.Get(wickplugin.HeaderTokenUserSource) != wickplugin.TokenUserSourceSystem ||
		h.Values(wickplugin.HeaderTokenUserEmail) != nil {
		t.Fatalf("system headers = %v", h)
	}

	p := &Proxy{key: "demo"}
	if id, _ := p.beginCall(request{tokenUser: sys}); id != "" {
		t.Fatalf("system identity got a user call id %q", id)
	}
	if id, _ := p.beginCall(request{tokenUser: sso}); id != "cid-u8" {
		t.Fatalf("sso call id = %q", id)
	}
	if len(fb.got) != 1 || fb.got[0] != "demo/tool/u8/token" {
		t.Fatalf("bridge calls = %v", fb.got)
	}
}

// The plugin sees the public origin, not Out.Host "plugin": a URL it builds
// from X-Forwarded-Proto/-Host (a poll_url) must pass wick's same-origin check.
func TestProxyForwardsPublicOrigin(t *testing.T) {
	cases := []struct {
		name, inProto, publicBase, wantProto string
		tls                                  bool
	}{
		{"behind TLS proxy", "https", "", "https", false},
		{"first of a list", "https, http", "", "https", false},
		{"plain http", "", "", "http", false},
		{"direct TLS", "", "", "https", true},
		// The proxy in front of wick sends no X-Forwarded-Proto: app_url's
		// scheme is the public one for app_url's own host.
		{"no proto, app_url host", "", "https://EXAMPLE.test", "https", false},
		{"no proto, other host", "", "https://other.test", "http", false},
		{"inbound proto beats app_url", "http", "https://example.test", "http", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := httptest.NewRequest(http.MethodGet, "http://example.test/tools/demo/x", nil)
			in.RemoteAddr = "10.0.0.9:1234"
			if c.inProto != "" {
				in.Header.Set("X-Forwarded-Proto", c.inProto)
			}
			if c.tls {
				in.TLS = &tls.ConnectionState{}
			}
			pr := &httputil.ProxyRequest{In: in, Out: in.Clone(in.Context())}
			pr.Out.Header.Del("X-Forwarded-Proto") // ReverseProxy strips it before Rewrite
			setForwarded(pr, c.publicBase)
			if got := pr.Out.Header.Get("X-Forwarded-Host"); got != "example.test" {
				t.Fatalf("X-Forwarded-Host = %q", got)
			}
			if got := pr.Out.Header.Get("X-Forwarded-Proto"); got != c.wantProto {
				t.Fatalf("X-Forwarded-Proto = %q, want %q", got, c.wantProto)
			}
			if got := pr.Out.Header.Get("X-Forwarded-For"); got != "10.0.0.9" {
				t.Fatalf("X-Forwarded-For = %q", got)
			}
		})
	}
}

// A board action's poll_url, built by the plugin from X-Forwarded-Proto/-Host
// behind a proxy that sends no X-Forwarded-Proto, keeps the https of the
// button URL (app_url) instead of turning into http and failing same-origin.
func TestProxyPollURLKeepsAppURLScheme(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Forwarded-Proto") + "://" + r.Header.Get("X-Forwarded-Host") + r.URL.Path
	}))
	defer ts.Close()
	target, _ := url.Parse(ts.URL)
	px := &Proxy{publicBase: func() string { return "https://support.example.test" }}
	rp := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		setForwarded(pr, px.publicBaseURL())
		pr.SetURL(target)
	}}
	in := httptest.NewRequest(http.MethodPost, "http://support.example.test/tools/sync/webhook/board/status", nil)
	rec := httptest.NewRecorder()
	rp.ServeHTTP(rec, in)
	if want := "https://support.example.test/tools/sync/webhook/board/status"; got != want {
		t.Fatalf("plugin built %q, want %q", got, want)
	}
}
