package view

import (
	"context"
	"strings"
	"testing"
	"time"

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
	if err := ResourcesAdminPage("Projects", "/admin/projects", rows, nil, owner, ResourceFilter{}, admin).Render(context.Background(), &sb); err != nil {
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
	if err := ResourcesAdminPage("Skills", "/admin/skills", rows, nil, ResourceOwnerEdit{}, ResourceFilter{}, admin).Render(context.Background(), &sb); err != nil {
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

// Deleted projects share the Projects table: the filter switch, a Deleted
// badge, who/when, days left and a Restore form — and none of the live
// row's owner or tag controls.
func TestDeletedProjectRowRenders(t *testing.T) {
	admin := &entity.User{ID: "u-admin", Name: "Admin", Role: entity.RoleAdmin, Approved: true}
	rows := []ResourceAdminRow{{
		ID: "acme", Name: "Acme", CreatedBy: "user-a", OwnerLabel: "User A",
		Deleted: &DeletedInfo{Key: "acme__1", By: "User B", At: time.Now(), DaysLeft: 12, Chats: 3},
	}}
	owner := ResourceOwnerEdit{Enabled: true, Users: []UserOption{{ID: "user-a", Label: "User A"}}}
	filter := ResourceFilter{Enabled: true, Show: ShowDeleted, Active: 4, Deleted: 1}
	var sb strings.Builder
	if err := ResourcesAdminPage("Projects", "/admin/projects", rows, nil, owner, filter, admin).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{
		`href="/admin/projects?show=active"`, `href="/admin/projects?show=deleted"`, `href="/admin/projects?show=all"`,
		`>Deleted</span>`, `Deleted by User B`, `12 days left`, `3 chats`,
		`action="/admin/projects-trash/acme__1/restore"`, `>Restore</button>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("deleted row is missing %q", want)
		}
	}
	for _, unwanted := range []string{`/admin/projects/acme/owner`, `/admin/projects/acme/tags`, `tag-picker`} {
		if strings.Contains(out, unwanted) {
			t.Errorf("deleted row renders a live control %q", unwanted)
		}
	}

	// Surfaces without a trash render no filter at all.
	sb.Reset()
	if err := ResourcesAdminPage("Skills", "/admin/skills", nil, nil, ResourceOwnerEdit{}, ResourceFilter{}, admin).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sb.String(), "show=deleted") {
		t.Error("a surface without a trash shows the Deleted filter")
	}
}
