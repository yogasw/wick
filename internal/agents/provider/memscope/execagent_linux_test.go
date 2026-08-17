//go:build linux || android

package memscope

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// withStubExec replaces execFn with a recorder for the test's duration
// and restores syscall.Exec on cleanup. A real exec would replace the
// test binary's own process image and the test would never report a
// result, so every RunAgentExec test must go through this.
func withStubExec(t *testing.T) *struct {
	called bool
	bin    string
	argv   []string
} {
	t.Helper()
	rec := &struct {
		called bool
		bin    string
		argv   []string
	}{}
	orig := execFn
	execFn = func(bin string, argv []string, envv []string) error {
		rec.called, rec.bin, rec.argv = true, bin, argv
		return nil
	}
	t.Cleanup(func() { execFn = orig })
	return rec
}

// The whole point: the calling process's own pid ends up in the scope's
// cgroup.procs before the exec that turns it into the agent, so
// everything the agent forks afterward inherits membership.
func TestRunAgentExec_JoinsCgroupBeforeExec(t *testing.T) {
	root := t.TempDir()
	rec := withStubExec(t)

	err := RunAgentExec(ExecOpts{
		Root: root, Slice: SliceName, Unit: "claude-agent-9",
		LimitMB: 256, Bin: "/usr/bin/claude", Args: []string{"--foo"},
	})
	if err != nil {
		t.Fatalf("RunAgentExec: %v", err)
	}
	if !rec.called {
		t.Fatal("execFn was never called")
	}

	dir := filepath.Join(root, SliceName, "claude-agent-9.scope")
	got, err := os.ReadFile(filepath.Join(dir, "cgroup.procs"))
	if err != nil {
		t.Fatalf("read cgroup.procs: %v", err)
	}
	if strings.TrimSpace(string(got)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("cgroup.procs = %q, want this process's own pid", got)
	}
}

// LimitMB translates to bytes in memory.limit_in_bytes, the file the
// kernel actually enforces against — mirrors
// TestEnsureCgroupSlice_WritesAggregateLimitInBytes for the per-scope
// ceiling instead of the aggregate one.
func TestRunAgentExec_WritesLimitInBytes(t *testing.T) {
	root := t.TempDir()
	withStubExec(t)

	if err := RunAgentExec(ExecOpts{
		Root: root, Slice: SliceName, Unit: "claude-agent-1",
		LimitMB: 512, Bin: "/usr/bin/claude",
	}); err != nil {
		t.Fatalf("RunAgentExec: %v", err)
	}

	dir := filepath.Join(root, SliceName, "claude-agent-1.scope")
	got, err := os.ReadFile(filepath.Join(dir, "memory.limit_in_bytes"))
	if err != nil {
		t.Fatalf("read memory.limit_in_bytes: %v", err)
	}
	want := strconv.Itoa(512 * 1024 * 1024)
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("memory.limit_in_bytes = %q, want %q", got, want)
	}
}

// Measure mode: LimitMB == 0 must still create and join the scope (so
// memory.max_usage_in_bytes is later readable) but must not write a
// ceiling — the same "record, don't constrain" contract the systemd path
// holds in memguard.sliceLimits for MemGuardMeasure.
func TestRunAgentExec_ZeroLimitJoinsWithoutWritingLimit(t *testing.T) {
	root := t.TempDir()
	rec := withStubExec(t)

	if err := RunAgentExec(ExecOpts{
		Root: root, Slice: SliceName, Unit: "claude-agent-2",
		LimitMB: 0, Bin: "/usr/bin/claude",
	}); err != nil {
		t.Fatalf("RunAgentExec: %v", err)
	}
	if !rec.called {
		t.Fatal("execFn was never called")
	}

	dir := filepath.Join(root, SliceName, "claude-agent-2.scope")
	if _, err := os.Stat(filepath.Join(dir, "memory.limit_in_bytes")); err == nil {
		t.Fatal("memory.limit_in_bytes written despite LimitMB == 0")
	}
	// The scope directory itself must still exist — that is what makes
	// memory.max_usage_in_bytes readable later by ReadStatsV1At.
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("scope directory not created for measure mode: %v", err)
	}
}

// The real command and its arguments must reach execFn unchanged, with
// argv[0] matching the binary — the same contract os/exec itself expects
// from a Cmd.Args slice.
func TestRunAgentExec_PassesBinAndArgsToExecFn(t *testing.T) {
	root := t.TempDir()
	rec := withStubExec(t)

	if err := RunAgentExec(ExecOpts{
		Root: root, Slice: SliceName, Unit: "claude-agent-3",
		Bin: "/usr/bin/claude", Args: []string{"--output-format", "stream-json"},
	}); err != nil {
		t.Fatalf("RunAgentExec: %v", err)
	}
	if rec.bin != "/usr/bin/claude" {
		t.Fatalf("execFn bin = %q, want /usr/bin/claude", rec.bin)
	}
	want := []string{"/usr/bin/claude", "--output-format", "stream-json"}
	if len(rec.argv) != len(want) {
		t.Fatalf("execFn argv = %v, want %v", rec.argv, want)
	}
	for i := range want {
		if rec.argv[i] != want[i] {
			t.Fatalf("execFn argv = %v, want %v", rec.argv, want)
		}
	}
}

// A root that cannot become a directory (ENOTDIR — a stand-in for "the
// cgroup mount is not there") must fail loudly rather than exec
// unconfined. Deliberately not a permission-bits test: this suite runs as
// root in CI/sandboxes as often as not, and root bypasses those bits
// entirely, which would make a chmod-based failure case flaky-false-pass
// depending on who runs it. A path component that is a plain file fails
// MkdirAll (ENOTDIR) regardless of caller privilege.
func TestRunAgentExec_UnusableRootFailsWithoutExecing(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(blocker, "agents-slice-cannot-nest-under-a-file")
	rec := withStubExec(t)

	err := RunAgentExec(ExecOpts{
		Root: root, Slice: SliceName, Unit: "claude-agent-4", Bin: "/usr/bin/claude",
	})
	if err == nil {
		t.Fatal("RunAgentExec succeeded against a root that cannot be a directory")
	}
	if rec.called {
		t.Fatal("execFn was called despite a setup failure")
	}
}
