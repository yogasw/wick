//go:build linux || android

package memscope

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/pkg/safeexec"
)

// EnsureSlice makes the agents slice exist with the configured aggregate
// ceiling, reloading systemd only when the unit actually changed.
//
// daemon-reload does not restart running units, so this never disturbs a
// session in flight. Membership is fixed at spawn: sessions started before
// a limit change keep their old placement until they end.
func EnsureSlice(limits SliceLimits) error {
	l := log.With().Str("component", "memscope").Logger()

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "systemd", "user")

	changed, err := ensureSliceAt(dir, limits)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	l.Info().
		Int("aggregate_mb", limits.AggregateMB).
		Int("cpu_weight", limits.CPUWeight).
		Int("cpu_quota_pct", limits.CPUQuotaPct).
		Int("tasks_max", limits.TasksMax).
		Int("io_weight", limits.IOWeight).
		Msg("agents.slice updated")
	if err := safeexec.Command("systemctl", "--user", "daemon-reload").Run(); err != nil {
		l.Warn().Err(err).Msg("daemon-reload failed; slice limits apply on next reload")
	}
	return nil
}

var (
	probeOnce sync.Once
	probeOK   bool
)

// Available reports whether this process can create a transient scope at
// all — systemd-run on PATH plus a reachable user bus. Probed once by
// actually creating a throwaway scope, because that is the only question
// that matters and the only answer that cannot be wrong.
func Available() bool {
	probeOnce.Do(func() {
		err := safeexec.Command("systemd-run",
			"--user", "--scope", "--quiet", "--collect",
			"-p", "MemoryMax=64M", "--", "/bin/true").Run()
		probeOK = err == nil
		if !probeOK {
			l := log.With().Str("component", "memscope").Logger()
			l.Info().Err(err).Msg("scope isolation unavailable; agents run unguarded")
		}
	})
	return probeOK
}
