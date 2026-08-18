//go:build linux || android

package memscope

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// execagent_linux.go is the far end of WrapArgvCgroupFS: the hidden
// `wick __agent-exec` re-exec that systemd-run's own binary would
// otherwise perform. app/agentexec_cmd.go parses flags and calls
// RunAgentExec; the setup (mkdir, write limit, join the group) is kept
// separate from the final syscall.Exec so it is unit-testable — a test
// cannot exec over itself and survive to report a result.

// ExecOpts describes one placement: create (or reuse) a cgroup, put this
// process in it, then become bin.
type ExecOpts struct {
	Root    string // cgroup v1 memory root, e.g. /sys/fs/cgroup/memory
	Slice   string // defaults to SliceName when empty
	Unit    string // scope directory name, without ".scope"
	LimitMB int    // 0 = no ceiling (measure mode: group exists, unconstrained)
	Bin     string
	Args    []string
}

// execFn performs the final replace-this-process step. A package var so
// tests can capture the call instead of actually exec'ing — the same
// indirection pattern DetectSource's sd_booted() check uses in
// internal/pkg/daemon, for the same reason: the real syscall cannot be
// exercised from inside `go test` without ending the test binary.
var execFn = syscall.Exec

// RunAgentExec places the calling process in a fresh cgroup v1 group —
// creating it if needed, applying LimitMB when set — then execs Bin,
// replacing this process image so the agent becomes what wick's own
// process-group teardown (kill(-pgid)) still reaches directly. No
// intermediate process is left between wick and the agent, matching what
// the systemd-run path guarantees via --scope (see
// teardown_integration_test.go's rationale).
func RunAgentExec(o ExecOpts) error {
	slice := o.Slice
	if slice == "" {
		slice = SliceName
	}
	dir := filepath.Join(o.Root, slice, o.Unit+".scope")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("memscope: create scope dir %s: %w", dir, err)
	}

	if o.LimitMB > 0 {
		limitBytes := strconv.Itoa(o.LimitMB * 1024 * 1024)
		if err := os.WriteFile(filepath.Join(dir, "memory.limit_in_bytes"), []byte(limitBytes), 0o644); err != nil {
			return fmt.Errorf("memscope: set limit on %s: %w", dir, err)
		}
	}

	// Joining before exec, not after: the write takes effect on this PID
	// immediately, and every process this one execs or forks inherits
	// cgroup membership. There is a short window between this write and
	// the exec below where our own (small, known) process image counts
	// against the limit — negligible next to what an agent allocates.
	pid := strconv.Itoa(os.Getpid())
	if err := os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(pid), 0o644); err != nil {
		return fmt.Errorf("memscope: join cgroup %s: %w", dir, err)
	}

	argv := append([]string{o.Bin}, o.Args...)
	if err := execFn(o.Bin, argv, os.Environ()); err != nil {
		return fmt.Errorf("memscope: exec %s: %w", o.Bin, err)
	}
	return nil // unreachable on success — execFn replaces this process
}
