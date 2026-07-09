package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// Spec describes when a schedule fires, parsed from user/agent input. Exactly
// one of Once / IntervalMs / Cron is set. FirstRunAt is the concrete first
// fire time the store's run_at is seeded with.
type Spec struct {
	Recurring  bool
	IntervalMs int64
	Cron       string
	FirstRunAt time.Time
}

// ParseWhen turns scheduling input into a Spec. Rules:
//   - runAt set (RFC3339 or +offset) and no every/cron → one-shot at runAt.
//   - every set ("5m", "1h30m", "90s") → recurring by interval; first fire is
//     now+interval unless runAt gives an explicit first fire.
//   - cron set (5-field) → recurring by cron; first fire is the next match.
//
// `now` is passed so callers stay testable. Returns an error on empty/invalid
// input or when more than one timing mode is supplied.
func ParseWhen(runAt, every, cron string, now time.Time) (Spec, error) {
	runAt = strings.TrimSpace(runAt)
	every = strings.TrimSpace(every)
	cron = strings.TrimSpace(cron)

	modes := 0
	if every != "" {
		modes++
	}
	if cron != "" {
		modes++
	}
	if modes > 1 {
		return Spec{}, fmt.Errorf("set only one of every / cron")
	}

	switch {
	case every != "":
		d, err := parseEvery(every)
		if err != nil {
			return Spec{}, err
		}
		first := now.Add(d)
		if runAt != "" {
			t, err := parseAt(runAt, now)
			if err != nil {
				return Spec{}, err
			}
			first = t
		}
		return Spec{Recurring: true, IntervalMs: d.Milliseconds(), FirstRunAt: first}, nil

	case cron != "":
		if !validCron(cron) {
			return Spec{}, fmt.Errorf("invalid cron %q (want 5 fields: min hour dom mon dow)", cron)
		}
		next, err := nextCronAfter(cron, now)
		if err != nil {
			return Spec{}, err
		}
		return Spec{Recurring: true, Cron: cron, FirstRunAt: next}, nil

	default:
		t, err := parseAt(runAt, now)
		if err != nil {
			return Spec{}, err
		}
		return Spec{Recurring: false, FirstRunAt: t}, nil
	}
}

// NextFrom computes the next fire time for a recurring schedule from `from`,
// ignoring stop conditions — used by resume to re-seed run_at. Returns zero
// for a non-recurring / malformed schedule.
func NextFrom(m entity.ScheduledMessage, from time.Time) (time.Time, error) {
	switch {
	case m.IntervalMs > 0:
		return from.Add(time.Duration(m.IntervalMs) * time.Millisecond), nil
	case strings.TrimSpace(m.Cron) != "":
		return nextCronAfter(m.Cron, from)
	default:
		return time.Time{}, fmt.Errorf("schedule is not recurring")
	}
}

// parseAt parses a one-shot fire time. Accepts, in order:
//   - "+<dur>" explicit offset from now  (+90m, +2h, +1d)
//   - a bare duration                    (90m, 2h, 1d)  → treated as +dur
//   - an absolute RFC3339 timestamp      (2026-07-09T12:40:00Z)
//
// The bare-duration case is the forgiving path: a user typing "1m" in the
// one-shot field almost always means "1 minute from now", not a literal time.
// Rejects a past absolute time.
func parseAt(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("run_at is required (RFC3339 like 2026-07-09T12:40:00Z, or relative like 2h / 90m / 1d)")
	}
	// Explicit or bare relative offset. Try duration first; if it parses,
	// it's a "from now" offset. Only fall through to absolute parsing when
	// it clearly isn't a duration (e.g. starts with a digit-year + "T").
	offset := strings.TrimPrefix(raw, "+")
	if d, err := parseEvery(offset); err == nil {
		return now.Add(d), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("run_at must be a relative duration (2h / 90m / 1d) or RFC3339 (2026-07-09T12:40:00Z): %w", err)
	}
	if t.Before(now) {
		return time.Time{}, fmt.Errorf("run_at is in the past")
	}
	return t, nil
}

// parseEvery parses a duration like "5m", "90s", "1h30m", "2h", "1d". Adds a
// day unit on top of Go's time.ParseDuration (which lacks "d"). Rejects
// non-positive durations.
func parseEvery(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("interval is empty")
	}
	// Support a trailing/embedded "d" (days) by expanding to hours.
	if i := strings.IndexByte(raw, 'd'); i >= 0 {
		days, err := strconv.Atoi(raw[:i])
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("interval %q: day part must be a positive integer (e.g. 1d)", raw)
		}
		rest := strings.TrimSpace(raw[i+1:])
		total := time.Duration(days) * 24 * time.Hour
		if rest != "" {
			d, err := time.ParseDuration(rest)
			if err != nil || d < 0 {
				return 0, fmt.Errorf("interval %q: %v", raw, err)
			}
			total += d
		}
		return total, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("interval %q must look like 5m / 90s / 1h30m / 1d: %w", raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("interval must be positive")
	}
	return d, nil
}

// maxCronScanMinutes bounds the forward scan for the next cron-matching
// minute so a never-matching expression (e.g. "0 0 31 2 *") can't loop
// forever. 366 days of minutes covers any valid 5-field schedule.
const maxCronScanMinutes = 366 * 24 * 60

// advance computes the next fire time for a recurring schedule after a fire
// at `from`, or the zero time if the schedule has reached a stop condition
// (max_runs / ends_at). The caller decides done-vs-continue from the zero
// check. One-shot schedules never call this.
//
// runCountAfter is the RunCount value AFTER the fire that just happened (so
// max_runs is compared against fires already completed).
func advance(m entity.ScheduledMessage, from time.Time, runCountAfter int) (time.Time, error) {
	if m.MaxRuns > 0 && runCountAfter >= m.MaxRuns {
		return time.Time{}, nil
	}
	var next time.Time
	switch {
	case m.IntervalMs > 0:
		next = from.Add(time.Duration(m.IntervalMs) * time.Millisecond)
	case strings.TrimSpace(m.Cron) != "":
		n, err := nextCronAfter(m.Cron, from)
		if err != nil {
			return time.Time{}, err
		}
		next = n
	default:
		return time.Time{}, fmt.Errorf("recurring schedule has neither interval nor cron")
	}
	if m.EndsAt != nil && next.After(*m.EndsAt) {
		return time.Time{}, nil
	}
	return next, nil
}

// nextCronAfter returns the first minute strictly after `from` whose
// wall-clock matches the 5-field cron expression. Scans minute-by-minute
// (bounded); fine for a low-volume nudge scheduler and avoids pulling in a
// full cron library.
func nextCronAfter(expr string, from time.Time) (time.Time, error) {
	if !validCron(expr) {
		return time.Time{}, fmt.Errorf("invalid cron expression %q (want 5 fields: min hour dom mon dow)", expr)
	}
	// Start at the next whole minute after `from`.
	t := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < maxCronScanMinutes; i++ {
		if cronMatchesMinute(expr, t) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron %q has no match within a year", expr)
}

// validCron reports whether expr parses as a 5-field expression.
func validCron(expr string) bool {
	return len(splitFields(expr)) == 5
}

// ── 5-field cron matcher (min hour dom mon dow) ──────────────────────
//
// Self-contained so this package doesn't depend on the workflow trigger
// package's unexported matcher. Supports *, */step, ranges a-b, a-b/step,
// and comma lists.

func cronMatchesMinute(expr string, t time.Time) bool {
	fields := splitFields(expr)
	if len(fields) != 5 {
		return false
	}
	values := [5]int{t.Minute(), t.Hour(), t.Day(), int(t.Month()), int(t.Weekday())}
	ranges := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	for i, field := range fields {
		if !cronFieldMatches(field, values[i], ranges[i][0], ranges[i][1]) {
			return false
		}
	}
	return true
}

func splitFields(s string) []string {
	return strings.Fields(s)
}

func cronFieldMatches(field string, value, min, max int) bool {
	for _, part := range strings.Split(field, ",") {
		if cronPartMatches(part, value, min, max) {
			return true
		}
	}
	return false
}

func cronPartMatches(part string, value, min, max int) bool {
	if part == "*" {
		return true
	}
	if strings.HasPrefix(part, "*/") {
		step := atoiCron(part[2:])
		if step <= 0 {
			return false
		}
		return (value-min)%step == 0
	}
	if dash := strings.IndexByte(part, '-'); dash > 0 {
		slash := strings.IndexByte(part, '/')
		var rs, re, step int
		if slash > 0 {
			rs = atoiCron(part[:dash])
			re = atoiCron(part[dash+1 : slash])
			step = atoiCron(part[slash+1:])
		} else {
			rs = atoiCron(part[:dash])
			re = atoiCron(part[dash+1:])
			step = 1
		}
		if step <= 0 {
			step = 1
		}
		if value < rs || value > re {
			return false
		}
		return (value-rs)%step == 0
	}
	return atoiCron(part) == value
}

func atoiCron(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return -1
	}
	return n
}
