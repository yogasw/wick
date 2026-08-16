package pool

import (
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/pkg/sysmem"
)

// admission.go refuses a spawn while the machine is already short of
// memory, so the queue absorbs the pressure instead of the OOM killer.
//
// This is the counterpart to capacity.go: that one counts slots, this one
// counts bytes. A slot being free says nothing about whether the machine
// can host what would fill it.
//
// It reads /proc/meminfo and needs no cgroup or systemd, which makes it
// one of the two layers that still protect Termux/Android.

// memoryAdmits decides whether there is room to start another agent.
//
// Unknown availability admits: this guard is advisory, and turning "I
// cannot read the machine" into "nothing may ever start" would be a worse
// failure than the one being prevented.
func memoryAdmits(minFreeMB int, availBytes uint64, availKnown bool) bool {
	if minFreeMB <= 0 || !availKnown {
		return true
	}
	return availBytes >= uint64(minFreeMB)*1024*1024
}

// memoryAdmitsNow answers for the live machine, logging a refusal so a
// queued spawn is never a silent mystery.
func (p *Pool) memoryAdmitsNow() bool {
	if p.cfg.MinFreeMemoryLoader == nil {
		return true
	}
	minFreeMB := p.cfg.MinFreeMemoryLoader()
	if minFreeMB <= 0 {
		return true
	}
	avail, ok := sysmem.Available()
	if memoryAdmits(minFreeMB, avail, ok) {
		return true
	}
	l := log.With().Str("component", "pool").Logger()
	l.Info().
		Uint64("available_bytes", avail).
		Int("min_free_mb", minFreeMB).
		Msg("spawn queued: machine is low on memory")
	return false
}
