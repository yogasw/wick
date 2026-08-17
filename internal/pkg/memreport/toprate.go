package memreport

import (
	"sort"
	"time"
)

// toprate.go turns machine-wide process snapshots into RATES.
//
// Ranking by raw CPUTicks answers the wrong question: those counters are
// cumulative since process start, so the top of that list is whatever has
// been alive longest, not whatever is busy now. A browser open since
// yesterday outranks the compiler pegging a core this second.
//
// This keeps one previous snapshot and diffs against it, the same trick
// History uses per agent — but machine-wide and without retaining a
// series, since the top-process table only ever shows "right now".

// ProcRate is one process with its instantaneous rates.
type ProcRate struct {
	Proc
	CPUPct     float64
	IOReadBps  uint64
	IOWriteBps uint64
}

// RateTracker diffs consecutive machine-wide snapshots.
//
// Not safe for concurrent use; the API handler owns one and calls it from
// a single request path.
type RateTracker struct {
	prev   map[int]Proc
	prevAt time.Time
}

// NewRateTracker returns an empty tracker. Its first Update reports zero
// rates — a rate needs two observations, and inventing one from a
// process's lifetime total would spike wildly for anything long-lived.
func NewRateTracker() *RateTracker {
	return &RateTracker{prev: map[int]Proc{}}
}

// Update records a snapshot and returns each process with rates derived
// against the previous one.
func (t *RateTracker) Update(now time.Time, procs []Proc) []ProcRate {
	elapsed := now.Sub(t.prevAt).Seconds()
	first := t.prevAt.IsZero()

	out := make([]ProcRate, 0, len(procs))
	next := make(map[int]Proc, len(procs))

	for _, p := range procs {
		r := ProcRate{Proc: p}
		if !first && elapsed > 0 {
			if old, ok := t.prev[p.PID]; ok {
				r.CPUPct = CPUPercent(saturatingSub(p.CPUTicks, old.CPUTicks), elapsed)
				r.IOReadBps = uint64(float64(saturatingSub(p.IOReadBytes, old.IOReadBytes)) / elapsed)
				r.IOWriteBps = uint64(float64(saturatingSub(p.IOWriteBytes, old.IOWriteBytes)) / elapsed)
			}
		}
		out = append(out, r)
		next[p.PID] = p
	}

	// Replace rather than merge: processes that exited must not linger as
	// stale entries for the life of the server.
	t.prev = next
	t.prevAt = now
	return out
}

// TopRateBy ranks rated processes and returns the first limit, dropping
// the zero-valued tail so a quiet machine shows a short list rather than
// a table padded with idle system processes.
func TopRateBy(procs []ProcRate, key func(ProcRate) float64, limit int) []ProcRate {
	out := make([]ProcRate, len(procs))
	copy(out, procs)

	sort.Slice(out, func(i, j int) bool {
		a, b := key(out[i]), key(out[j])
		if a != b {
			return a > b
		}
		return out[i].PID < out[j].PID
	})

	end := len(out)
	for end > 0 && key(out[end-1]) == 0 {
		end--
	}
	out = out[:end]

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Rate keys for TopRateBy.
func RateByCPU(p ProcRate) float64 { return p.CPUPct }
func RateByMem(p ProcRate) float64 { return float64(p.RSSBytes) }
func RateByIO(p ProcRate) float64  { return float64(p.IOReadBps + p.IOWriteBps) }
