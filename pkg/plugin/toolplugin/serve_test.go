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
