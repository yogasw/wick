package config

// guardscope.go resolves WHERE the memory limit is applied, from either
// the two switches or the single choice they replaced.
//
// The old setting named the mechanism ("auto", "wrapper") and forced a
// choice between them. That framing was wrong twice over: "auto" chose
// nothing an operator could observe — it picked a kernel mechanism, which
// happens under either setting — and the two are not alternatives at all.
// The kernel applies every ceiling in the hierarchy and the tightest
// wins, so both can run at once, and on a real host that combination is
// strictly better than either alone:
//
//	on-spawn only  agents wick starts, and nothing else
//	on-path only   everything invoked by name — until a package update
//	               replaces the link, after which nothing is covered and
//	               nothing says so
//	both           the path shim covers what wick cannot reach, and wick
//	               still covers its own agents when the shim is gone
//
// A read of the old value must not change behaviour for anyone, so
// migration is a pure function of it rather than a write at boot: an
// operator who never opens the settings page keeps exactly what they had.

// GuardScopes is where limits are applied.
type GuardScopes struct {
	// OnSpawn wraps the agents wick starts, at the moment it starts them.
	OnSpawn bool
	// OnPath wraps anything invoked by name, via a shim installed in
	// front of the binary. Reaching further costs a file on disk and one
	// privileged command, and it still misses a caller that uses an
	// absolute path.
	OnPath bool
}

// Any reports whether any isolation is asked for. False means the guard
// has a mode set but nowhere to apply it, which is the same as off.
func (s GuardScopes) Any() bool { return s.OnSpawn || s.OnPath }

// ResolveGuardScopes reads the effective scopes, falling back to the
// legacy method when neither switch is set.
//
// The zero case is why legacy is keyed on "neither set" rather than on
// the method being non-empty: a config written before the switches
// existed has both false, and reading that as "no isolation anywhere"
// would silently disable a guard the operator turned on.
//
// Turning both switches off deliberately IS expressible — it just means
// the same thing as setting the mode to off, and the settings page says
// so rather than pretending otherwise.
func ResolveGuardScopes(onSpawn, onPath bool, legacyMethod string) GuardScopes {
	if onSpawn || onPath {
		return GuardScopes{OnSpawn: onSpawn, OnPath: onPath}
	}
	return scopesFromMethod(legacyMethod)
}

// scopesFromMethod maps a pre-2026-08 method value onto the switches.
//
//	wrapper       something outside wick wraps it, so wick only measures
//	auto / scope  wick wraps its own spawns
//	""            an unset method defaulted to auto
func scopesFromMethod(method string) GuardScopes {
	if method == MethodWrapper {
		return GuardScopes{OnPath: true}
	}
	return GuardScopes{OnSpawn: true}
}
