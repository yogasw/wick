//go:build linux || android

package wrapper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/safeexec"
)

// shim_exec_linux_test.go runs the generated shim as a real script.
//
// The other tests assert what the text contains, which cannot catch a
// shim that is syntactically wrong, quotes a path badly, or loses its
// arguments — all of which produce a file that reads correctly and fails
// the moment an agent is spawned through it. /bin/sh is the only judge
// of that, so here it judges.
//
// Both cases exercised below are fallbacks, not the isolated path:
// creating a real cgroup needs a systemd user session that CI does not
// have. The fallbacks are worth testing on their own merits — they are
// what runs when isolation is unavailable, and getting them wrong means
// a spawn that fails instead of a spawn that is merely unguarded.

// writeShim renders a shim pointed at a stub "real binary" that echoes
// its arguments, and returns the paths to both.
func writeShim(t *testing.T, limitMB int) (shim string) {
	t.Helper()
	// The user's home, not t.TempDir(): /tmp is mounted noexec on some
	// systems, and a shim that cannot be executed tests nothing.
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("#!/bin/sh\necho \"REAL $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	shim = filepath.Join(dir, "shim")
	body := RenderShim(Provider{Name: "t", RealBin: real, LimitMB: limitMB}, "agents.slice")
	if err := os.WriteFile(shim, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return shim
}

// run executes the shim through /bin/sh, so noexec mounts and a missing
// exec bit cannot turn a real failure into a skip.
func run(t *testing.T, shim string, env []string, args ...string) (string, error) {
	t.Helper()
	c := safeexec.Command("/bin/sh", append([]string{shim}, args...)...)
	c.Env = env
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Without a user session there is nothing to create a scope in. The shim
// must still run the agent — an unguarded agent beats no agent at all,
// the same trade memguard.go makes when a slice cannot be ensured.
func TestShim_RunsTheAgentWithoutAUserSession(t *testing.T) {
	shim := writeShim(t, 64)

	out, err := run(t, shim, []string{"PATH=/usr/bin:/bin"}, "hello", "world")
	if err != nil {
		t.Fatalf("shim failed with no XDG_RUNTIME_DIR: %v (%s)", err, out)
	}
	if out != "REAL hello world" {
		t.Fatalf("shim output = %q, want the real binary to run with its arguments", out)
	}
}

// AGENT_NO_CGROUP bypasses the shim for one command, so an operator can
// rule isolation out while debugging without uninstalling anything.
func TestShim_EscapeHatchBypassesIsolation(t *testing.T) {
	shim := writeShim(t, 64)

	out, err := run(t, shim,
		[]string{"PATH=/usr/bin:/bin", "XDG_RUNTIME_DIR=/run/user/1000", "AGENT_NO_CGROUP=1"},
		"bypassed")
	if err != nil {
		t.Fatalf("escape hatch failed: %v (%s)", err, out)
	}
	if out != "REAL bypassed" {
		t.Fatalf("output = %q, want the real binary to run directly", out)
	}
}

// Arguments must survive whatever the shim does to them: an agent spawn
// carries flags with spaces and quotes, and "$@" is the only form that
// preserves them.
func TestShim_PreservesArgumentBoundaries(t *testing.T) {
	shim := writeShim(t, 0)

	out, err := run(t, shim, []string{"PATH=/usr/bin:/bin"}, "--prompt", "two words")
	if err != nil {
		t.Fatalf("shim failed: %v (%s)", err, out)
	}
	if out != "REAL --prompt two words" {
		t.Fatalf("output = %q, want arguments passed through intact", out)
	}
}

// A shim with a syntax error still looks fine as text and fails only
// when something is spawned through it.
func TestShim_IsValidShellSyntax(t *testing.T) {
	for _, limit := range []int{0, 1200} {
		shim := writeShim(t, limit)
		c := safeexec.Command("/bin/sh", "-n", shim)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("limit %d: shim is not valid sh: %v\n%s", limit, err, out)
		}
	}
}
