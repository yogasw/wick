package usagereport

import (
	"strings"
	"time"
)

// window.go defines the time ranges the ledger can be read over.
//
// "What did this cost" is almost never a question about all time. It is
// "today", "this week", "since we switched models" — and an all-time
// figure answers none of them, because a number that only ever grows
// cannot show that something changed. So every ledger surface takes a
// window, and they all take the SAME set, named the same way, so a
// figure copied from one page means the same thing on the next.
//
// Days are counted in the server's own timezone, not UTC: "today" is
// read by a person looking at a clock on the wall, and on a host running
// at UTC+7 a UTC day boundary would cut the morning off the wrong end.

// Window is one selectable range. A zero Since means all time; a zero
// Until means "up to now".
type Window struct {
	Key   string    `json:"key"`
	Label string    `json:"label"`
	Since time.Time `json:"-"`
	Until time.Time `json:"-"`
}

// CacheKey identifies the range for the report cache. Custom ranges
// share the "custom" key, so the bounds have to be part of it.
func (w Window) CacheKey() string {
	if w.Since.IsZero() && w.Until.IsZero() {
		return w.Key
	}
	return w.Key + "|" + w.Since.Format(time.RFC3339) + "|" + w.Until.Format(time.RFC3339)
}

// WindowKeys lists the ranges in the order a UI should offer them —
// narrowest first, because the narrow ones are what people check
// repeatedly and the wide ones are what they check once.
var WindowKeys = []string{"today", "7d", "30d", "90d", "1y", "all"}

var windowLabels = map[string]string{
	"today": "Today",
	"7d":    "7 days",
	"30d":   "30 days",
	"90d":   "90 days",
	"1y":    "1 year",
	"all":   "All time",
}

// windowDays is how many CALENDAR days each key covers, counting today
// as the first. "7d" is the last seven days you would point at on a
// calendar, not the last 168 hours — the rolling version makes a figure
// change meaning depending on the time of day it was read.
var windowDays = map[string]int{
	"today": 1,
	"7d":    7,
	"30d":   30,
	"90d":   90,
	"1y":    365,
}

// ParseWindow resolves a key to a window, falling back to all time for
// anything unrecognised (including ""). An unknown range must not be an
// error: it arrives from a URL, and answering the broadest question is
// more useful than refusing to answer.
func ParseWindow(key string, now time.Time) Window {
	key = strings.TrimSpace(strings.ToLower(key))
	days, ok := windowDays[key]
	if !ok {
		return Window{Key: "all", Label: windowLabels["all"]}
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return Window{
		Key:   key,
		Label: windowLabels[key],
		Since: midnight.AddDate(0, 0, -(days - 1)),
	}
}

// Windows lists every selectable range, so a client renders the same
// chips this package understands instead of keeping its own copy of the
// list (which is how the two drift).
func Windows(now time.Time) []Window {
	out := make([]Window, 0, len(WindowKeys))
	for _, k := range WindowKeys {
		out = append(out, ParseWindow(k, now))
	}
	return out
}

// CustomWindow bounds the ledger by explicit dates, so "1-7 Sep" on a
// page means the same seven days in its token figures.
//
// Dates are whole days in the server's timezone, the way a date picker
// means them: `from` starts at its midnight, `to` ends at the last
// instant of its day. A `to` earlier than `from` is treated as no upper
// bound rather than as an empty range — an inverted pair is a typo, and
// answering "zero" to it looks like a fact.
func CustomWindow(from, to time.Time) Window {
	w := Window{Key: "custom", Label: "Custom"}
	if !from.IsZero() {
		w.Since = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	}
	if !to.IsZero() && (from.IsZero() || !to.Before(from)) {
		end := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())
		w.Until = end.AddDate(0, 0, 1).Add(-time.Nanosecond)
	}
	if w.Since.IsZero() && w.Until.IsZero() {
		return ParseWindow("all", time.Now())
	}
	if !w.Since.IsZero() && !w.Until.IsZero() {
		w.Label = w.Since.Format("2 Jan") + " – " + w.Until.Format("2 Jan")
	}
	return w
}

// ParseDate reads a YYYY-MM-DD from a query string in the given
// location. A value it cannot read is a zero time, i.e. "no bound",
// which is the safe reading of a malformed date on a reporting page.
func ParseDate(s string, loc *time.Location) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation("2006-01-02", s, loc)
	if err != nil {
		return time.Time{}
	}
	return t
}

// WindowFromQuery resolves the range a request asked for: explicit dates
// win, otherwise a named window, otherwise all time. One function so the
// two mounts that serve this report cannot disagree about what a query
// string means.
func WindowFromQuery(window, since, until string, now time.Time) Window {
	if strings.TrimSpace(since) != "" || strings.TrimSpace(until) != "" {
		loc := now.Location()
		return CustomWindow(ParseDate(since, loc), ParseDate(until, loc))
	}
	return ParseWindow(window, now)
}
