package agents

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
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
			gotAccess, gotManage := providerPerm(tc.approved, tc.admin, true, tc.access, tc.manag)
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
	_, manage := providerPerm(true, false, true, true, true)
	if !manage {
		t.Error("a manage grant must work without an access grant")
	}
	// And an access grant alone never becomes manage.
	_, manage = providerPerm(true, false, true, true, false)
	if manage {
		t.Error("access alone granted manage — the Providers menu would open for everyone")
	}
}

// An unapproved admin is still unapproved: approval gates everything,
// including the role.
func TestProviderPermissionApprovalOutranksRole(t *testing.T) {
	access, manage := providerPerm(false, true, true, true, true)
	if access || manage {
		t.Errorf("unapproved admin got access=%v manage=%v, want both false", access, manage)
	}
}

// admin_see_all_provider_instances off: the admin role stops walking past
// the tags and is judged like everyone else. On, nothing changes.
func TestProviderPermAdminKnob(t *testing.T) {
	cases := []struct {
		name                           string
		admin, adminAll, access, manag bool
		wantAccess, wantManage         bool
	}{
		{name: "knob on: admin without tags gets both", admin: true, adminAll: true, wantAccess: true, wantManage: true},
		{name: "knob off: admin without tags gets nothing", admin: true, adminAll: false, wantAccess: false, wantManage: false},
		{name: "knob off: admin with access tag may pick", admin: true, adminAll: false, access: true, wantAccess: true, wantManage: false},
		{name: "knob off: admin with manage tag may manage", admin: true, adminAll: false, manag: true, wantAccess: false, wantManage: true},
		{name: "knob off: non-admin unchanged", admin: false, adminAll: false, access: true, wantAccess: true, wantManage: false},
		{name: "knob on: non-admin unchanged", admin: false, adminAll: true, access: false, manag: true, wantAccess: false, wantManage: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, m := providerPerm(true, tc.admin, tc.adminAll, tc.access, tc.manag)
			if a != tc.wantAccess || m != tc.wantManage {
				t.Errorf("access=%v manage=%v, want access=%v manage=%v", a, m, tc.wantAccess, tc.wantManage)
			}
		})
	}
}

// userCanAccessProvider end to end with the knob flipped. The tag store
// grants claude/tagged to holders of the tag only; "untagged" is open to
// everyone (CanAccessTool's rule).
func TestUserCanAccessProviderAdminKnob(t *testing.T) {
	prevTag, prevKnob := providerAccessTagAllows, adminSeeAllProviders
	t.Cleanup(func() { providerAccessTagAllows, adminSeeAllProviders = prevTag, prevKnob })
	holders := map[string]bool{"admin-tagged": true, "user-tagged": true}
	providerAccessTagAllows = func(_ context.Context, u *entity.User, _ provider.Type, name string) bool {
		return name == "untagged" || holders[u.ID]
	}

	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin, Approved: true}
	adminTagged := &entity.User{ID: "admin-tagged", Role: entity.RoleAdmin, Approved: true}
	user := &entity.User{ID: "user", Role: entity.RoleUser, Approved: true}
	userTagged := &entity.User{ID: "user-tagged", Role: entity.RoleUser, Approved: true}

	cases := []struct {
		name     string
		knob     bool
		u        *entity.User
		instance string
		want     bool
	}{
		{"knob on: admin without tag", true, admin, "tagged", true},
		{"knob off: admin without tag is denied", false, admin, "tagged", false},
		{"knob off: admin with tag is allowed", false, adminTagged, "tagged", true},
		{"knob off: admin on untagged instance", false, admin, "untagged", true},
		{"knob on: non-admin without tag denied", true, user, "tagged", false},
		{"knob off: non-admin without tag denied", false, user, "tagged", false},
		{"knob on: non-admin with tag allowed", true, userTagged, "tagged", true},
		{"knob off: non-admin with tag allowed", false, userTagged, "tagged", true},
		{"nil user", true, nil, "untagged", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			knob := tc.knob
			adminSeeAllProviders = func() bool { return knob }
			if got := userCanAccessProvider(context.Background(), tc.u, provider.TypeClaude, tc.instance); got != tc.want {
				t.Errorf("userCanAccessProvider = %v, want %v", got, tc.want)
			}
		})
	}
}

// With no configs wired the knob reads ON: an unconfigured boot must not
// take providers away from admins.
func TestAdminSeeAllProvidersDefaultsOnWithoutConfigs(t *testing.T) {
	prev := globalConfigs
	t.Cleanup(func() { globalConfigs = prev })
	globalConfigs = nil
	if !adminSeeAllProviders() {
		t.Fatal("no configs wired must read as on")
	}
	if userBypassesProviderTags(&entity.User{Role: entity.RoleUser, Approved: true}) {
		t.Fatal("a non-admin never bypasses provider tags")
	}
}

// The request-side gates — manage, both list filters, and the 404 the API
// answers — follow the same knob, so an admin with it off is refused by
// the server and not only hidden in the UI.
func TestProviderGatesFollowAdminKnob(t *testing.T) {
	prevTag, prevManage, prevKnob := providerAccessTagAllows, providerManageTagAllows, adminSeeAllProviders
	t.Cleanup(func() {
		providerAccessTagAllows, providerManageTagAllows, adminSeeAllProviders = prevTag, prevManage, prevKnob
	})
	// "mine" is reachable through the caller's tags, "other" is not.
	providerAccessTagAllows = func(_ context.Context, _ *entity.User, _ provider.Type, name string) bool { return name == "mine" }
	providerManageTagAllows = func(_ context.Context, _ *entity.User, _ provider.Type, name string) bool { return name == "mine" }

	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin, Approved: true}
	names := []string{"mine", "other"}
	key := func(n string) (provider.Type, string) { return provider.TypeClaude, n }

	for _, knob := range []bool{true, false} {
		k := knob
		adminSeeAllProviders = func() bool { return k }
		_, c := postCtx(t, admin, "/", "{}", nil)

		wantOther := knob
		if got := canManageProvider(c, provider.TypeClaude, "other"); got != wantOther {
			t.Errorf("knob=%v: canManageProvider(other) = %v, want %v", knob, got, wantOther)
		}
		if !canManageProvider(c, provider.TypeClaude, "mine") {
			t.Errorf("knob=%v: a manage tag must still grant the admin", knob)
		}
		if got := canAccessProvider(c, provider.TypeClaude, "other"); got != wantOther {
			t.Errorf("knob=%v: canAccessProvider(other) = %v, want %v", knob, got, wantOther)
		}
		wantLen := 1
		if knob {
			wantLen = 2
		}
		if got := visibleProviders(c, names, key); len(got) != wantLen {
			t.Errorf("knob=%v: visibleProviders = %v", knob, got)
		}
		if got := manageableProviders(c, names, key); len(got) != wantLen {
			t.Errorf("knob=%v: manageableProviders = %v", knob, got)
		}
		// The Providers page itself stays open to admins (it also holds
		// admin-only configuration); only its instance list narrows.
		if !HasManageableProvider(c) {
			t.Errorf("knob=%v: admin lost the Providers menu", knob)
		}

		w, c2 := postCtx(t, admin, "/", "{}", nil)
		ok := requireProviderManage(c2, provider.TypeClaude, "other")
		if ok != knob {
			t.Errorf("knob=%v: requireProviderManage(other) = %v", knob, ok)
		}
		if !knob && w.Code != 404 {
			t.Errorf("knob off: refusal code = %d, want 404", w.Code)
		}
	}
}
