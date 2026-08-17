//go:build linux || android

package memscope

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withCgroupRoot points cgroupV1MemoryRoot at a temp tree that looks like
// a real (if partial) cgroup v1 mount for the duration of the test, and
// restores the real path on cleanup — the same indirection pattern
// daemon.bootedWithSystemdFn uses so the probe never touches the actual
// host filesystem from a test.
func withCgroupRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := cgroupV1MemoryRoot
	cgroupV1MemoryRoot = dir
	t.Cleanup(func() { cgroupV1MemoryRoot = orig })
	return dir
}

// cgroupFSProbe's positive case depends on real kernel behaviour — mkdir
// under an actual cgroup v1 mount auto-populates memory.limit_in_bytes,
// which a plain temp directory on ext4/tmpfs cannot be made to imitate.
// So this runs against the real host root and skips where the mount
// genuinely is not usable, the same pattern
// teardown_integration_test.go uses via Available(): a hermetic fake
// would only prove the fake was wired correctly, not that the probe
// tells a real cgroup mount from a directory that merely looks like one.
func TestCgroupFSProbe_AgainstRealHostMount(t *testing.T) {
	if !cgroupFSProbe() {
		t.Skip("no writable cgroup v1 memory controller on this host — nothing to assert")
	}
	// Reaching here on a host that just reported no controller would be
	// the actual bug; skip already covered that. What's worth pinning is
	// that a second call agrees with the first — the probe is not flaky
	// across repeated invocations against the same real mount.
	if !cgroupFSProbe() {
		t.Fatal("cgroupFSProbe() was true, then false, against the same host mount")
	}
}

// A root that cannot be written to at all (no such directory) must not be
// mistaken for a usable cgroup mount.
func TestCgroupFSProbe_MissingRootIsUnavailable(t *testing.T) {
	orig := cgroupV1MemoryRoot
	cgroupV1MemoryRoot = filepath.Join(t.TempDir(), "does-not-exist")
	t.Cleanup(func() { cgroupV1MemoryRoot = orig })

	if cgroupFSProbe() {
		t.Fatal("cgroupFSProbe() = true against a root that does not exist")
	}
}

// EnsureCgroupSlice must create the slice directory even with a zero
// SliceLimits (measure mode / no aggregate cap) — the directory is what
// makes later scope creation possible, independent of any limit.
func TestEnsureCgroupSlice_CreatesDirectory(t *testing.T) {
	root := withCgroupRoot(t)
	if err := EnsureCgroupSlice(SliceLimits{}); err != nil {
		t.Fatalf("EnsureCgroupSlice: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, SliceName)); err != nil {
		t.Fatalf("slice directory not created: %v", err)
	}
}

// An aggregate ceiling must land in the slice's own memory.limit_in_bytes,
// converted to bytes — the file the kernel actually reads.
func TestEnsureCgroupSlice_WritesAggregateLimitInBytes(t *testing.T) {
	root := withCgroupRoot(t)
	if err := EnsureCgroupSlice(SliceLimits{AggregateMB: 2048}); err != nil {
		t.Fatalf("EnsureCgroupSlice: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, SliceName, "memory.limit_in_bytes"))
	if err != nil {
		t.Fatalf("read memory.limit_in_bytes: %v", err)
	}
	want := "2147483648" // 2048 * 1024 * 1024
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("memory.limit_in_bytes = %q, want %q", got, want)
	}
}

// A zero AggregateMB must not write memory.limit_in_bytes at all — same
// "zero means leave the kernel default" contract RenderSlice already
// holds for the systemd path.
func TestEnsureCgroupSlice_ZeroAggregateWritesNoLimit(t *testing.T) {
	root := withCgroupRoot(t)
	if err := EnsureCgroupSlice(SliceLimits{}); err != nil {
		t.Fatalf("EnsureCgroupSlice: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, SliceName, "memory.limit_in_bytes")); err == nil {
		t.Fatal("memory.limit_in_bytes written despite AggregateMB == 0")
	}
}

// WrapArgvCgroupFS must invoke wick's own binary (not the target directly
// — nothing else will create the cgroup) through the hidden subcommand,
// with the real command preserved intact after a -- separator, mirroring
// TestWrapArgv_PassesThroughBinaryAndArgs for the systemd path.
func TestWrapArgvCgroupFS_ReExecsSelfWithRealCommandAfterSeparator(t *testing.T) {
	bin, argv := WrapArgvCgroupFS("/usr/local/bin/wick", "/usr/bin/claude",
		[]string{"--output-format", "stream-json"},
		Opts{Unit: "claude-agent-7", Slice: SliceName, LimitMB: 512})

	if bin != "/usr/local/bin/wick" {
		t.Fatalf("bin = %q, want the wick binary itself", bin)
	}
	if argv[0] != AgentExecSubcommand {
		t.Fatalf("argv[0] = %q, want %q", argv[0], AgentExecSubcommand)
	}
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"--unit=claude-agent-7", "--limit-mb=512", "--slice=" + SliceName,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv %q missing %q", joined, want)
		}
	}
	sep := -1
	for i, a := range argv {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		t.Fatal("argv has no -- separator before the real command")
	}
	rest := strings.Join(argv[sep+1:], " ")
	if rest != "/usr/bin/claude --output-format stream-json" {
		t.Fatalf("command after -- = %q", rest)
	}
}

// LimitMB == 0 (measure mode) must still pass through as an explicit
// "--limit-mb=0" — RunAgentExec on the receiving end treats that as "join
// the group, apply no ceiling", so the flag must survive, not be omitted.
func TestWrapArgvCgroupFS_ZeroLimitStillPassesTheFlag(t *testing.T) {
	_, argv := WrapArgvCgroupFS("/usr/local/bin/wick", "/usr/bin/claude", nil,
		Opts{Unit: "claude-agent-1", LimitMB: 0})
	if !strings.Contains(strings.Join(argv, " "), "--limit-mb=0") {
		t.Fatalf("argv %v dropped the zero limit flag", argv)
	}
}
