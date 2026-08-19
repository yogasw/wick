//go:build linux || android

package provider

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider/memscope"
)

// memguard_cgroupfs_linux_test.go holds the cgroupfs-backend cases that
// assert what Wrap actually BUILDS, as opposed to which branch it takes.
//
// Build-tagged because memscope.WrapArgvCgroupFS only produces a wrapper
// on Linux — cgroupfs_other.go returns the command unchanged, since there
// is no cgroup filesystem to drive off Linux. Left in the untagged file,
// these asserted Linux behaviour on every platform and failed on Windows
// while passing on Linux, which reads as a broken build rather than as an
// unsupported platform.
//
// Branch-selection cases (which backend Wrap dispatches to, degradation
// when the self path cannot be resolved) stay untagged in memguard_test.go:
// that logic is platform-agnostic and should be exercised everywhere.

// The cgroupfs backend is the systemd-less fallback (a Fly.io Machine —
// or any container — with no systemd user session, but a real cgroup v1
// mount). Wrap must re-exec wick's own binary through the hidden
// __agent-exec subcommand rather than reach for systemd-run, which is
// exactly what would fail there.
func TestMemGuard_CgroupFSBackendReExecsSelf(t *testing.T) {
	withBackend(t, memscope.BackendCgroupFS)
	withSelfExecutable(t, "/usr/local/bin/wick", nil)
	g := &MemGuard{Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnSpawn: true}, AgentLimitMB: 512}

	bin, argv, unit := g.Wrap("/usr/bin/claude", []string{"--foo"}, "claude", 4)
	if bin != "/usr/local/bin/wick" {
		t.Fatalf("bin = %q, want wick's own binary under the cgroupfs backend", bin)
	}
	if unit == "" {
		t.Fatal("cgroupfs backend created no scope")
	}
	if len(argv) == 0 || argv[0] != memscope.AgentExecSubcommand {
		t.Fatalf("argv[0] = %v, want %q", argv, memscope.AgentExecSubcommand)
	}
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--limit-mb=512") {
		t.Fatalf("argv %v does not carry the limit", argv)
	}
	if !strings.Contains(joined, "/usr/bin/claude --foo") {
		t.Fatalf("argv %v lost the real command", argv)
	}
}
