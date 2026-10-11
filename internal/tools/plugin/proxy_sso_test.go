package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	customconn "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/pkg/entity"
	pluginpkg "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

// memKeys is an in-memory custom.KeyStore (plain values, no encryption).
type memKeys map[string]string

func (m memKeys) GetOwned(o, k string) string { return m[o+"/"+k] }
func (m memKeys) SetOwned(_ context.Context, o, k, v string) error {
	m[o+"/"+k] = v
	return nil
}
func (m memKeys) EnsureOwned(context.Context, string, ...entity.Config) error { return nil }
func (m memKeys) EncryptSecret(p string) (string, error)                      { return p, nil }
func (m memKeys) DecryptSecret(t string) (string, error)                      { return t, nil }

// End to end through ServeWebhook with wick's real SSO verifier: a token
// minted for this tool reaches the plugin as X-Wick-Token-User-* (source
// sso) with a Host API call id for that user, the raw X-Wick-User never
// reaches the plugin, a token minted for another tool or sent to another
// server is ignored, and wick's system identity arrives as source=system
// without a call id.
func TestServeWebhookSSOEndToEnd(t *testing.T) {
	fb := &fakeBridge{}
	pluginpkg.SetHostBridge(fb)
	t.Cleanup(func() { pluginpkg.SetHostBridge(nil) })

	svc := customconn.New(customconn.Deps{Keys: memKeys{}, BaseURL: func() string { return "https://wick.example.com" }})
	tool.SetHeaderTokenResolver(customconn.SSOTokenResolver(svc.VerifySSOCaller, func(_ context.Context, id string) (tool.User, bool) {
		if id != "u5" {
			return tool.User{}, false
		}
		return tool.User{ID: "u5", Email: "u5@example.com"}, true
	}))
	t.Cleanup(func() { tool.SetHeaderTokenResolver(nil) })

	mint := func(sub, url string) string {
		tok, err := svc.SSO().Mint(customconn.SSOClaims{Subject: sub, Audience: "wick.example.com", Issuer: "https://wick.example.com", WickURL: url}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}

	pl := &plugin{h: echoHandler()}
	tm := &pluginpkg.ToolModule{Webhooks: []pluginpkg.ToolWebhook{{Method: "POST", Path: "/webhook/mcp", Group: "/webhook"}}}
	_, rr := setup(t, pl, tm)
	hit := func(tok string) map[string]string {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/tools/demo/webhook/mcp", nil)
		req.Header.Set("X-Wick-User", tok)
		req.Header.Set(pluginpkg.HeaderTokenUserID, "forged")
		rr.wh["POST /webhook/mcp"](tool.NewWebhookCtx(rec, req, tool.Tool{Key: "demo", Path: "/tools/demo"}, cfgMap{}))
		if rec.Code != http.StatusOK {
			t.Fatalf("code %d", rec.Code)
		}
		var got map[string]string
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		return got
	}

	got := hit(mint("u5", "https://wick.example.com/tools/demo/webhook/mcp"))
	if got[pluginpkg.HeaderTokenUserID] != "u5" || got[pluginpkg.HeaderTokenUserSource] != pluginpkg.TokenUserSourceSSO ||
		got[pluginpkg.HeaderTokenUserEmail] != "u5@example.com" || got["X-Wick-Call-Id"] != "cid-u5" {
		t.Fatalf("sso headers at plugin = %v", got)
	}
	if _, ok := got["X-Wick-User"]; ok {
		t.Fatal("raw X-Wick-User reached the plugin")
	}

	for name, tok := range map[string]string{
		"other tool":   mint("u5", "https://wick.example.com/tools/other/webhook/mcp"),
		"other server": mint("u5", "https://evil.example/tools/demo/webhook/mcp"),
	} {
		if got := hit(tok); got[pluginpkg.HeaderTokenUserID] != "" || got[pluginpkg.HeaderTokenUserSource] != "" {
			t.Errorf("%s: token recognised, plugin got %v", name, got)
		}
	}

	n := len(fb.got)
	got = hit(mint(tool.SystemUserID, "https://wick.example.com/tools/demo/webhook/mcp"))
	if got[pluginpkg.HeaderTokenUserID] != pluginpkg.TokenUserSystemID || got[pluginpkg.HeaderTokenUserSource] != pluginpkg.TokenUserSourceSystem ||
		got[pluginpkg.HeaderTokenUserRole] != "system" || got["X-Wick-Call-Id"] != "" || len(fb.got) != n {
		t.Fatalf("system headers at plugin = %v (bridge calls %v)", got, fb.got[n:])
	}
}
