package view

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// The Owner column is a form, and a form that posts to the wrong place — or
// whose select does not contain its own current value — silently reassigns a
// row to somebody else. Both pages are rendered here for that reason, rather
// than trusted to the handler tests, which never see the markup.
func TestOwnerPickersRenderTheirForms(t *testing.T) {
	admin := &entity.User{ID: "u-admin", Name: "Admin", Role: entity.RoleAdmin, Approved: true}
	users := []UserOption{{ID: "u-new", Label: "New"}}

	var sb strings.Builder
	rows := []ResourceAdminRow{{ID: "p-1", Name: "Proj", CreatedBy: "u-old", OwnerLabel: "Old"}}
	owner := ResourceOwnerEdit{Enabled: true, Users: users}
	if err := ResourcesAdminPage("Projects", "/admin/projects", rows, nil, owner, admin).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render projects: %v", err)
	}
	out := sb.String()
	for _, want := range []string{
		`action="/admin/projects/p-1/owner"`,
		`name="owner_user_id"`,
		// The current owner is no longer an approved user. Dropping them from
		// the list would leave the select showing — and on submit, applying —
		// somebody else entirely.
		`Old — unknown user`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("projects page is missing %q", want)
		}
	}

	// A surface that cannot re-stamp an owner renders the same column read-only.
	sb.Reset()
	if err := ResourcesAdminPage("Skills", "/admin/skills", rows, nil, ResourceOwnerEdit{}, admin).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render skills: %v", err)
	}
	if strings.Contains(sb.String(), `name="owner_user_id"`) {
		t.Error("skills page must not offer an owner picker")
	}

	sb.Reset()
	crows := []ConnectorAdminRow{{
		Connector:  entity.Connector{ID: "c-1", Label: "Row", Key: "k", CreatedBy: "u-old"},
		OwnerLabel: "Old",
	}}
	if err := ConnectorsAdminPage(crows, nil, users, admin).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render connectors: %v", err)
	}
	out = sb.String()
	if !strings.Contains(out, `action="/admin/connectors/c-1/owner"`) {
		t.Error("connectors page is missing the owner form")
	}
	// Turning an instance off belongs to its settings page, which is the only
	// screen that shows what else breaks when you do.
	if strings.Contains(out, "/disabled") {
		t.Error("connectors page still offers a disable toggle")
	}
}

// A disabled instance has no toggle on this page any more, so the state itself
// has to stay readable — otherwise a row missing from MCP looks identical to a
// healthy one.
func TestDisabledConnectorStillReadsAsDisabled(t *testing.T) {
	admin := &entity.User{ID: "u-admin", Name: "Admin", Role: entity.RoleAdmin, Approved: true}
	crows := []ConnectorAdminRow{{
		Connector: entity.Connector{ID: "c-1", Label: "Row", Key: "k", Disabled: true},
	}}
	var sb strings.Builder
	if err := ConnectorsAdminPage(crows, nil, nil, admin).Render(context.Background(), &sb); err != nil {
		t.Fatalf("render connectors: %v", err)
	}
	if !strings.Contains(sb.String(), ">Disabled<") {
		t.Error("a disabled instance must still be marked as one")
	}
}
