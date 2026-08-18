//go:build linux || android

package memscope

import (
	"os"
	"path/filepath"

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
