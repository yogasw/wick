package agents

import (
	"context"
	"fmt"
	"net/http"
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

// The pick-side gates follow the knob, so an admin with it off is refused
// by the server and not only hidden in the UI. Manage does not: admins
// look after every instance on the Providers page either way.
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
		// Manage ignores the knob: admins look after every instance.
		if !canManageProvider(c, provider.TypeClaude, "other") {
			t.Errorf("knob=%v: admin must manage every instance", knob)
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
		if got := manageableProviders(c, names, key); len(got) != 2 {
			t.Errorf("knob=%v: manageableProviders = %v", knob, got)
		}
		// The Providers page itself stays open to admins (it also holds
		// admin-only configuration); only its instance list narrows.
		if !HasManageableProvider(c) {
			t.Errorf("knob=%v: admin lost the Providers menu", knob)
		}

		w, c2 := postCtx(t, admin, "/", "{}", nil)
		if !requireProviderManage(c2, provider.TypeClaude, "other") {
			t.Errorf("knob=%v: requireProviderManage(other) refused an admin (code %d)", knob, w.Code)
		}
	}
}

// stubProviderInstances stands in for the provider config file: find
// answers from the map, save writes back into it.
func stubProviderInstances(t *testing.T, store map[string]provider.Instance) {
	t.Helper()
	prevFind, prevSave := findProviderInstanceFn, saveProviderInstanceFn
	t.Cleanup(func() { findProviderInstanceFn, saveProviderInstanceFn = prevFind, prevSave })
	findProviderInstanceFn = func(typ provider.Type, name string) (provider.Instance, error) {
		ins, ok := store[string(typ)+"/"+name]
		if !ok {
			return provider.Instance{}, fmt.Errorf("no instance %s/%s", typ, name)
		}
		return ins, nil
	}
	saveProviderInstanceFn = func(ins provider.Instance) error {
		store[string(ins.Type)+"/"+ins.Name] = ins
		return nil
	}
}

// Unset permissions: the general ones start on, the ones that decide what
// runs on the host or whose login is used start off. Every key has a
// default, and an instance's stored value wins over it.
func TestOwnerPermDefaults(t *testing.T) {
	on := []string{ownerPermConfigure, ownerPermModels, ownerPermEnv, ownerPermExtraArgs,
		ownerPermRename, ownerPermDelete, ownerPermAIRouter, ownerPermStorage, ownerPermRescan, ownerPermSessions}
	off := []string{ownerPermBinary, ownerPermExtraMCP, ownerPermExternalSkills, ownerPermSandbox,
		ownerPermAIRouterRawConf, ownerPermAuthFrom}
	eff := effectiveOwnerPerms(nil)
	if len(eff) != len(on)+len(off) || len(ownerPermKeys) != len(eff) {
		t.Fatalf("effective perms = %d keys, ownerPermKeys = %d, want %d", len(eff), len(ownerPermKeys), len(on)+len(off))
	}
	for _, k := range on {
		if !eff[k] {
			t.Errorf("%s must default on", k)
		}
	}
	for _, k := range off {
		if eff[k] {
			t.Errorf("%s must default off", k)
		}
	}
	for _, k := range ownerPermKeys {
		if !isOwnerPerm(k) {
			t.Errorf("%s listed but not known", k)
		}
	}
	stored := effectiveOwnerPerms(map[string]bool{ownerPermBinary: true, ownerPermDelete: false})
	if !stored[ownerPermBinary] || stored[ownerPermDelete] || !stored[ownerPermRename] {
		t.Errorf("stored values must win, the rest keep defaults: %v", stored)
	}
}

// An instance's owner may do exactly what that instance allows; an admin
// does everything whatever is ticked; anyone else nothing.
func TestProviderOwnerPermissions(t *testing.T) {
	prevOwned := providerOwnedBy
	t.Cleanup(func() { providerOwnedBy = prevOwned })
	providerOwnedBy = func(_ context.Context, u *entity.User, _ provider.Type, name string) bool {
		return u.ID == "owner" && (name == "mine" || name == "plain")
	}
	stubProviderInstances(t, map[string]provider.Instance{
		"claude/mine": {Type: provider.TypeClaude, Name: "mine", OwnerPerms: map[string]bool{
			ownerPermDelete: false, ownerPermBinary: true,
		}},
		"claude/plain":  {Type: provider.TypeClaude, Name: "plain"},
		"claude/theirs": {Type: provider.TypeClaude, Name: "theirs"},
	})

	owner := &entity.User{ID: "owner", Role: entity.RoleUser, Approved: true}
	other := &entity.User{ID: "other", Role: entity.RoleUser, Approved: true}
	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin, Approved: true}

	cases := []struct {
		u    *entity.User
		inst string
		perm string
		want bool
	}{
		{owner, "mine", ownerPermConfigure, true}, // default on
		{owner, "mine", ownerPermDelete, false},   // ticked off on this instance
		{owner, "mine", ownerPermBinary, true},    // ticked on on this instance
		{owner, "mine", ownerPermAuthFrom, false}, // default off
		{owner, "plain", ownerPermDelete, true},   // same owner, other instance: default
		{owner, "plain", ownerPermBinary, false},
		{owner, "theirs", ownerPermConfigure, false},
		{owner, "gone", ownerPermConfigure, false},
		{other, "mine", ownerPermConfigure, false},
		{admin, "theirs", ownerPermBinary, true},
		{admin, "mine", ownerPermDelete, true},
	}
	for _, tc := range cases {
		_, c := postCtx(t, tc.u, "/", "{}", nil)
		if got := canProviderDo(c, provider.TypeClaude, tc.inst, tc.perm); got != tc.want {
			t.Errorf("%s on %s / %s = %v, want %v", tc.u.ID, tc.inst, tc.perm, got, tc.want)
		}
	}
	// The owner looks after their own instance even with nothing ticked.
	_, c := postCtx(t, owner, "/", "{}", nil)
	if !canManageProvider(c, provider.TypeClaude, "mine") {
		t.Error("owner must manage their own instance")
	}
}

// Every config key maps to the permission that guards it; the host keys
// each have their own.
func TestProviderKeyPerm(t *testing.T) {
	for key, want := range map[string]string{
		"binary": ownerPermBinary, "extra_args": ownerPermExtraArgs, "env": ownerPermEnv,
		"extra_mcp_servers": ownerPermExtraMCP, "load_external_skills": ownerPermExternalSkills,
		"sandbox_mode": ownerPermSandbox,
		"auth_from":    ownerPermAuthFrom, "opencode_data_dir": ownerPermAuthFrom, "omp_profile": ownerPermAuthFrom,
		"models": ownerPermModels, "model_select": ownerPermModels, "live_models": ownerPermModels,
		"live_model_filter": ownerPermModels, "live_model_default": ownerPermModels, "opencode_model": ownerPermModels,
		"max_concurrent": ownerPermConfigure, "send_mode": ownerPermConfigure,
	} {
		if got := providerKeyPerm(key); got != want {
			t.Errorf("providerKeyPerm(%q) = %q, want %q", key, got, want)
		}
	}
}

// The owner-perms endpoint: admins store known permissions on the
// instance, everyone else is refused, an unknown name refuses the whole
// request.
func TestSaveProviderOwnerPerms(t *testing.T) {
	store := map[string]provider.Instance{
		"claude/mine": {Type: provider.TypeClaude, Name: "mine", OwnerPerms: map[string]bool{ownerPermRename: false}},
	}
	stubProviderInstances(t, store)
	path := map[string]string{"type": "claude", "name": "mine"}
	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin, Approved: true}
	owner := &entity.User{ID: "owner", Role: entity.RoleUser, Approved: true}

	w, c := postCtx(t, owner, "/", `{"binary":true}`, path)
	saveProviderOwnerPerms(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin: status=%d body=%s", w.Code, w.Body.String())
	}
	if store["claude/mine"].OwnerPerms[ownerPermBinary] {
		t.Fatal("non-admin request must not be stored")
	}

	w, c = postCtx(t, admin, "/", `{"binary":true,"nope":true}`, path)
	saveProviderOwnerPerms(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown key: status=%d body=%s", w.Code, w.Body.String())
	}
	if _, ok := store["claude/mine"].OwnerPerms[ownerPermBinary]; ok {
		t.Fatal("a refused request must not store any key")
	}

	w, c = postCtx(t, admin, "/", `{"binary":true,"delete":false}`, path)
	saveProviderOwnerPerms(c)
	if w.Code != http.StatusOK {
		t.Fatalf("admin: status=%d body=%s", w.Code, w.Body.String())
	}
	got := store["claude/mine"].OwnerPerms
	if !got[ownerPermBinary] || got[ownerPermDelete] {
		t.Errorf("stored = %v, want binary on, delete off", got)
	}
	if v, ok := got[ownerPermRename]; !ok || v {
		t.Errorf("a key the body leaves out must keep its stored value: %v", got)
	}

	w, c = postCtx(t, admin, "/", `{"binary":true}`, map[string]string{"type": "claude", "name": "gone"})
	saveProviderOwnerPerms(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing instance: status=%d", w.Code)
	}
}

// Creating an instance: admins always; a non-admin only while they carry the
// tag named in provider_create_tag; nobody else, and nobody when it is empty.
func TestCanCreateProvider(t *testing.T) {
	prevName, prevCarries := providerCreateTagName, userCarriesTagNamed
	t.Cleanup(func() { providerCreateTagName, userCarriesTagNamed = prevName, prevCarries })
	userCarriesTagNamed = func(_ context.Context, u *entity.User, name string) bool {
		return u.ID == "creator" && name == "provider_creators"
	}
	admin := &entity.User{ID: "admin", Role: entity.RoleAdmin, Approved: true}
	creator := &entity.User{ID: "creator", Role: entity.RoleUser, Approved: true}
	other := &entity.User{ID: "other", Role: entity.RoleUser, Approved: true}

	for _, tagName := range []string{"", "provider_creators"} {
		n := tagName
		providerCreateTagName = func() string { return n }
		for _, tc := range []struct {
			u    *entity.User
			want bool
		}{
			{admin, true},
			{creator, n != ""},
			{other, false},
		} {
			_, c := postCtx(t, tc.u, "/", "{}", nil)
			if got := canCreateProvider(c); got != tc.want {
				t.Errorf("tag=%q %s: canCreateProvider = %v, want %v", n, tc.u.ID, got, tc.want)
			}
		}
	}
}
