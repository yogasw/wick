package mcp

import (
	"context"
	"reflect"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// catalogStubModule is an OAuth connector with two ops, so one can be
// switched off while the row stays listed.
func catalogStubModule(requireAIDesc bool) connector.Module {
	op := func(key string) connector.Operation {
		return connector.Op(key, key, "Returns ok", struct{}{},
			func(c *connector.Ctx) (any, error) { return "ok", nil }, wickdocs.Docs{})
	}
	return connector.Module{
		Meta: connector.Meta{
			Key: "cat-stub", Name: "Catalog Stub", Description: "catalog test connector",
			Fixed: true, RequireAIDescription: requireAIDesc,
		},
		Operations: []connector.Category{connector.Cat("", "", op("echo"), op("ping"))},
		OAuth:      &connector.OAuthMeta{DisplayName: "Catalog Stub"},
	}
}

// catalogIDs renders a VisibleCatalog result as the ids wick_list emits.
func catalogIDs(cat []connectors.CatalogEntry) []string {
	out := []string{}
	for _, e := range cat {
		out = append(out, e.Row.ID)
		for _, a := range e.Accounts {
			out = append(out, e.Row.ID+"/"+a.ID)
		}
	}
	return out
}

func listIDs(p listResult) []string {
	out := []string{}
	for _, c := range p.Connectors {
		out = append(out, c.ID)
	}
	return out
}

// VisibleCatalog and wick_list are one rule: same rows, same accounts, and
// someone else's SSO account stays out of both.
func TestVisibleCatalogMatchesWickList(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, catalogStubModule(false))
	ctx := context.Background()
	rows, err := svc.ListByKey(ctx, "cat-stub")
	if err != nil || len(rows) == 0 {
		t.Fatalf("bootstrap row missing: %v", err)
	}
	row := rows[0]
	if err := svc.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	for _, u := range []string{"alice", "bob"} {
		if err := svc.SaveAccount(ctx, row.ID, "u-"+u, "ext-"+u, u, "tok-"+u); err != nil {
			t.Fatalf("save %s: %v", u, err)
		}
	}
	h := NewHandler(svc)
	bob := &entity.User{ID: "u-bob", Role: entity.RoleUser}

	cat, err := svc.VisibleCatalog(ctx, bob.ID, nil, false)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(cat) != 1 || len(cat[0].Accounts) != 1 || cat[0].Accounts[0].DisplayName != "bob" {
		t.Fatalf("bob's catalog should hold the row and his own account only, got %+v", cat)
	}
	if got, want := listIDs(dispatchListAs(t, h, bob)), catalogIDs(cat); !reflect.DeepEqual(got, want) {
		t.Fatalf("wick_list ids %v != catalog ids %v", got, want)
	}
	if n := len(cat[0].Ops); n != 2 {
		t.Fatalf("ops = %d, want 2", n)
	}

	// An op switched off leaves the catalog and wick_list's count alike.
	if err := svc.SetOperationEnabled(ctx, row.ID, "ping", false); err != nil {
		t.Fatalf("disable op: %v", err)
	}
	cat, _ = svc.VisibleCatalog(ctx, bob.ID, nil, false)
	if len(cat) != 1 || len(cat[0].Ops) != 1 || cat[0].Ops[0].Key != "echo" {
		t.Fatalf("disabled op still in catalog: %+v", cat)
	}
	if got := dispatchListAs(t, h, bob).TotalTools; got != 1 { // counted once per row, not per account entry
		t.Fatalf("wick_list total_tools = %d, want 1", got)
	}
}

// A row that still needs setup is not in the catalog.
func TestVisibleCatalogSkipsNeedsSetup(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, catalogStubModule(true))
	cat, err := svc.VisibleCatalog(context.Background(), "u-bob", nil, false)
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if len(cat) != 0 {
		t.Fatalf("needs_setup row listed: %+v", cat)
	}
}
