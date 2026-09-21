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

// Window is one selectable range. A zero Since means all time.
type Window struct {
	Key   string    `json:"key"`
	Label string    `json:"label"`
	Since time.Time `json:"-"`
}

// WindowKeys lists the ranges in the order a UI should offer them —
// narrowest first, because the narrow ones are what people check
// repeatedly and the wide ones are what they check once.
var WindowKeys = []string{"today", "7d", "30d", "90d", "all"}

var windowLabels = map[string]string{
	"today": "Today",
	"7d":    "7 days",
	"30d":   "30 days",
	"90d":   "90 days",
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
