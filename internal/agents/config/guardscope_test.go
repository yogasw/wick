package config

import "testing"

// An existing install must keep behaving exactly as it did. Anyone whose
// config predates the switches has both false, and reading that as "no
// isolation anywhere" would silently disable a guard they turned on.
func TestResolveGuardScopes_LegacyMethodStillDecides(t *testing.T) {
	cases := []struct {
		method string
		want   GuardScopes
	}{
		// wrapper meant something outside wick does the wrapping.
		{MethodWrapper, GuardScopes{OnPath: true}},
		// auto meant wick wraps its own spawns.
		{MethodAuto, GuardScopes{OnSpawn: true}},
		// scope never differed from auto — see the comment on MethodScope.
		{MethodScope, GuardScopes{OnSpawn: true}},
		// An unset method defaulted to auto.
		{"", GuardScopes{OnSpawn: true}},
		// An unknown value is safer read as "wick wraps" than as
		// "nothing wraps": the first over-applies a limit, the second
		// removes one the operator believes is there.
		{"something-else", GuardScopes{OnSpawn: true}},
	}
	for _, c := range cases {
		t.Run(c.method, func(t *testing.T) {
			if got := ResolveGuardScopes(false, false, c.method); got != c.want {
				t.Fatalf("method %q resolved to %+v, want %+v", c.method, got, c.want)
			}
		})
	}
}

// Once either switch is set, it is the answer — the legacy value must
// not override a choice the operator has since made.
func TestResolveGuardScopes_SwitchesWinOverLegacy(t *testing.T) {
	// Someone on the old "wrapper" setting who then turns on-spawn on.
	got := ResolveGuardScopes(true, false, MethodWrapper)

	if want := (GuardScopes{OnSpawn: true}); got != want {
		t.Fatalf("got %+v, want %+v — the legacy method overrode an explicit switch", got, want)
	}
}

// Both at once is the point of the change, not an error state. The
// kernel applies every ceiling and the tightest wins, so the path shim
// covers what wick cannot reach while wick still covers its own agents
// when the shim's link is gone.
func TestResolveGuardScopes_BothTogether(t *testing.T) {
	got := ResolveGuardScopes(true, true, "")

	if !got.OnSpawn || !got.OnPath {
		t.Fatalf("got %+v, want both scopes active", got)
	}
	if !got.Any() {
		t.Fatal("Any() is false with both scopes on")
	}
}

// Turning both off is expressible on purpose: it means the same as
// setting the mode to off. It must not fall back to the legacy value,
// which would make the switches impossible to turn off.
func TestGuardScopes_NoneIsExpressible(t *testing.T) {
	// Reached through the struct rather than the resolver, since the
	// resolver reads all-false as "not yet migrated".
	if (GuardScopes{}).Any() {
		t.Fatal("empty scopes reported as active")
	}
}
