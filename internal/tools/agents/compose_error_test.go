package agents

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/tool"
)

// A failed new-session submit must not answer 2xx: the composer fetches and
// navigates to res.url on any 2xx, which dropped the user back on an empty
// "New session" with the error gone. It toasts a non-2xx body instead.
func TestRenderComposeReportsErrorAsNon2xx(t *testing.T) {
	for _, tc := range []struct{ msg, want string }{
		{"opencode instance oc: pick a model: no model", "no model"},
		{"", "Failed to create session."},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/tools/agents/", nil)
		c := tool.NewCtx(w, r, nil, tool.Tool{Key: "agents", Path: "/tools/agents"}, nil, nil)
		renderCompose(c, "hello", tc.msg)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", w.Code)
		}
		if body := w.Body.String(); !strings.Contains(body, tc.want) || strings.Contains(body, "<html") {
			t.Fatalf("body = %q", body)
		}
	}
}
