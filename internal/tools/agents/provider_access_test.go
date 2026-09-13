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

// The whole rule, as a table. The two grants are independent jobs:
// ACCESS says who may pick this provider for a project or a session
// (untagged = everyone), MANAGE says who looks after its account in the
// Providers menu (untagged = admins only).
func TestProviderPermissionTable(t *testing.T) {
	cases := []struct {
		name                           string
		approved, admin, access, manag bool
		wantAccess, wantManage         bool
	}{
		{name: "not approved", wantAccess: false, wantManage: false},
		{name: "not approved even with tags", access: true, manag: true, wantAccess: false, wantManage: false},
		{name: "admin picks and manages everything", approved: true, admin: true, wantAccess: true, wantManage: true},
		{name: "untagged provider is pickable by any approved user", approved: true, access: true, wantAccess: true, wantManage: false},
		{name: "access-tagged without the tag is not pickable", approved: true, access: false, wantAccess: false, wantManage: false},
		{name: "manage tag grants the Providers menu", approved: true, access: true, manag: true, wantAccess: true, wantManage: true},
		// The grants are independent: someone who looks after an account
		// need not be allowed to run projects on it, and vice versa.
		{name: "manage without access still manages", approved: true, access: false, manag: true, wantAccess: false, wantManage: true},
		{name: "access without manage never opens the menu", approved: true, access: true, manag: false, wantAccess: true, wantManage: false},
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

// Manage is evaluated on its own, so the caller passes access=true for
// it — mirroring canManageProvider, which no longer consults the access
// path at all. This test pins that independence.
func TestProviderManageDoesNotDependOnAccess(t *testing.T) {
	// access=false, manage=true in the STORED tags; canManageProvider
	// passes access=true because manage stands alone.
	_, manage := providerPerm(true, false, true, true)
	if !manage {
		t.Error("a manage grant must work without an access grant")
	}
	// And an access grant alone never becomes manage.
	_, manage = providerPerm(true, false, true, false)
	if manage {
		t.Error("access alone granted manage — the Providers menu would open for everyone")
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
