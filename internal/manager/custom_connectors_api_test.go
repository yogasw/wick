package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	customconn "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/postgres"
)

func newCustomAPIDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: postgres.NewLogLevel("silent"),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	postgres.Migrate(db)
	return db
}

// newCustomHandler wires a Handler with a real custom-connector service on
// an in-memory DB so the JSON endpoints exercise the live service +
// requireDefMutableJSON gate without touching the SPA embed.
func newCustomHandler(t *testing.T) (*Handler, *customconn.Service) {
	t.Helper()
	db := newCustomAPIDB(t)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	connSvc := connectors.NewServiceFromDB(db)
	connSvc.SetConfigs(cfgsSvc)
	if err := connSvc.Bootstrap(context.Background(), nil); err != nil {
		t.Fatalf("connectors bootstrap: %v", err)
	}
	custom := customconn.New(customconn.Deps{
		DB:         db,
		Connectors: connSvc,
		Keys:       cfgsSvc,
	})
	return &Handler{connectors: connSvc, configs: cfgsSvc, custom: custom}, custom
}

func seedDef(t *testing.T, svc *customconn.Service, key, createdBy string) *entity.CustomConnector {
	t.Helper()
	def := &entity.CustomConnector{
		Key:         key,
		Name:        "Petstore",
		Description: "A pet store",
		Icon:        "🐾",
		Source:      "curl",
		SourceMeta:  `{"category":"API","health_op":"list_pets"}`,
		Configs:     `[{"key":"base_url","widget":"url","required":true}]`,
		Ops:         `[{"key":"list_pets","name":"List Pets","description":"List.","inputs":[],"request":{"method":"GET","url_template":"{{.cfg.base_url}}/pets"}}]`,
		CreatedBy:   createdBy,
	}
	if err := svc.Store().CreateDef(context.Background(), def); err != nil {
		t.Fatalf("seed def: %v", err)
	}
	return def
}

func reqWithUser(method, path string, user *entity.User) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	return req.WithContext(login.WithUser(req.Context(), user, nil))
}

// TestAPIConnectorReloadRejectsNonCustom verifies the reload endpoint is
// wired and rejects a non-custom connector key.
func TestAPIConnectorReloadRejectsNonCustom(t *testing.T) {
	h, _ := newCustomHandler(t)
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}
	req := reqWithUser(http.MethodPost, "/manager/api/connectors/slack/reload", admin)
	req.SetPathValue("key", "slack")
	rec := httptest.NewRecorder()
	h.apiConnectorReload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (not a custom connector); body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPICustomMeta(t *testing.T) {
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}

	t.Run("returns categories and empty providers", func(t *testing.T) {
		h, _ := newCustomHandler(t)
		rec := httptest.NewRecorder()
		h.apiCustomMeta(rec, reqWithUser(http.MethodGet, "/manager/api/connectors/custom/meta", admin))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var got customMetaResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
		}
		if len(got.Categories) == 0 {
			t.Errorf("categories empty, want a non-empty catalog")
		}
		if got.AIProviders == nil {
			t.Errorf("ai_providers = nil, want [] (JSON encodes empty slice)")
		}
	})

	t.Run("404 when service not wired", func(t *testing.T) {
		h := &Handler{}
		rec := httptest.NewRecorder()
		h.apiCustomMeta(rec, reqWithUser(http.MethodGet, "/manager/api/connectors/custom/meta", admin))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestAPICustomDraft(t *testing.T) {
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}
	other := &entity.User{ID: "u-other", Role: entity.RoleUser}

	cases := []struct {
		name     string
		defID    func(d *entity.CustomConnector) string
		user     *entity.User
		wantCode int
		check    func(t *testing.T, res customDraftResponse)
	}{
		{
			name:     "admin gets the draft",
			defID:    func(d *entity.CustomConnector) string { return d.ID },
			user:     admin,
			wantCode: http.StatusOK,
			check: func(t *testing.T, res customDraftResponse) {
				if res.Draft == nil {
					t.Fatalf("draft nil")
				}
				if res.Draft.Key != "petstore" || res.Draft.Name != "Petstore" {
					t.Errorf("meta wrong: %+v", res.Draft)
				}
				if res.Draft.Category != "API" {
					t.Errorf("category = %q, want API", res.Draft.Category)
				}
				if res.Draft.HealthOp != "list_pets" {
					t.Errorf("health_op = %q, want list_pets", res.Draft.HealthOp)
				}
				if len(res.Draft.Configs) != 1 || len(res.Draft.Ops) != 1 {
					t.Errorf("configs/ops counts: %d/%d", len(res.Draft.Configs), len(res.Draft.Ops))
				}
			},
		},
		{
			name:     "non-creator non-admin gets 404",
			defID:    func(d *entity.CustomConnector) string { return d.ID },
			user:     other,
			wantCode: http.StatusNotFound,
		},
		{
			name:     "missing def gets 404",
			defID:    func(d *entity.CustomConnector) string { return "does-not-exist" },
			user:     admin,
			wantCode: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, svc := newCustomHandler(t)
			def := seedDef(t, svc, "petstore", admin.ID)
			id := tc.defID(def)

			req := reqWithUser(http.MethodGet, "/manager/api/connectors/custom/"+id+"/draft", tc.user)
			req.SetPathValue("defID", id)
			rec := httptest.NewRecorder()
			h.apiCustomDraft(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.check != nil {
				var res customDraftResponse
				if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
					t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
				}
				tc.check(t, res)
			}
		})
	}
}

func TestAPICustomSetDisabled(t *testing.T) {
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}

	cases := []struct {
		name         string
		disabled     bool
		wantDisabled bool
	}{
		{name: "disable", disabled: true, wantDisabled: true},
		{name: "enable", disabled: false, wantDisabled: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, svc := newCustomHandler(t)
			def := seedDef(t, svc, "petstore", admin.ID)

			req := reqWithUser(http.MethodPost, "/manager/api/connectors/custom/"+def.ID+"/disable", admin)
			req.SetPathValue("defID", def.ID)
			rec := httptest.NewRecorder()
			h.apiCustomSetDisabled(tc.disabled)(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			var got map[string]bool
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got["disabled"] != tc.wantDisabled {
				t.Errorf("disabled = %v, want %v", got["disabled"], tc.wantDisabled)
			}
			stored, err := svc.Store().GetDef(context.Background(), def.ID)
			if err != nil {
				t.Fatalf("reload def: %v", err)
			}
			if stored.Disabled != tc.wantDisabled {
				t.Errorf("persisted disabled = %v, want %v", stored.Disabled, tc.wantDisabled)
			}
		})
	}

	t.Run("non-creator non-admin gets 404", func(t *testing.T) {
		h, svc := newCustomHandler(t)
		def := seedDef(t, svc, "petstore", admin.ID)
		other := &entity.User{ID: "u-other", Role: entity.RoleUser}

		req := reqWithUser(http.MethodPost, "/manager/api/connectors/custom/"+def.ID+"/disable", other)
		req.SetPathValue("defID", def.ID)
		rec := httptest.NewRecorder()
		h.apiCustomSetDisabled(true)(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
	})
}

func TestAPICustomDelete(t *testing.T) {
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}

	t.Run("admin deletes the def", func(t *testing.T) {
		h, svc := newCustomHandler(t)
		def := seedDef(t, svc, "petstore", admin.ID)

		req := reqWithUser(http.MethodPost, "/manager/api/connectors/custom/"+def.ID+"/delete", admin)
		req.SetPathValue("defID", def.ID)
		rec := httptest.NewRecorder()
		h.apiCustomDelete(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		if _, err := svc.Store().GetDef(context.Background(), def.ID); err == nil {
			t.Errorf("def still present after delete")
		}
	})

	t.Run("non-creator non-admin gets 404", func(t *testing.T) {
		h, svc := newCustomHandler(t)
		def := seedDef(t, svc, "petstore", admin.ID)
		other := &entity.User{ID: "u-other", Role: entity.RoleUser}

		req := reqWithUser(http.MethodPost, "/manager/api/connectors/custom/"+def.ID+"/delete", other)
		req.SetPathValue("defID", def.ID)
		rec := httptest.NewRecorder()
		h.apiCustomDelete(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", rec.Code)
		}
		if _, err := svc.Store().GetDef(context.Background(), def.ID); err != nil {
			t.Errorf("def removed despite 404")
		}
	})
}
