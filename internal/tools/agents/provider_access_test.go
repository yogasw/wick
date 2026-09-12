package agents

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// The two paths must stay distinct, and the manage path must be derived
// from the access one. A typo that made them equal would silently grant
// reconnect to everyone an instance is merely visible to.
func TestProviderTagPaths(t *testing.T) {
	access := providerAccessPath(provider.TypeClaude, "engineer")
	manage := providerManagePath(provider.TypeClaude, "engineer")

	if access != "/providers/claude/engineer" {
		t.Errorf("access path = %q", access)
	}
	if manage != "/providers/claude/engineer/manage" {
		t.Errorf("manage path = %q", manage)
	}
	if access == manage {
		t.Fatal("access and manage share one path — one grant would be both")
	}
	// The admin panel (internal/admin/providers.go) builds these same
	// two strings by hand. If this test changes, that one must too.
}

// The whole rule, as a table. The two defaults are opposite on purpose:
// an untagged instance is VISIBLE to everyone but MANAGEABLE by no one
// except admins.
func TestProviderPermissionTable(t *testing.T) {
	cases := []struct {
		name                           string
		approved, admin, access, manag bool
		wantAccess, wantManage         bool
	}{
		{name: "not approved", wantAccess: false, wantManage: false},
		{name: "not approved even with tags", access: true, manag: true, wantAccess: false, wantManage: false},
		{name: "admin sees and manages everything", approved: true, admin: true, wantAccess: true, wantManage: true},
		{name: "untagged instance is visible to any approved user", approved: true, access: true, wantAccess: true, wantManage: false},
		{name: "tagged instance without the tag is invisible", approved: true, access: false, wantAccess: false, wantManage: false},
		{name: "manage tag grants reconnect", approved: true, access: true, manag: true, wantAccess: true, wantManage: true},
		// The case that matters most: a manage grant on something the
		// user cannot see must not become a back door into seeing it.
		{name: "manage tag without access is nothing", approved: true, access: false, manag: true, wantAccess: false, wantManage: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotAccess, gotManage := providerPerm(tc.approved, tc.admin, tc.access, tc.manag)
			if gotAccess != tc.wantAccess || gotManage != tc.wantManage {
				t.Errorf("access=%v manage=%v, want access=%v manage=%v",
					gotAccess, gotManage, tc.wantAccess, tc.wantManage)
			}
		})
	}
}

// An unapproved admin is still unapproved: approval gates everything,
// including the role.
func TestProviderPermissionApprovalOutranksRole(t *testing.T) {
	access, manage := providerPerm(false, true, true, true)
	if access || manage {
		t.Errorf("unapproved admin got access=%v manage=%v, want both false", access, manage)
	}
}
