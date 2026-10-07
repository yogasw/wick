package agents

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// A web terminal is a shell on the host: every route must refuse anyone
// who is not a provider admin, before touching the instance or a session.
func TestProviderTerminalRoutesAreAdminOnly(t *testing.T) {
	routes := map[string]struct {
		method string
		h      func(*tool.Ctx)
	}{
		"status": {"GET", apiProviderTerminalStatus},
		"start":  {"POST", apiProviderTerminalStart},
		"close":  {"POST", apiProviderTerminalClose},
		"proxy":  {"GET", apiProviderTerminalProxy},
	}
	users := map[string]*entity.User{
		"anonymous":     nil,
		"approved user": {Email: "u@example.com", Role: entity.RoleUser, Approved: true},
	}
	for rn, rt := range routes {
		for un, u := range users {
			t.Run(rn+"/"+un, func(t *testing.T) {
				r := httptest.NewRequest(rt.method, "/tools/agents/api/providers/opencode/x/terminal/abc/ws", nil)
				r.SetPathValue("type", "opencode")
				r.SetPathValue("name", "x")
				r.SetPathValue("id", "abc")
				if u != nil {
					r = r.WithContext(login.WithUser(r.Context(), u, nil))
				}
				w := httptest.NewRecorder()
				rt.h(tool.NewCtx(w, r, nil, tool.Tool{}, nil, nil))
				if w.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", w.Code)
				}
			})
		}
	}
}

// An admin naming a session that does not exist (or belongs to another
// instance) gets 404, never somebody else's terminal.
func TestProviderTerminalUnknownSession(t *testing.T) {
	admin := &entity.User{Email: "a@example.com", Role: entity.RoleAdmin, Approved: true}
	for _, h := range []func(*tool.Ctx){apiProviderTerminalProxy, apiProviderTerminalClose} {
		r := httptest.NewRequest("GET", "/x", nil)
		r.SetPathValue("type", "omp")
		r.SetPathValue("name", "other")
		r.SetPathValue("id", "nope")
		r = r.WithContext(login.WithUser(r.Context(), admin, nil))
		w := httptest.NewRecorder()
		h(tool.NewCtx(w, r, nil, tool.Tool{}, nil, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	}
}
