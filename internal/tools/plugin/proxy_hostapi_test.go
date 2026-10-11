package plugin

import (
	"net/http"
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
