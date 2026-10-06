package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// pluginsMux wires the real admin routes, so the test covers the route and
// its admin gate, not just the page handler.
func pluginsMux() *http.ServeMux {
	mux := http.NewServeMux()
	(&Handler{}).Register(mux, login.NewMiddleware(nil, testSecrets{}))
	return mux
}

func getAs(mux *http.ServeMux, path string, u *entity.User) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if u != nil {
		req = req.WithContext(login.WithUser(req.Context(), u, nil))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAdminPluginsPageForAdmin(t *testing.T) {
	admin := &entity.User{ID: "a1", Name: "Admin", Role: entity.RoleAdmin, Approved: true}
	rec := getAs(pluginsMux(), "/admin/plugins", admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`data-embed="plugins"`, `href="/admin/plugins"`, "Admin · Plugins"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// A non-admin gets exactly what every other /admin page gives them.
func TestAdminPluginsPageNonAdminMatchesOtherAdminPages(t *testing.T) {
	user := &entity.User{ID: "u1", Name: "User", Role: entity.RoleUser, Approved: true}
	mux := pluginsMux()
	for _, u := range []*entity.User{user, nil} {
		got := getAs(mux, "/admin/plugins", u)
		ref := getAs(mux, "/admin/tags", u)
		if got.Code != ref.Code || got.Header().Get("Location") != ref.Header().Get("Location") {
			t.Errorf("user=%v: /admin/plugins = %d %q, /admin/tags = %d %q", u != nil,
				got.Code, got.Header().Get("Location"), ref.Code, ref.Header().Get("Location"))
		}
		if got.Code == http.StatusOK {
			t.Errorf("user=%v: /admin/plugins served the page", u != nil)
		}
	}
}
