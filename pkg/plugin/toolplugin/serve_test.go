package toolplugin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/yogasw/wick/pkg/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
	"github.com/yogasw/wick/pkg/tool"
)

func testModule() tool.Module {
	return tool.Module{
		Meta:    tool.Tool{Key: "demo", Name: "Demo"},
		Configs: []entity.Config{{Key: "greeting", Required: true}},
		Register: func(r tool.Router) {
			r.GET("/", func(c *tool.Ctx) {
				u, _ := c.User()
				c.HTML(templ.Raw("<p>" + c.Cfg("greeting") + " " + u.Email + "</p>"))
			})
			r.GET("/api", func(c *tool.Ctx) { c.JSON(200, map[string]any{"missing": c.Missing()}) })
			wh := r.WebhookGroup("/webhook")
			wh.POST("/hook", func(c *tool.WebhookCtx) { c.W.WriteHeader(http.StatusAccepted) })
		},
	}
}

func TestManifestListsWebhooksAndKeepWarm(t *testing.T) {
	tm := Manifest(testModule(), KeepWarm())
	if !tm.KeepWarm || tm.Meta.Key != "demo" {
		t.Fatalf("manifest = %+v", tm)
	}
	if len(tm.Webhooks) != 1 || tm.Webhooks[0] != (wickplugin.ToolWebhook{Method: "POST", Path: "/webhook/hook", Group: "/webhook"}) {
		t.Fatalf("webhooks = %+v", tm.Webhooks)
	}
}

func TestPageIsFragmentWithLayoutHeaders(t *testing.T) {
	h := Handler(testModule(), map[string]string{"greeting": "hi"})
	req := httptest.NewRequest("GET", "/tools/demo", nil)
	req.Header.Set(wickplugin.HeaderUserID, "u1")
	req.Header.Set(wickplugin.HeaderUserEmail, "a@b.c")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get(wickplugin.HeaderLayout) != wickplugin.LayoutPage || rec.Header().Get(wickplugin.HeaderTitle) != "Demo" {
		t.Fatalf("layout headers missing: %v", rec.Header())
	}
	body, _ := io.ReadAll(rec.Body)
	if string(body) != "<p>hi a@b.c</p>" {
		t.Fatalf("body = %q", body)
	}
}

func TestMissingConfigAndJSONPassThrough(t *testing.T) {
	h := Handler(testModule(), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/tools/demo/api", nil))
	if rec.Header().Get(wickplugin.HeaderLayout) != "" || !strings.Contains(rec.Body.String(), `"greeting"`) {
		t.Fatalf("json response = %v %q", rec.Header(), rec.Body.String())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/tools/demo/webhook/hook", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("webhook code = %d", rec.Code)
	}
}

func TestUserFromHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if _, ok := UserFromHeaders(r); ok {
		t.Fatal("no headers must mean no user")
	}
	r.Header.Set(wickplugin.HeaderUserID, "7")
	r.Header.Set(wickplugin.HeaderUserRole, "admin")
	r.Header.Set(wickplugin.HeaderUserTags, "ops, support")
	u, ok := UserFromHeaders(r)
	if !ok || !u.IsAdmin || !u.HasTag("support") || len(u.Tags) != 2 {
		t.Fatalf("user = %+v", u)
	}
}

// whoModule echoes WebhookCtx.TokenUser so the SDK path is tested end to
// end: host-injected headers → wrap → tool.WebhookCtx.TokenUser.
func whoModule() tool.Module {
	return tool.Module{
		Meta: tool.Tool{Key: "who", Name: "Who"},
		Register: func(r tool.Router) {
			r.WebhookGroup("/mcp").POST("", func(c *tool.WebhookCtx) {
				u, ok := c.TokenCaller()
				if !ok {
					c.W.WriteHeader(http.StatusUnauthorized)
					return
				}
				_, person := c.TokenUser()
				c.JSON(200, map[string]any{"id": u.ID, "source": u.Source, "admin": u.IsAdmin, "email": u.Email, "person": person})
			})
		},
	}
}

// An SSO call carries no Authorization header, so TokenUser must answer
// from what the host injected — the case S2 found returning false.
func TestWebhookTokenUserWithoutBearer(t *testing.T) {
	h := Handler(whoModule(), nil)
	cases := []struct {
		name, id, role, source, email string
		want                          string
	}{
		{"sso", "u9", "admin", wickplugin.TokenUserSourceSSO, "a@b.c", `{"admin":true,"email":"a@b.c","id":"u9","person":true,"source":"sso"}`},
		{"system", wickplugin.TokenUserSystemID, "system", wickplugin.TokenUserSourceSystem, "", `{"admin":false,"email":"","id":"system:wick","person":false,"source":"system"}`},
		// An older host sent no source header; it only injected PAT owners.
		{"legacy pat", "u1", "user", "", "x@y.z", `{"admin":false,"email":"x@y.z","id":"u1","person":true,"source":"pat"}`},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("POST", "/tools/who/mcp", nil)
		req.Header.Set(wickplugin.HeaderTokenUserID, tc.id)
		req.Header.Set(wickplugin.HeaderTokenUserRole, tc.role)
		if tc.email != "" {
			req.Header.Set(wickplugin.HeaderTokenUserEmail, tc.email)
		}
		if tc.source != "" {
			req.Header.Set(wickplugin.HeaderTokenUserSource, tc.source)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != tc.want {
			t.Errorf("%s: %d %s, want %s", tc.name, rec.Code, rec.Body.String(), tc.want)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/tools/who/mcp", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no injected identity: code %d, want 401", rec.Code)
	}
}
