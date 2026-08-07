package custom

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// Rename is display-only: the key is baked into the registry, the instance
// rows, and the access tag, so it must survive a rename untouched.
func TestRenameKeepsKey(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	svc := New(Deps{DB: db})

	def := &entity.CustomConnector{Key: "n8n_new", Name: "n8n-new", Source: "manual"}
	if err := svc.store.CreateDef(ctx, def); err != nil {
		t.Fatalf("CreateDef: %v", err)
	}

	if err := svc.Rename(ctx, def.ID, "n8n prod"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	got, err := svc.store.GetDef(ctx, def.ID)
	if err != nil {
		t.Fatalf("GetDef: %v", err)
	}
	if got.Name != "n8n prod" {
		t.Errorf("Name = %q, want %q", got.Name, "n8n prod")
	}
	if got.Key != "n8n_new" {
		t.Errorf("key changed on rename: %q — instances and tags would orphan", got.Key)
	}
}

// A blank name would erase the connector's label everywhere it renders,
// so it is rejected rather than stored.
func TestRenameRejectsBlank(t *testing.T) {
	ctx := context.Background()
	svc := New(Deps{DB: newTestDB(t)})

	def := &entity.CustomConnector{Key: "petstore", Name: "Petstore", Source: "manual"}
	if err := svc.store.CreateDef(ctx, def); err != nil {
		t.Fatalf("CreateDef: %v", err)
	}

	for _, name := range []string{"", "   "} {
		if err := svc.Rename(ctx, def.ID, name); err == nil {
			t.Errorf("Rename(%q) = nil, want error", name)
		}
	}
	got, _ := svc.store.GetDef(ctx, def.ID)
	if got.Name != "Petstore" {
		t.Errorf("Name mutated by a rejected rename: %q", got.Name)
	}
}

// For MCP defs the server row's Label is the same string shown in the edit
// form, and SaveServer syncs label → def.Name. If a rename left the server
// row stale, the next save would silently resurrect the old name.
func TestRenameSyncsMCPServerLabel(t *testing.T) {
	ctx := context.Background()
	svc := New(Deps{DB: newTestDB(t)})

	srv := &entity.CustomConnectorMCPServer{
		Label:       "n8n-new",
		Transport:   "http",
		URL:         "https://mcp.example.com/rpc",
		AuthScheme:  "oauth",
		AuthHeaders: "[]",
		AuthExtra:   "{}",
		Headers:     "[]",
	}
	if err := svc.store.CreateServer(ctx, srv); err != nil {
		t.Fatalf("CreateServer: %v", err)
	}
	def := &entity.CustomConnector{
		Key:        "n8n_new",
		Name:       "n8n-new",
		Source:     entity.CustomConnectorSourceMCP,
		SourceMeta: `{"server_id":"` + srv.ID + `"}`,
	}
	if err := svc.store.CreateDef(ctx, def); err != nil {
		t.Fatalf("CreateDef: %v", err)
	}

	if err := svc.Rename(ctx, def.ID, "n8n staging"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	gotSrv, err := svc.store.GetServer(ctx, srv.ID)
	if err != nil {
		t.Fatalf("GetServer: %v", err)
	}
	if gotSrv.Label != "n8n staging" {
		t.Errorf("server Label = %q, want it to follow the rename", gotSrv.Label)
	}
}
