package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
	"gorm.io/gorm"
)

// newHideHandler wires connectors and the login service on ONE database,
// the way production does, so the hide endpoint writes the same users row
// the rows endpoint reads back.
func newHideHandler(t *testing.T) (*Handler, *connectors.Service, *login.Service, func(email string) *entity.User, *gorm.DB) {
	t.Helper()
	db := newAPISQLite(t)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	svc := connectors.NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	if err := svc.Bootstrap(context.Background(), []connector.Module{apiDetailModule("slack")}); err != nil {
		t.Fatalf("connectors bootstrap: %v", err)
	}
	users := login.NewService(db, "")
	mkUser := func(email string) *entity.User {
		u, err := users.UpsertUser(context.Background(), email, email, "")
		if err != nil {
			t.Fatalf("upsert %s: %v", email, err)
		}
		// Plain user regardless of first-user promotion: hiding must not
		// lean on admin rights.
		u.Role = entity.RoleUser
		return u
	}
	return &Handler{connectors: svc, users: users}, svc, users, mkUser, db
}

func hideReq(t *testing.T, h *Handler, user *entity.User, id string, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/manager/api/connectors/slack/"+id+"/hide", bytes.NewReader([]byte(body)))
	req.SetPathValue("key", "slack")
	req.SetPathValue("id", id)
	req = req.WithContext(login.WithUser(req.Context(), user, nil))
	rec := httptest.NewRecorder()
	h.apiSetConnectorHidden(rec, req)
	return rec
}

// rowsAs reads GET /manager/api/connectors/slack as user, reloaded from the
// DB the way the session middleware does on every request.
func rowsAs(t *testing.T, h *Handler, users *login.Service, user *entity.User) map[string]bool {
	t.Helper()
	fresh, err := users.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	fresh.Role = user.Role
	req := httptest.NewRequest(http.MethodGet, "/manager/api/connectors/slack", nil)
	req.SetPathValue("key", "slack")
	req = req.WithContext(login.WithUser(req.Context(), fresh, nil))
	rec := httptest.NewRecorder()
	h.apiConnectorRows(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rows status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var got connectorListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode rows: %v", err)
	}
	out := map[string]bool{}
	for _, r := range got.Rows {
		out[r.ID] = r.HiddenForMe
	}
	return out
}

func TestAPISetConnectorHidden(t *testing.T) {
	h, svc, users, mkUser, _ := newHideHandler(t)
	alice := mkUser("alice@example.test")
	bob := mkUser("bob@example.test")
	// Owned by someone else: alice can see it but not configure it, and
	// hiding must still work for her.
	row, err := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-owner")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if h.canConfigureRow(alice, row) {
		t.Fatal("test setup: alice should be view-only on the row")
	}

	rec := hideReq(t, h, alice, row.ID, `{"hidden":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("hide status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Hidden    bool     `json:"hidden"`
		HiddenIDs []string `json:"hidden_ids"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Hidden || !reflect.DeepEqual(resp.HiddenIDs, []string{row.ID}) {
		t.Fatalf("resp = %+v", resp)
	}
	// Idempotent: a double click keeps it hidden instead of toggling.
	if rec := hideReq(t, h, alice, row.ID, `{"hidden":true}`); rec.Code != http.StatusOK {
		t.Fatalf("second hide status = %d", rec.Code)
	}

	if got := rowsAs(t, h, users, alice); !got[row.ID] {
		t.Errorf("alice rows: hidden_for_me = false, want true (%v)", got)
	}
	if got := rowsAs(t, h, users, bob); got[row.ID] {
		t.Errorf("bob rows: hidden_for_me leaked from alice (%v)", got)
	}

	rec = hideReq(t, h, alice, row.ID, `{"hidden":false}`)
	if rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(`"hidden_ids":[]`)) {
		t.Fatalf("unhide = %d %s", rec.Code, rec.Body.String())
	}
	if got := rowsAs(t, h, users, alice); got[row.ID] {
		t.Errorf("alice rows after unhide: still hidden (%v)", got)
	}
}

func TestAPISetConnectorHiddenRejects(t *testing.T) {
	h, svc, _, mkUser, db := newHideHandler(t)
	alice := mkUser("alice@example.test")
	row, _ := svc.Create(t.Context(), "slack", "Prod", map[string]string{}, "u-owner")

	// A row alice cannot see: filter-tagged with a tag she does not carry.
	secret, _ := svc.Create(t.Context(), "slack", "Secret", map[string]string{}, "u-owner")
	tag := entity.Tag{Name: "secret-team", IsFilter: true}
	if err := db.Create(&tag).Error; err != nil {
		t.Fatalf("tag: %v", err)
	}
	if err := db.Create(&entity.ToolTag{ToolPath: "/connectors/" + secret.ID, TagID: tag.ID}).Error; err != nil {
		t.Fatalf("tool_tag: %v", err)
	}

	cases := []struct {
		name string
		id   string
		body string
		want int
	}{
		{"invisible row", secret.ID, `{"hidden":true}`, http.StatusNotFound},
		{"unknown row", "no-such-id", `{"hidden":true}`, http.StatusNotFound},
		{"session workspace id", "sw_x", `{"hidden":true}`, http.StatusBadRequest},
		{"account id", "a/b", `{"hidden":true}`, http.StatusBadRequest},
		{"missing hidden", row.ID, `{}`, http.StatusBadRequest},
		{"bad json", row.ID, `nope`, http.StatusBadRequest},
		// Unhide skips the row lookup so a deleted instance can be cleared.
		{"unhide deleted row", "deleted-id", `{"hidden":false}`, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := hideReq(t, h, alice, tc.id, tc.body); rec.Code != tc.want {
				t.Errorf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	t.Run("no user service", func(t *testing.T) {
		bare := &Handler{connectors: svc}
		if rec := hideReq(t, bare, alice, row.ID, `{"hidden":true}`); rec.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503", rec.Code)
		}
	})
}
