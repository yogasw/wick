//go:build linux || android

package memscope

import (
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/pkg/safeexec"
)

// backend_linux.go answers "how do we isolate a spawn on this machine" as
// a ranked choice, not a single yes/no.
//
// Found in production on a Fly.io Machine: systemd-run is the mechanism
// Available() has always probed, but its absence does not mean cgroups
// are unusable — the kernel still exposes /sys/fs/cgroup/memory (cgroup
// v1) directly, writable with no systemd in the picture at all. A
// container with no init manager still has a real Linux kernel under it.
// Available() alone could only ever say "systemd or nothing"; Backend
// says which of two real mechanisms is in play, so Wrap can build the
// right argv for either.

// Backend identifies which isolation mechanism DetectBackend found
// working on this machine.
type Backend int

const (
	// BackendNone: neither systemd-run nor raw cgroupfs is usable.
	// Agents run unguarded; only /proc-based measurement remains.
	BackendNone Backend = iota
	// BackendSystemd: systemd-run --user --scope works. Preferred —
	// transient scopes are reaped automatically and memory.events gives
	// a real per-scope OOM-kill counter (cgroup v2).
	BackendSystemd
	// BackendCgroupFS: no systemd, but /sys/fs/cgroup/memory (cgroup v1)
	// accepts a manually-created child cgroup. Weaker than systemd in one
	// respect — see cgroupfs_linux.go's doc comment on OOM evidence — but
	// still a real kernel-enforced ceiling, which "unguarded" is not.
	BackendCgroupFS
)

func (b Backend) String() string {
	switch b {
	case BackendSystemd:
		return "systemd"
	case BackendCgroupFS:
		return "cgroupfs"
	default:
		return "none"
	}
}

var (
	backendOnce sync.Once
	backendVal  Backend
)

// DetectBackend probes once (each probe actually creates and tears down a
// throwaway group — the only check that cannot be wrong) and caches the
// result for the life of the process, same contract as the old
// Available().
func DetectBackend() Backend {
	backendOnce.Do(func() {
		l := log.With().Str("component", "memscope").Logger()
		if systemdScopeProbe() {
			backendVal = BackendSystemd
			return
		}
		if cgroupFSProbe() {
			l.Info().Msg("systemd scope unavailable; falling back to raw cgroupfs (cgroup v1) isolation")
			backendVal = BackendCgroupFS
			return
		}
		l.Info().Msg("scope isolation unavailable; agents run unguarded")
	})
	return backendVal
}

// Available reports whether ANY isolation mechanism works. Kept for
// callers (the Resources page banner, existing tests) that only ever
// needed a yes/no; DetectBackend is the seam that also says which one.
func Available() bool { return DetectBackend() != BackendNone }

// systemdScopeProbe is the original Available() check, unchanged:
// systemd-run on PATH plus a reachable user bus.
func systemdScopeProbe() bool {
	err := safeexec.Command("systemd-run",
		"--user", "--scope", "--quiet", "--collect",
		"-p", "MemoryMax=64M", "--", "/bin/true").Run()
	return err == nil
}
