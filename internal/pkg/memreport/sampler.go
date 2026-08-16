package memreport

import (
	"context"
	"time"
)

// sampler.go drives the history buffer on an interval.
//
// Sampling is cheap but not free — one /proc walk reads three small files
// per process — so the interval is a knob rather than a constant, and the
// loop exits on context cancellation so a shutdown is not delayed by a
// pending tick.

// Sampler periodically records a snapshot into a History.
type Sampler struct {
	History *History
	// Interval between samples. Non-positive falls back to 15s rather
	// than spinning: a zero-interval loop would peg a core doing nothing
	// but reading /proc.
	Interval time.Duration
	// Names are the process names treated as tree roots.
	Names []string
	// TotalAvail reports machine memory. Injected so the sampler stays
	// testable without the real machine, and so this package does not
	// depend on sysmem (which would be a cycle once sysmem grows).
	TotalAvail func() (total, available uint64)
	// Enabled gates sampling per tick, read live so an operator turning
	// the guard off stops the walk without restarting wick.
	Enabled func() bool
}

// Run samples until ctx is cancelled. Intended to be started in its own
// goroutine at boot.
func (s *Sampler) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.sample(now)
		}
	}
}

// sample records one observation, skipping quietly when disabled or when
// /proc is unavailable (Windows, macOS) — an unsupported platform is a
// documented degradation, not an error worth logging every tick.
func (s *Sampler) sample(now time.Time) {
	if s.History == nil {
		return
	}
	if s.Enabled != nil && !s.Enabled() {
		return
	}
	procs, err := Snapshot()
	if err != nil {
		return
	}
	var total, avail uint64
	if s.TotalAvail != nil {
		total, avail = s.TotalAvail()
	}
	s.History.Record(now, procs, Roots(procs, s.Names), total, avail)
}
