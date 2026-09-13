package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// ssoStubModule is stubModule with OAuth declared, so its rows can carry
// connected accounts and wick_list emits kind="account" entries.
func ssoStubModule() connector.Module {
	return connector.Module{
		Meta: connector.Meta{Key: "sso-stub", Name: "SSO Stub", Description: "In-process SSO test connector", Fixed: true},
		Operations: []connector.Category{
			connector.Cat("", "",
				connector.Op("echo", "Echo", "Returns ok",
					struct{}{},
					func(c *connector.Ctx) (any, error) { return "ok", nil },
					wickdocs.Docs{},
				),
			),
		},
		OAuth: &connector.OAuthMeta{DisplayName: "SSO Stub"},
	}
}

// dispatchListAs runs wick_list as the given user (not the local admin the
// other tests use) and returns the decoded payload.
func dispatchListAs(t *testing.T, h *Handler, user *entity.User) listResult {
	t.Helper()
	msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q,"params":{"name":"wick_list","arguments":{}}}`, "tools/call")
	raw := h.dispatchLine(login.WithUser(context.Background(), user, nil), []byte(msg))
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, raw)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatalf("no content in wick_list response:\n%s", raw)
	}
	var payload listResult
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v\ntext=%s", err, resp.Result.Content[0].Text)
	}
	return payload
}

// accountNames lists the kind="account" entries of a wick_list payload.
func accountNames(p listResult) []string {
	out := []string{}
	for _, c := range p.Connectors {
		if c.Kind == "account" {
			out = append(out, c.Connector)
		}
	}
	return out
}

// Default policy: two people connected their own account to the same
// instance, and each of them lists the connector plus their OWN account only.
func TestWickListHidesOtherUsersAccounts(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, ssoStubModule())
	rows, err := svc.ListByKey(context.Background(), "sso-stub")
	if err != nil || len(rows) == 0 {
		t.Fatalf("bootstrap row missing: %v", err)
	}
	row := rows[0]
	ctx := context.Background()
	if err := svc.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	if err := svc.SaveAccount(ctx, row.ID, "u-alice", "ext-a", "alice", "tok-a"); err != nil {
		t.Fatalf("save alice: %v", err)
	}
	if err := svc.SaveAccount(ctx, row.ID, "u-bob", "ext-b", "bob", "tok-b"); err != nil {
		t.Fatalf("save bob: %v", err)
	}
	h := NewHandler(svc)

	bob := dispatchListAs(t, h, &entity.User{ID: "u-bob", Role: entity.RoleUser})
	if got := accountNames(bob); len(got) != 1 || got[0] != "SSO Stub – @bob" {
		t.Fatalf("bob should list his own account only, got %v", got)
	}
	if bob.TotalConnectors != 2 {
		t.Fatalf("total_connectors = %d, want 2 (connector + own account)", bob.TotalConnectors)
	}

	admin := dispatchListAs(t, h, &entity.User{ID: "u-admin", Role: entity.RoleAdmin})
	if got := accountNames(admin); len(got) != 2 {
		t.Fatalf("an admin should list both accounts, got %v", got)
	}

	// Shared pool: everyone sees every account again.
	if err := svc.SetAccessPolicy(ctx, row.ID, connectors.AccessPolicy{EnableSSO: true, MultiAccount: true, AllowOthersSeeAccounts: true}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	shared := dispatchListAs(t, h, &entity.User{ID: "u-bob", Role: entity.RoleUser})
	if got := accountNames(shared); len(got) != 2 {
		t.Fatalf("shared pool should list both accounts, got %v", got)
	}
}
