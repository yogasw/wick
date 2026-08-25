package custom

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/postgres"
)

// ── mapInputSchema ───────────────────────────────────────────────────

func TestMapInputSchema(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"name", "count"},
		"properties": map[string]any{
			"name":    map[string]any{"type": "string", "description": "Display name"},
			"count":   map[string]any{"type": "integer", "default": float64(5)},
			"ratio":   map[string]any{"type": "number"},
			"active":  map[string]any{"type": "boolean"},
			"mode":    map[string]any{"type": "string", "enum": []any{"a", "b"}},
			"site":    map[string]any{"type": "string", "format": "uri"},
			"api_key": map[string]any{"type": "string"},
			"passwd":  map[string]any{"type": "string", "format": "password"},
			"auth_hdr": map[string]any{
				"type": "string", "description": "Bearer token to send",
			},
			"tags_x": map[string]any{"type": "array"},
			"extra":  map[string]any{"type": "object", "description": "Extra payload"},
		},
	}
	fields := mapInputSchema(schema)

	byKey := map[string]DefField{}
	for _, f := range fields {
		byKey[f.Key] = f
	}
	want := map[string]struct {
		widget   string
		secret   bool
		required bool
	}{
		"name":     {widget: "text", required: true},
		"count":    {widget: "number", required: true},
		"ratio":    {widget: "number"},
		"active":   {widget: "checkbox"},
		"mode":     {widget: "dropdown"},
		"site":     {widget: "url"},
		"api_key":  {widget: "secret", secret: true}, // name heuristic
		"passwd":   {widget: "secret", secret: true}, // format
		"auth_hdr": {widget: "secret", secret: true}, // description heuristic
		"tags_x":   {widget: "textarea"},
		"extra":    {widget: "textarea"},
	}
	if len(fields) != len(want) {
		t.Fatalf("got %d fields, want %d: %+v", len(fields), len(want), fields)
	}
	for key, w := range want {
		f, ok := byKey[key]
		if !ok {
			t.Errorf("field %q missing", key)
			continue
		}
		if f.Widget != w.widget {
			t.Errorf("%s widget = %q, want %q", key, f.Widget, w.widget)
		}
		if f.Secret != w.secret {
			t.Errorf("%s secret = %v, want %v", key, f.Secret, w.secret)
		}
		if f.Required != w.required {
			t.Errorf("%s required = %v, want %v", key, f.Required, w.required)
		}
	}
	if f := byKey["mode"]; f.Options != "a|b" {
		t.Errorf("mode options = %q", f.Options)
	}
	if f := byKey["count"]; f.Default != "5" {
		t.Errorf("count default = %q", f.Default)
	}
	if f := byKey["extra"]; !strings.HasSuffix(f.Desc, "(raw JSON)") {
		t.Errorf("extra desc = %q, want (raw JSON) suffix", f.Desc)
	}
	if f := byKey["name"]; f.Desc != "Display name" {
		t.Errorf("name desc = %q", f.Desc)
	}

	// Output is sorted by property name.
	keys := make([]string, 0, len(fields))
	for _, f := range fields {
		keys = append(keys, f.Key)
	}
	if !sort.StringsAreSorted(keys) {
		t.Errorf("fields not sorted: %v", keys)
	}

	if got := mapInputSchema(nil); len(got) != 0 {
		t.Errorf("nil schema → %+v, want empty", got)
	}
	if got := mapInputSchema(map[string]any{"type": "object"}); len(got) != 0 {
		t.Errorf("no properties → %+v, want empty", got)
	}
}

// ── small package funcs ──────────────────────────────────────────────

func TestFilterTagName(t *testing.T) {
	if got := FilterTagName("github"); got != "custom:github" {
		t.Errorf("FilterTagName = %q", got)
	}
}

func TestCategoryNames(t *testing.T) {
	names := CategoryNames()
	if len(names) != 6 {
		t.Errorf("len = %d, want 6: %v", len(names), names)
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("not sorted: %v", names)
	}
	for _, n := range names {
		if n == "" {
			t.Error("empty category name")
		}
	}
}

// ── ServerForm validation ────────────────────────────────────────────

func TestServerFormValidate(t *testing.T) {
	cases := []struct {
		name    string
		form    ServerForm
		wantErr string
	}{
		{
			name:    "missing label",
			form:    ServerForm{URL: "https://mcp.example.com", AuthScheme: "none"},
			wantErr: "label is required",
		},
		{
			name:    "non-http url",
			form:    ServerForm{Label: "x", URL: "ftp://mcp.example.com", AuthScheme: "none"},
			wantErr: "must be http(s)",
		},
		{
			name:    "empty url",
			form:    ServerForm{Label: "x", AuthScheme: "none"},
			wantErr: "must be http(s)",
		},
		{
			name:    "unknown scheme",
			form:    ServerForm{Label: "x", URL: "https://mcp.example.com", AuthScheme: "magic"},
			wantErr: "unknown auth scheme",
		},
		{
			name:    "bearer without token",
			form:    ServerForm{Label: "x", URL: "https://mcp.example.com", AuthScheme: "bearer"},
			wantErr: "needs a token",
		},
		{
			name: "bearer with token ok",
			form: ServerForm{Label: "x", URL: "https://mcp.example.com", AuthScheme: "bearer", AuthSecret: "tok"},
		},
		{
			name: "none ok",
			form: ServerForm{Label: "x", URL: "http://localhost:9000/mcp", AuthScheme: "none"},
		},
		{
			name: "sso ok",
			form: ServerForm{Label: "x", URL: "https://mcp.example.com", AuthScheme: "sso"},
		},
		{
			name: "custom_header ok",
			form: ServerForm{Label: "x", URL: "https://mcp.example.com", AuthScheme: "custom_header"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.form.validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

// ── Store round-trip (in-memory sqlite) ──────────────────────────────

func newTestDB(t *testing.T) *gorm.DB {
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

func TestStoreDefRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewStore(newTestDB(t))

	def := &entity.CustomConnector{
		Key:        "petstore",
		Name:       "Petstore",
		Source:     "curl",
		SourceMeta: `{"category":"API"}`,
		Configs:    `[{"key":"base_url","widget":"url","required":true}]`,
		Ops:        `[{"title":"Pets","ops":[{"key":"list_pets","name":"List Pets","description":"List.","inputs":[],"request":{"method":"GET","url_template":"{{.cfg.base_url}}/pets"}}]}]`,
		CreatedBy:  "admin-1",
	}
	if err := store.CreateDef(ctx, def); err != nil {
		t.Fatalf("CreateDef: %v", err)
	}
	if def.ID == "" {
		t.Fatal("ID not assigned on create")
	}

	got, err := store.GetDef(ctx, def.ID)
	if err != nil {
		t.Fatalf("GetDef: %v", err)
	}
	if got.Key != "petstore" || got.Name != "Petstore" || got.CreatedBy != "admin-1" {
		t.Errorf("GetDef = %+v", got)
	}

	byKey, err := store.GetDefByKey(ctx, "petstore")
	if err != nil || byKey.ID != def.ID {
		t.Errorf("GetDefByKey = %+v, %v", byKey, err)
	}

	// Duplicate key rejected with ErrKeyTaken.
	dup := &entity.CustomConnector{Key: "petstore", Name: "Other", Source: "curl"}
	if err := store.CreateDef(ctx, dup); !errors.Is(err, ErrKeyTaken) {
		t.Errorf("duplicate CreateDef err = %v, want ErrKeyTaken", err)
	}

	second := &entity.CustomConnector{Key: "weather", Name: "Weather", Source: "manual"}
	if err := store.CreateDef(ctx, second); err != nil {
		t.Fatalf("CreateDef second: %v", err)
	}
	defs, err := store.ListDefs(ctx)
	if err != nil || len(defs) != 2 {
		t.Fatalf("ListDefs = %d defs, %v", len(defs), err)
	}

	got.Name = "Petstore v2"
	got.Disabled = true
	if err := store.UpdateDef(ctx, got); err != nil {
		t.Fatalf("UpdateDef: %v", err)
	}
	updated, err := store.GetDef(ctx, def.ID)
	if err != nil {
		t.Fatalf("GetDef after update: %v", err)
	}
	if updated.Name != "Petstore v2" || !updated.Disabled {
		t.Errorf("update not persisted: %+v", updated)
	}
	if updated.Key != "petstore" {
		t.Errorf("key changed on update: %q", updated.Key)
	}

	if err := store.DeleteDef(ctx, def.ID); err != nil {
		t.Fatalf("DeleteDef: %v", err)
	}
	if _, err := store.GetDef(ctx, def.ID); err == nil {
		t.Error("GetDef should fail after delete")
	}
}

func TestStoreServerRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := NewStore(newTestDB(t))

	srv := &entity.CustomConnectorMCPServer{
		Label:       "Internal Tools",
		Transport:   "http",
		URL:         "https://mcp.example.com/rpc",
		AuthScheme:  "bearer",
		AuthSecret:  "wick_enc_tok",
		AuthHeaders: "[]",
		AuthExtra:   "{}",
		Headers:     "[]",
	}
	if err := store.CreateServer(ctx, srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	if srv.ID == "" {
		t.Fatal("ID not assigned on create")
	}

	got, err := store.GetServer(ctx, srv.ID)
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if got.Label != "Internal Tools" || got.AuthScheme != "bearer" {
		t.Errorf("GetServer = %+v", got)
	}

	got.LastTestOK = true
	if err := store.UpdateServer(ctx, got); err != nil {
		t.Fatalf("UpdateServer: %v", err)
	}
	updated, _ := store.GetServer(ctx, srv.ID)
	if !updated.LastTestOK {
		t.Error("LastTestOK not persisted")
	}

	list, err := store.ListServers(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListServers = %d rows, %v", len(list), err)
	}

	if err := store.DeleteServer(ctx, srv.ID); err != nil {
		t.Fatalf("DeleteServer: %v", err)
	}
	if _, err := store.GetServer(ctx, srv.ID); err == nil {
		t.Error("GetServer should fail after delete")
	}
}
