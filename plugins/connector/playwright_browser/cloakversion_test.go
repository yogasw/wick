package main

import (
	"testing"

	"github.com/yogasw/wick/pkg/connector"
)

// TestCmpDottedVersion pins the version comparator used to gate the newer-binary
// launch flags (no_viewport / --start-maximized) to the same >=148 threshold the
// cloakbrowser wrapper uses.
func TestCmpDottedVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// Pro v150 is newer than the 148 threshold → qualifies.
		{"150.0.7871.114", headlessNoViewportMinVersion, 1},
		// Free v146 is older than 148 → does NOT qualify.
		{"146.0.7680.177.5", headlessNoViewportMinVersion, -1},
		// Exactly the threshold → equal (qualifies via >= 0).
		{headlessNoViewportMinVersion, headlessNoViewportMinVersion, 0},
		// Major dominates.
		{"149.0.0.0", "148.99.99.99", 1},
		{"148.0.0.0", "149.0.0.0", -1},
		// A shorter version pads missing segments with 0, so "148" (== 148.0.0.0)
		// is OLDER than "148.0.7778.215.4" — matches _version_newer's full compare.
		{"148", "148.0.7778.215.4", -1},
		// Bare majors compare on the major alone.
		{"150", "148", 1},
		// Deeper segment breaks a tie.
		{"148.0.7778.215.5", "148.0.7778.215.4", 1},
		{"148.0.7778.215.3", "148.0.7778.215.4", -1},
	}
	for _, c := range cases {
		if got := cmpDottedVersion(c.a, c.b); got != c.want {
			t.Errorf("cmpDottedVersion(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestAtoiSafe covers the non-numeric-tolerant integer parse.
func TestAtoiSafe(t *testing.T) {
	cases := map[string]int{
		"150": 150, "0": 0, "": 0, "  42 ": 42,
		"12abc": 12, "abc": 0, "7680": 7680,
	}
	for in, want := range cases {
		if got := atoiSafe(in); got != want {
			t.Errorf("atoiSafe(%q) = %d, want %d", in, got, want)
		}
	}
}

// ctxWith builds a connector.Ctx with the given config for arg-gating tests.
func ctxWith(cfg map[string]string) *connector.Ctx {
	return connector.NewPluginCtx(nil, cfg, map[string]string{})
}

// TestCloakArgsBase asserts the always-present stealth args mirror
// get_default_stealth_args(): --no-sandbox, a --fingerprint seed in [10000,99999],
// and --fingerprint-platform=windows. --ignore-gpu-blocklist is Windows-only and
// asserted separately (it depends on the host GOOS).
func TestCloakArgsBase(t *testing.T) {
	// A free engine with no resolvable version → conservative (no --start-maximized).
	c := ctxWith(map[string]string{"browser": "cloakbrowser"})
	args := cloakArgs(c)

	has := func(prefix string) bool {
		for _, a := range args {
			if len(a) >= len(prefix) && a[:len(prefix)] == prefix {
				return true
			}
		}
		return false
	}
	if !has("--no-sandbox") {
		t.Error("cloakArgs missing --no-sandbox")
	}
	if !has("--fingerprint=") {
		t.Error("cloakArgs missing --fingerprint=<seed>")
	}
	if !has("--fingerprint-platform=windows") {
		t.Error("cloakArgs missing --fingerprint-platform=windows")
	}
	// An unknown-version free binary must NOT be maximized (matches
	// binary_supports_maximized_window returning false below the gate).
	if has("--start-maximized") {
		t.Error("cloakArgs added --start-maximized for an unknown-version binary; want it gated off")
	}
}
