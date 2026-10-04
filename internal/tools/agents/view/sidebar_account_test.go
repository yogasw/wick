package view

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/ui"
)

func renderLayout(t *testing.T, u *entity.User, viewingAs string) string {
	t.Helper()
	ctx := login.WithUser(context.Background(), u, nil)
	if viewingAs != "" {
		ctx = ui.WithImpersonation(ctx, ui.ImpersonationInfo{Active: true, ActingAs: viewingAs})
	}
	var sb strings.Builder
	if err := AgentsLayout(AgentsLayoutVM{Base: "/tools/agents", ActivePage: "overview"}).Render(ctx, &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

// accountPanel cuts out the account menu at the foot of the sidebar.
func accountPanel(t *testing.T, html string) string {
	t.Helper()
	i := strings.Index(html, "data-sidebar-account")
	if i < 0 {
		t.Fatal("the sidebar has no account row")
	}
	return html[i:]
}

// The top of the sidebar is the Team | Agents switch: the brand bar and its
// avatar are gone, and so is the old "Team" nav row the switch replaces.
func TestSidebarTopIsTheSpaceSwitch(t *testing.T) {
	got := renderLayout(t, namedAdmin(), "")
	if !strings.Contains(got, "data-space-switch") || !strings.Contains(got, `href="/tools/agents/team"`) {
		t.Fatal("no Team | Agents switch linking to the Team app")
	}
	if strings.Contains(got, ">NEW<") {
		t.Error("the old Team nav row (NEW chip) is still in the sidebar")
	}
	top := got[strings.Index(got, "data-agents-top-widgets"):]
	top = top[:strings.Index(top, "</nav>")]
	if strings.Contains(top, "data-menu") && strings.Contains(top, "Sign out") {
		t.Error("the account dropdown is still in the top bar")
	}
	if !strings.Contains(got, "data-sidebar-close") || !strings.Contains(got, "data-sidebar-open") {
		t.Error("mobile drawer controls went missing with the top bar")
	}
}

// Mini Tools left the sidebar for the account menu, which carries the same
// items as wick's own account menu minus "Agents".
func TestSidebarAccountMenuItems(t *testing.T) {
	got := renderLayout(t, namedAdmin(), "")
	menu := accountPanel(t, got)
	for _, want := range []string{`href="/profile"`, `href="/profile/tokens"`, `href="/profile/connections"`, `href="/profile/mcp"`, `href="/mini-tools"`, `href="/admin"`, "/auth/logout"} {
		if !strings.Contains(menu, want) {
			t.Errorf("account menu is missing %s", want)
		}
	}
	if strings.Contains(menu, `href="/tools/agents"`) {
		t.Error("account menu still offers Agents; the switch covers it")
	}
	before := got[:strings.Index(got, "data-sidebar-account")]
	if strings.Contains(before, `href="/mini-tools"`) {
		t.Error("Mini Tools is still a sidebar row")
	}
	if !strings.Contains(menu, "/tools/agents/settings") {
		t.Error("Settings left the sidebar foot")
	}
}

func TestSidebarAccountMenuAdminGating(t *testing.T) {
	u := &entity.User{ID: "u1", Name: "Member", Email: "m@example.com", Role: entity.RoleUser, Approved: true}
	menu := accountPanel(t, renderLayout(t, u, ""))
	if strings.Contains(menu, `href="/admin"`) || strings.Contains(menu, "/tools/agents/settings") {
		t.Error("a non-admin sees Admin panel or Settings")
	}
	if !strings.Contains(menu, `href="/mini-tools"`) {
		t.Error("Mini Tools is for everyone")
	}
}

func TestSidebarAccountMenuViewingAs(t *testing.T) {
	menu := accountPanel(t, renderLayout(t, namedAdmin(), "Member"))
	if !strings.Contains(menu, "Viewing as Member") || !strings.Contains(menu, `action="/admin/impersonate/stop"`) {
		t.Fatal("the account menu lost the Viewing as banner and its way back")
	}
	if !strings.Contains(menu, "bg-cau-400") {
		t.Error("no caution dot on the account row while viewing as someone else")
	}
}

// namedAdmin has a name: the account row shows its initial.
func namedAdmin() *entity.User {
	return &entity.User{ID: "a", Name: "Admin", Email: "a@example.com", Approved: true, Role: entity.RoleAdmin}
}
