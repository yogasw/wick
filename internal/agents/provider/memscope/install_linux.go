//go:build linux || android

package memscope

import (
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/rs/zerolog"
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
		// Once per process even when the unit is unchanged: a runtime
		// drop-in can predate this wick (RuntimeMemoryArgs).
		if runtimeSynced.CompareAndSwap(false, true) {
			syncRuntimeMemory(l, limits)
		}
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
	runtimeSynced.Store(true)
	syncRuntimeMemory(l, limits)
	return nil
}

// runtimeSynced: this process already put the runtime memory controls
// back in line with the unit.
var runtimeSynced atomic.Bool

// syncRuntimeMemory overrides any runtime drop-in on the slice's memory
// keys with the unit's own values (RuntimeMemoryArgs). Best effort.
func syncRuntimeMemory(l zerolog.Logger, limits SliceLimits) {
	args := RuntimeMemoryArgs(limits)
	if err := safeexec.Command("systemctl", args...).Run(); err != nil {
		l.Warn().Err(err).Strs("args", args).Msg("could not align the slice's runtime memory controls")
		return
	}
	l.Info().Strs("args", args).Msg("agents.slice runtime memory controls aligned with the unit")
}
