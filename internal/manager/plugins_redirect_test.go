package manager

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

type redirectTestSecrets struct{}

func (redirectTestSecrets) SessionSecret() string      { return "test-secret" }
func (redirectTestSecrets) AdminPasswordChanged() bool { return true }

func TestManagerPluginsRedirectsToAdmin(t *testing.T) {
	mux := http.NewServeMux()
	midd := login.NewMiddleware(nil, redirectTestSecrets{})
	// Same catch-all Register adds: the specific route must win over it.
	mux.Handle("GET /manager/{path...}", http.NotFoundHandler())
	registerPluginsRedirect(mux, func(next http.HandlerFunc) http.Handler { return midd.RequireAuth(next) })
	u := &entity.User{ID: "a1", Role: entity.RoleAdmin, Approved: true}
	for path, want := range map[string]string{
		"/manager/plugins":             "/admin/plugins",
		"/manager/plugins?tab=sources": "/admin/plugins?tab=sources",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(login.WithUser(req.Context(), u, nil))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("%s = %d %q, want 302 %q", path, rec.Code, rec.Header().Get("Location"), want)
		}
	}
}
