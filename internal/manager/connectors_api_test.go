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
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/postgres"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
)

func newAPISQLite(t *testing.T) *gorm.DB {
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

func apiTestModule(key, name, icon string, defaultTags ...tool.DefaultTag) connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:         key,
			Name:        name,
			Icon:        icon,
			DefaultTags: defaultTags,
		},
		Operations: []connector.Operation{
			{Key: "noop", Name: "Noop", Execute: func(*connector.Ctx) (any, error) { return nil, nil }},
		},
	}
}

func newConnectorsSvcForAPI(t *testing.T, mods []connector.Module) *connectors.Service {
	t.Helper()
	db := newAPISQLite(t)
	cfgsSvc := configs.NewService(db)
	if err := cfgsSvc.Bootstrap(context.Background()); err != nil {
		t.Fatalf("configs bootstrap: %v", err)
	}
	svc := connectors.NewServiceFromDB(db)
	svc.SetConfigs(cfgsSvc)
	if err := svc.Bootstrap(context.Background(), mods); err != nil {
		t.Fatalf("connectors bootstrap: %v", err)
	}
	return svc
}

func TestAPIConnectors(t *testing.T) {
	mods := []connector.Module{
		apiTestModule("slack", "Slack", "💬", tags.Connector, tags.Communication),
		apiTestModule("github", "GitHub", "🐙", tags.Connector, tags.Development),
		apiTestModule("maint", "Maintenance", "🛠", tags.Connector, tags.System),
	}

	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}
	normal := &entity.User{ID: "u-normal", Role: entity.RoleUser}

	cases := []struct {
		name       string
		user       *entity.User
		wantKeys   []string
		wantAbsent []string
	}{
		{
			name:     "admin sees all defs including system",
			user:     admin,
			wantKeys: []string{"slack", "github", "maint"},
		},
		{
			name:       "non-admin without rows sees nothing",
			user:       normal,
			wantKeys:   []string{},
			wantAbsent: []string{"slack", "github", "maint"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{connectors: newConnectorsSvcForAPI(t, mods)}

			req := httptest.NewRequest(http.MethodGet, "/manager/api/connectors", nil)
			req = req.WithContext(login.WithUser(req.Context(), tc.user, nil))
			rec := httptest.NewRecorder()

			h.apiConnectors(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", ct)
			}

			var got []connectorDef
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode body: %v; body=%s", err, rec.Body.String())
			}

			gotKeys := make(map[string]connectorDef, len(got))
			for _, d := range got {
				gotKeys[d.Key] = d
			}
			for _, k := range tc.wantKeys {
				if _, ok := gotKeys[k]; !ok {
					t.Errorf("missing connector %q in response %+v", k, got)
				}
			}
			for _, k := range tc.wantAbsent {
				if _, ok := gotKeys[k]; ok {
					t.Errorf("connector %q should not be present for this user", k)
				}
			}
		})
	}
}

func TestAPIConnectorsShape(t *testing.T) {
	mods := []connector.Module{
		apiTestModule("slack", "Slack", "💬", tags.Connector, tags.Communication),
	}
	h := &Handler{connectors: newConnectorsSvcForAPI(t, mods)}
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}

	req := httptest.NewRequest(http.MethodGet, "/manager/api/connectors", nil)
	req = req.WithContext(login.WithUser(req.Context(), admin, nil))
	rec := httptest.NewRecorder()
	h.apiConnectors(rec, req)

	var got []connectorDef
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	d := got[0]
	if d.Key != "slack" || d.Name != "Slack" || d.Icon != "💬" {
		t.Errorf("identity fields wrong: %+v", d)
	}
	if d.Category != tags.Communication.Name {
		t.Errorf("category = %q, want %q", d.Category, tags.Communication.Name)
	}
	if d.Custom {
		t.Errorf("custom should be false for a code-defined connector")
	}
}
