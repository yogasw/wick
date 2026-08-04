package provider

import "strings"

// RunTarget is the pair that decides what actually runs: which provider
// instance, and which model on it.
//
// The two travel together on purpose. A model id is only meaningful
// against the instance it was chosen on — wick's ids are
// "<entryID>@<vendorModelID>", scoped to one instance's model registry —
// so taking the provider from one place and the model from another
// produces a pin that resolves to the wrong model or to nothing at all.
// Every layer that can express "run on X" therefore expresses the pair,
// and resolution picks a whole RunTarget rather than filling two fields
// independently.
type RunTarget struct {
	// Provider is an instance key: "type" or "type/name".
	Provider string
	// ModelID is the pin within that instance. Empty means "let the
	// instance choose", which is a valid answer, not a missing one.
	ModelID string
}

// Empty reports whether this target names no provider at all.
//
// Deliberately keyed on Provider alone: a model without an instance to
// run it on is not a usable target, while an instance without a model is
// (the instance resolves its own default).
func (t RunTarget) Empty() bool { return strings.TrimSpace(t.Provider) == "" }

// ResolveRunTarget returns the first candidate that names a provider.
//
// Callers pass candidates in precedence order — most specific first. The
// winner is taken WHOLE: its ModelID travels with its Provider, and a
// blank ModelID on the winning candidate is respected rather than
// back-filled from a weaker one. Back-filling is the bug this function
// exists to prevent: it is how a session pinned to one instance's model
// ends up running that pin against a different instance.
//
// Note on what callers should NOT pass: the global DefaultProvider
// setting is a NEW-SESSION default (channels, API, quick-create), not a
// fallback for delegated work. A sub-agent that inherits it silently
// jumps to whatever instance an operator set globally instead of running
// where its conversation runs — the exact surprise this resolver exists
// to remove. Sub-agent chains end at the parent session / project.
//
// Returns the zero RunTarget when no candidate names a provider; callers
// treat that as "fall through to the built-in default".
func ResolveRunTarget(candidates ...RunTarget) RunTarget {
	for _, c := range candidates {
		if c.Empty() {
			continue
		}
		return RunTarget{
			Provider: strings.TrimSpace(c.Provider),
			ModelID:  strings.TrimSpace(c.ModelID),
		}
	}
	return RunTarget{}
}

// SplitInstanceKey splits an instance key into type and name. A bare
// type ("claude") names its canonical instance ("claude", "claude").
// Mirrors the parse in pool.spawn so both agree on what a stored
// Provider string means.
func SplitInstanceKey(key string) (typ, name string) {
	key = strings.TrimSpace(key)
	if i := strings.IndexByte(key, '/'); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, key
}

// SameInstance reports whether two instance keys name the same provider
// instance, normalizing a bare type to its canonical form so "claude"
// and "claude/claude" compare equal.
//
// Used to decide whether an inherited model pin is still meaningful: a
// pin only survives when the instance it was chosen on is the instance
// about to run.
func SameInstance(a, b string) bool {
	at, an := SplitInstanceKey(a)
	bt, bn := SplitInstanceKey(b)
	return at == bt && an == bn
}
