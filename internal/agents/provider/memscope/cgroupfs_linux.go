//go:build linux || android

package memscope

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/rs/zerolog/log"
)

// cgroupfs_linux.go is the fallback mechanism for a Linux box with no
// systemd — observed in production on a Fly.io Machine, whose own init
// (not systemd) is PID 1. systemd-run has nothing to talk to there, but
// the kernel still mounts cgroup v1 directly at /sys/fs/cgroup/memory,
// writable with no daemon, no bus, no unit file. This package drives that
// hierarchy by hand: mkdir a scope, write its limit, move a process in,
// exec. No systemd-run stands in for the "run inside a fresh scope" step,
// so wick itself performs it — see execagent.go's RunAgentExec, invoked
// through a hidden `wick __agent-exec` re-exec (WrapArgvCgroupFS below).
//
// What this backend cannot give you, and does not pretend to: cgroup v1
// has no per-group equivalent of v2's memory.events oom_kill counter. A
// kill still happens — the kernel enforces memory.limit_in_bytes exactly
// as it enforces a v2 memory.max — but ReadCgroupFSStatsAt cannot name it
// with the same confidence WrapArgv's systemd path can. See its doc
// comment. Enforcement is real; the nice "used 1.5GB, over its 512MB
// limit" message is not always available. That is a weaker report, never
// a weaker limit.

// cgroupV1MemoryRoot is the cgroup v1 memory-controller mount point.
// Indirected through a var (not a const) so tests point it at a temp
// directory instead of the real host hierarchy.
var cgroupV1MemoryRoot = "/sys/fs/cgroup/memory"

// cgroupFSProbe reports whether this process can create and populate a
// cgroup v1 memory group. Like the systemd probe it replaces as a
// fallback for, it actually creates and removes a throwaway group rather
// than guessing from permissions — a mount can be present but read-only,
// or present but missing the memory controller.
func cgroupFSProbe() bool {
	dir, err := os.MkdirTemp(cgroupV1MemoryRoot, "wick-probe-*")
	if err != nil {
		return false
	}
	defer os.Remove(dir)
	// A real cgroup directory always has these controller files; a plain
	// tmpfs directory (mount present, controller not attached) will not.
	if _, err := os.Stat(filepath.Join(dir, "memory.limit_in_bytes")); err != nil {
		return false
	}
	return os.WriteFile(filepath.Join(dir, "cgroup.procs"), []byte(""), 0o644) == nil
}

// EnsureCgroupSlice makes the agents slice directory exist and, when an
// aggregate ceiling is configured, applies it hierarchically so children
// share the one cap.
//
// memory.use_hierarchy must be set while the slice has no live
// descendants, or the kernel refuses the write (EBUSY) — true the first
// time this runs, and harmlessly re-attempted (and ignored) on every
// later call for the same reason ensureSliceAt only rewrites systemd's
// unit file on a real change.
func EnsureCgroupSlice(limits SliceLimits) error {
	dir := filepath.Join(cgroupV1MemoryRoot, SliceName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("memscope: create %s: %w", dir, err)
	}
	l := log.With().Str("component", "memscope").Str("backend", "cgroupfs").Logger()

	// Best-effort: an already-populated slice rejects this, and that is
	// fine — hierarchy was already on from the first call.
	_ = os.WriteFile(filepath.Join(dir, "memory.use_hierarchy"), []byte("1"), 0o644)

	if limits.AggregateMB <= 0 {
		return nil
	}
	limitBytes := strconv.Itoa(limits.AggregateMB * 1024 * 1024)
	if err := os.WriteFile(filepath.Join(dir, "memory.limit_in_bytes"), []byte(limitBytes), 0o644); err != nil {
		l.Warn().Err(err).Int("aggregate_mb", limits.AggregateMB).
			Msg("could not write aggregate limit onto agents slice; per-scope limits still apply")
		return nil
	}
	return nil
}

// WrapArgvCgroupFS returns the binary and argv that place cmd in a new
// cgroup v1 group before running it. Unlike systemd-run, nothing on this
// machine performs that placement for us, so wick re-execs itself as the
// wrapper: `<wick binary> __agent-exec <flags> -- <real bin> <real args>`.
// The hidden subcommand's implementation is RunAgentExec in execagent.go.
//
// selfPath is the absolute path to the running wick binary — passed in
// rather than resolved here so this stays a pure function like its
// systemd-run sibling, testable without touching os.Executable.
func WrapArgvCgroupFS(selfPath, bin string, args []string, o Opts) (string, []string) {
	slice := o.Slice
	if slice == "" {
		slice = SliceName
	}
	argv := []string{
		AgentExecSubcommand,
		"--root=" + cgroupV1MemoryRoot,
		"--slice=" + slice,
		"--unit=" + o.Unit,
		"--limit-mb=" + strconv.Itoa(o.LimitMB),
		"--",
		bin,
	}
	argv = append(argv, args...)
	return selfPath, argv
}
