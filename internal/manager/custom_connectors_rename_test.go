package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/connectors"
	customconn "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// renameReq builds a POST carrying the JSON rename body, with the caller
// installed in the context the same way reqWithUser does.
func renameReq(t *testing.T, defID, name string, user *entity.User) *http.Request {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		t.Fatalf("marshal rename body: %v", err)
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/manager/api/connectors/custom/"+defID+"/rename",
		strings.NewReader(string(payload)),
	)
	req.SetPathValue("defID", defID)
	return req.WithContext(login.WithUser(req.Context(), user, nil))
}

// newCustomHandlerWithDB mirrors newCustomHandler but also hands back the
// DB, for tests that need to insert rows the connectors service would
// refuse (keys registered after its bootstrap-time module map was built).
func newCustomHandlerWithDB(t *testing.T) (*Handler, *customconn.Service, *gorm.DB) {
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
	custom := customconn.New(customconn.Deps{DB: db, Connectors: connSvc, Keys: cfgsSvc})
	return &Handler{connectors: connSvc, configs: cfgsSvc, custom: custom}, custom, db
}

// seedMCPDef creates an MCP-sourced def plus its backing server row, so
// handlers that gate on "is this an MCP connector" reach their real logic.
// The URL is never dialed by these tests — the guards run before any probe.
func seedMCPDef(t *testing.T, svc *customconn.Service, key, createdBy string) *entity.CustomConnector {
	t.Helper()
	ctx := context.Background()
	srv := &entity.CustomConnectorMCPServer{
		Label:       key,
		Transport:   "http",
		URL:         "https://mcp.invalid/rpc",
		AuthScheme:  "oauth",
		AuthHeaders: "[]",
		AuthExtra:   "{}",
		Headers:     "[]",
	}
	if err := svc.Store().CreateServer(ctx, srv); err != nil {
		t.Fatalf("seed mcp server: %v", err)
	}
	def := &entity.CustomConnector{
		Key:        key,
		Name:       key,
		Source:     entity.CustomConnectorSourceMCP,
		SourceMeta: `{"server_id":"` + srv.ID + `"}`,
		Configs:    `[]`,
		Ops:        `[]`,
		CreatedBy:  createdBy,
	}
	if err := svc.Store().CreateDef(ctx, def); err != nil {
		t.Fatalf("seed mcp def: %v", err)
	}
	return def
}

// TestAPICustomRename renames a def in place and asserts the immutable key
// survives — instances, access tags, and MCP tool ids all key off it.
func TestAPICustomRename(t *testing.T) {
	h, svc := newCustomHandler(t)
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}
	def := seedDef(t, svc, "petstore", "u-admin")

	rec := httptest.NewRecorder()
	h.apiCustomRename(rec, renameReq(t, def.ID, "Petstore Prod", admin))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got, err := svc.Store().GetDef(context.Background(), def.ID)
	if err != nil {
		t.Fatalf("GetDef: %v", err)
	}
	if got.Name != "Petstore Prod" {
		t.Errorf("Name = %q, want %q", got.Name, "Petstore Prod")
	}
	if got.Key != "petstore" {
		t.Errorf("key changed on rename: %q", got.Key)
	}
}

// A blank name is rejected rather than blanking the connector's label.
func TestAPICustomRenameRejectsBlank(t *testing.T) {
	h, svc := newCustomHandler(t)
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}
	def := seedDef(t, svc, "petstore", "u-admin")

	rec := httptest.NewRecorder()
	h.apiCustomRename(rec, renameReq(t, def.ID, "   ", admin))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	got, _ := svc.Store().GetDef(context.Background(), def.ID)
	if got.Name != "Petstore" {
		t.Errorf("Name mutated by rejected rename: %q", got.Name)
	}
}

// Renaming mutates a definition, so it sits behind the level-1 gate
// (admin ∨ creator). A stranger must not be able to rename someone
// else's connector.
func TestAPICustomRenameRejectsStranger(t *testing.T) {
	h, svc := newCustomHandler(t)
	def := seedDef(t, svc, "petstore", "u-owner")
	stranger := &entity.User{ID: "u-other", Role: entity.RoleUser}

	rec := httptest.NewRecorder()
	h.apiCustomRename(rec, renameReq(t, def.ID, "Hijacked", stranger))

	if rec.Code == http.StatusOK {
		t.Fatalf("stranger renamed another user's connector; body=%s", rec.Body.String())
	}
	got, _ := svc.Store().GetDef(context.Background(), def.ID)
	if got.Name != "Petstore" {
		t.Errorf("Name = %q, want it unchanged by a rejected rename", got.Name)
	}
}

// The resync endpoint accepts an optional instance_id so the probe runs
// under that instance's own OAuth account. An instance that does not belong
// to the requested connector must never be accepted as the probe identity —
// otherwise the id is a side door into another row's credentials.
//
// The def is registered first so the handler gets past its "is this a
// custom connector" check and actually reaches the instance guard.
func TestResyncInstanceMustBelongToConnector(t *testing.T) {
	h, svc, db := newCustomHandlerWithDB(t)
	ctx := context.Background()
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin}
	seedMCPDef(t, svc, "petstore_mcp", "u-admin")
	if err := svc.RegisterAllAtBoot(ctx); err != nil {
		t.Fatalf("register defs: %v", err)
	}

	// A real row that belongs to a DIFFERENT connector key. Inserted
	// directly: connectors.Create only accepts keys present in the service's
	// bootstrap-time module map, which these late-registered defs are not.
	foreign := &entity.Connector{Key: "other_mcp", Label: "Other row", CreatedBy: "u-admin"}
	if err := db.WithContext(ctx).Create(foreign).Error; err != nil {
		t.Fatalf("create foreign row: %v", err)
	}

	req := reqWithUser(http.MethodPost,
		"/manager/api/connectors/petstore_mcp/resync-tools?instance_id="+foreign.ID, admin)
	req.SetPathValue("key", "petstore_mcp")
	rec := httptest.NewRecorder()
	h.apiResyncMCPTools(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an instance owned by another connector; body=%s",
			rec.Code, rec.Body.String())
	}
}
