package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/tool"
)

func hookMeta(key string) tool.Tool {
	return tool.Tool{Key: key, Name: key, Path: "/tools/" + key}
}

// TestWebhookGroupPathResolution pins that group and route paths compose
// against the tool's mount point the same way ordinary routes do.
func TestWebhookGroupPathResolution(t *testing.T) {
	tr := newToolRouter(nil)
	tr.withScope(hookMeta("hooks"), false, func(r tool.Router) {
		wh := r.WebhookGroup("/webhook")
		wh.POST("/receive", func(c *tool.WebhookCtx) {})
		wh.GET("/health", func(c *tool.WebhookCtx) {})
		// Bare "/" means the group prefix itself.
		wh.PUT("/", func(c *tool.WebhookCtx) {})
		// A prefix without a leading slash resolves the same way.
		other := r.WebhookGroup("callback")
		other.PATCH("nested/deep", func(c *tool.WebhookCtx) {})
	})

	got := map[string]bool{}
	for _, hk := range tr.hooks {
		got[hk.method+" "+hk.path] = true
	}
	want := []string{
		"POST /tools/hooks/webhook/receive",
		"GET /tools/hooks/webhook/health",
		"PUT /tools/hooks/webhook",
		"PATCH /tools/hooks/callback/nested/deep",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing route %q; have %v", w, got)
		}
	}
	if len(tr.hooks) != len(want) {
		t.Fatalf("got %d hooks, want %d", len(tr.hooks), len(want))
	}
}

// TestWebhookPrefixesDeduped pins that several routes in one group yield a
// single exempt prefix, and that prefixes are sorted for a stable listing.
func TestWebhookPrefixesDeduped(t *testing.T) {
	tr := newToolRouter(nil)
	tr.withScope(hookMeta("hooks"), false, func(r tool.Router) {
		wh := r.WebhookGroup("/webhook")
		wh.POST("/a", func(c *tool.WebhookCtx) {})
		wh.POST("/b", func(c *tool.WebhookCtx) {})
		alt := r.WebhookGroup("/callback")
		alt.POST("/c", func(c *tool.WebhookCtx) {})
	})

	got := tr.WebhookPrefixes()
	want := []string{"/tools/hooks/callback", "/tools/hooks/webhook"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestWebhookGroupScopedPerTool pins that a group captures the meta active
// when it was created, so holding it past the end of Register cannot leak
// another tool's key into the path or the config scope.
func TestWebhookGroupScopedPerTool(t *testing.T) {
	tr := newToolRouter(nil)
	var escaped tool.WebhookRouter
	tr.withScope(hookMeta("first"), false, func(r tool.Router) {
		escaped = r.WebhookGroup("/webhook")
	})
	tr.withScope(hookMeta("second"), false, func(r tool.Router) {
		// Declared while "second" is the active scope, but the group
		// belongs to "first".
		escaped.POST("/late", func(c *tool.WebhookCtx) {})
	})

	if len(tr.hooks) != 1 {
		t.Fatalf("got %d hooks, want 1", len(tr.hooks))
	}
	if got := tr.hooks[0].path; got != "/tools/first/webhook/late" {
		t.Fatalf("path %q leaked the wrong tool scope", got)
	}
	if got := tr.hooks[0].toolKey; got != "first" {
		t.Fatalf("toolKey %q leaked the wrong tool scope", got)
	}
}

// TestWebhookValidateRejectsWholeToolGroup pins the boot-time refusal of a
// group that would strip authentication from an entire tool.
func TestWebhookValidateRejectsWholeToolGroup(t *testing.T) {
	for _, prefix := range []string{"/", ""} {
		tr := newToolRouter(nil)
		tr.withScope(hookMeta("hooks"), false, func(r tool.Router) {
			r.WebhookGroup(prefix).POST("/x", func(c *tool.WebhookCtx) {})
		})
		err := tr.validate()
		if err == nil {
			t.Fatalf("WebhookGroup(%q) was accepted; it exposes the whole tool", prefix)
		}
		if !strings.Contains(err.Error(), "without authentication") {
			t.Fatalf("WebhookGroup(%q): unexpected error %q", prefix, err.Error())
		}
	}
}

// TestWebhookValidateRejectsCollisionWithGatedRoute pins that a webhook
// route cannot shadow an ordinary route at the same METHOD PATH. Allowing
// it would quietly remove the access check from the gated route.
func TestWebhookValidateRejectsCollisionWithGatedRoute(t *testing.T) {
	tr := newToolRouter(nil)
	tr.withScope(hookMeta("hooks"), false, func(r tool.Router) {
		r.POST("/webhook/receive", func(c *tool.Ctx) {})
		r.WebhookGroup("/webhook").POST("/receive", func(c *tool.WebhookCtx) {})
	})
	err := tr.validate()
	if err == nil {
		t.Fatal("colliding webhook route was accepted")
	}
	if !strings.Contains(err.Error(), "duplicate route") {
		t.Fatalf("unexpected error: %q", err.Error())
	}
}

// TestWebhookValidateRejectsDuplicateHookRoutes pins that two groups
// cannot claim the same METHOD PATH either.
func TestWebhookValidateRejectsDuplicateHookRoutes(t *testing.T) {
	tr := newToolRouter(nil)
	tr.withScope(hookMeta("hooks"), false, func(r tool.Router) {
		r.WebhookGroup("/webhook").POST("/receive", func(c *tool.WebhookCtx) {})
		r.WebhookGroup("/webhook").POST("/receive", func(c *tool.WebhookCtx) {})
	})
	if err := tr.validate(); err == nil {
		t.Fatal("duplicate webhook route was accepted")
	}
}

// TestWebhookValidateAcceptsDistinctSurfaces pins the intended shape: a
// gated page plus a webhook subtree in one module is valid.
func TestWebhookValidateAcceptsDistinctSurfaces(t *testing.T) {
	tr := newToolRouter(nil)
	tr.withScope(hookMeta("hooks"), false, func(r tool.Router) {
		r.GET("/", func(c *tool.Ctx) {})
		r.WebhookGroup("/webhook").POST("/receive", func(c *tool.WebhookCtx) {})
	})
	if err := tr.validate(); err != nil {
		t.Fatalf("valid module rejected: %s", err.Error())
	}
}

// TestWebhookMountServesHandler pins that a mounted webhook route reaches
// its handler with a usable WebhookCtx.
func TestWebhookMountServesHandler(t *testing.T) {
	tr := newToolRouter(nil)
	tr.withScope(hookMeta("hooks"), false, func(r tool.Router) {
		r.WebhookGroup("/webhook").POST("/receive/{id}", func(c *tool.WebhookCtx) {
			c.JSON(http.StatusOK, map[string]string{
				"id":   c.PathValue("id"),
				"base": c.Base(),
				"sig":  c.Header("X-Hook-Signature"),
			})
		})
	})
	if err := tr.validate(); err != nil {
		t.Fatalf("validate: %s", err.Error())
	}
	mux := http.NewServeMux()
	tr.mount(mux)

	req := httptest.NewRequest(http.MethodPost, "/tools/hooks/webhook/receive/abc", strings.NewReader("{}"))
	req.Header.Set("X-Hook-Signature", "deadbeef")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type %q, want application/json", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{`"id":"abc"`, `"base":"/tools/hooks"`, `"sig":"deadbeef"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body %s missing %s", body, want)
		}
	}
}

// TestWebhookRoutesListing pins the descriptor list the manager renders.
func TestWebhookRoutesListing(t *testing.T) {
	tr := newToolRouter(nil)
	tr.withScope(hookMeta("hooks"), false, func(r tool.Router) {
		wh := r.WebhookGroup("/webhook")
		wh.POST("/receive", func(c *tool.WebhookCtx) {})
		wh.GET("/health", func(c *tool.WebhookCtx) {})
	})
	tr.withScope(hookMeta("other"), false, func(r tool.Router) {
		r.WebhookGroup("/cb").POST("/x", func(c *tool.WebhookCtx) {})
	})

	routes := tr.WebhookRoutes()
	if len(routes) != 3 {
		t.Fatalf("got %d routes, want 3", len(routes))
	}
	// Sorted by path: /tools/hooks/webhook/health precedes .../receive,
	// and /tools/other/cb/x comes last.
	if routes[0].Path != "/tools/hooks/webhook/health" || routes[0].Method != "GET" {
		t.Errorf("routes[0] = %+v", routes[0])
	}
	if routes[2].ToolKey != "other" {
		t.Errorf("routes[2] = %+v, want ToolKey=other", routes[2])
	}
	for _, r := range routes {
		if r.Group == "" || !strings.HasPrefix(r.Path, r.Group) {
			t.Errorf("route %+v has an inconsistent group", r)
		}
	}
}
