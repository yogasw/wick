package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
)

func shareTestHandler(t *testing.T) (*Handler, *connectors.Service) {
	t.Helper()
	svc := newConnectorsSvcForAPI(t, []connector.Module{
		apiTestModule("acme", "Acme", "🔌", tags.Connector),
		apiTestModule("open", "Open", "🔌", tags.Connector),
		apiTestModule("maint", "Maintenance", "🛠", tags.Connector, tags.System),
	})
	svc.SetTypeOwnerResolver(func(_ context.Context, key string) string {
		if key == "acme" {
			return "u-owner"
		}
		return ""
	})
	if err := svc.RestrictType(context.Background(), "acme", "u-owner"); err != nil {
		t.Fatalf("restrict: %v", err)
	}
	return &Handler{connectors: svc}, svc
}

func shareReq(method, key string, user *entity.User, body any) *http.Request {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, "/manager/api/connectors/"+key+"/sharing", &buf)
	req.SetPathValue("key", key)
	return req.WithContext(login.WithUser(req.Context(), user, nil))
}

func TestConnectorShareAPI(t *testing.T) {
	owner := &entity.User{ID: "u-owner", Role: entity.RoleUser, Approved: true}
	stranger := &entity.User{ID: "u-stranger", Role: entity.RoleUser, Approved: true}
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin, Approved: true}

	catalogue := func(h *Handler, u *entity.User) map[string]connectorDef {
		req := httptest.NewRequest(http.MethodGet, "/manager/api/connectors", nil)
		req = req.WithContext(login.WithUser(req.Context(), u, nil))
		rec := httptest.NewRecorder()
		h.apiConnectors(rec, req)
		var got []connectorDef
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := map[string]connectorDef{}
		for _, d := range got {
			out[d.Key] = d
		}
		return out
	}

	t.Run("a hidden connector is absent from a stranger's catalogue", func(t *testing.T) {
		h, _ := shareTestHandler(t)
		got := catalogue(h, stranger)
		if _, ok := got["acme"]; ok {
			t.Fatal("stranger must not see a hidden connector")
		}
		if _, ok := got["open"]; !ok {
			t.Fatal("an open connector stays listed")
		}
	})

	t.Run("its owner sees it, restricted and shareable", func(t *testing.T) {
		h, _ := shareTestHandler(t)
		d, ok := catalogue(h, owner)["acme"]
		if !ok {
			t.Fatal("owner must see their connector")
		}
		if d.Visibility != "restricted" || !d.CanShare {
			t.Fatalf("got visibility=%q can_share=%v", d.Visibility, d.CanShare)
		}
		if catalogue(h, owner)["open"].CanShare {
			t.Fatal("a non-owner, non-admin cannot share a built-in")
		}
	})

	t.Run("stranger gets not found, never a hint it exists", func(t *testing.T) {
		h, _ := shareTestHandler(t)
		rec := httptest.NewRecorder()
		h.apiConnectorSharing(rec, shareReq(http.MethodGet, "acme", stranger, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("a non-owner cannot change sharing of a visible connector", func(t *testing.T) {
		h, _ := shareTestHandler(t)
		rec := httptest.NewRecorder()
		h.apiSetConnectorSharing(rec, shareReq(http.MethodPost, "open", stranger, map[string]string{"action": "restrict"}))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})

	t.Run("the owner cannot open to everyone or link tags", func(t *testing.T) {
		h, _ := shareTestHandler(t)
		for _, action := range []string{"everyone", "link_tag", "unlink_tag"} {
			rec := httptest.NewRecorder()
			h.apiSetConnectorSharing(rec, shareReq(http.MethodPost, "acme", owner, map[string]string{"action": action, "tag_id": "x"}))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s: status = %d, want 403", action, rec.Code)
			}
		}
	})

	t.Run("an admin opens it to everyone", func(t *testing.T) {
		h, _ := shareTestHandler(t)
		rec := httptest.NewRecorder()
		h.apiSetConnectorSharing(rec, shareReq(http.MethodPost, "acme", admin, map[string]string{"action": "everyone"}))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
		}
		var out sharingJSON
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if out.Visibility != "everyone" {
			t.Fatalf("visibility = %q", out.Visibility)
		}
		if _, ok := catalogue(h, stranger)["acme"]; !ok {
			t.Fatal("stranger sees it once open")
		}
	})

	t.Run("system connectors are never shareable", func(t *testing.T) {
		h, _ := shareTestHandler(t)
		rec := httptest.NewRecorder()
		h.apiSetConnectorSharing(rec, shareReq(http.MethodPost, "maint", admin, map[string]string{"action": "everyone"}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("sharing with a person on an open connector is refused", func(t *testing.T) {
		h, svc := shareTestHandler(t)
		if err := svc.ShareTypeWithUser(context.Background(), "open", "u-x"); err == nil {
			t.Fatal("want ErrTypeOpen")
		}
		_ = h
	})
}
