package usagereport

import (
	"testing"
	"time"
)

// "Today" is read by a person looking at a clock on the wall. On a host
// running at UTC+7, a UTC day boundary would cut the morning off the
// wrong end and quietly report an empty "today" until 7am.
func TestParseWindowCountsDaysLocally(t *testing.T) {
	wib := time.FixedZone("WIB", 7*3600)
	now := time.Date(2026, 9, 21, 7, 42, 0, 0, wib)

	today := ParseWindow("today", now)
	want := time.Date(2026, 9, 21, 0, 0, 0, 0, wib)
	if !today.Since.Equal(want) {
		t.Fatalf("today since = %s, want local midnight %s", today.Since, want)
	}
	// A turn that ran at 06:00 WIB is part of today, even though it is
	// still yesterday in UTC.
	early := time.Date(2026, 9, 21, 6, 0, 0, 0, wib)
	if early.Before(today.Since) {
		t.Error("an early-morning turn fell outside today")
	}
}

// "7 days" is the last seven days you would point at on a calendar, not
// the last 168 hours: a rolling window makes the same figure mean
// something different depending on the hour it was read.
func TestParseWindowCoversCalendarDays(t *testing.T) {
	now := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)
	w := ParseWindow("7d", now)
	want := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	if !w.Since.Equal(want) {
		t.Fatalf("7d since = %s, want %s (today plus the six days before it)", w.Since, want)
	}
	if w.Label != "7 days" {
		t.Fatalf("label = %q", w.Label)
	}
}

// An unknown range arrives from a URL. Answering the broadest question
// is more useful than refusing to answer at all.
func TestParseWindowFallsBackToAllTime(t *testing.T) {
	for _, key := range []string{"", "all", "banana", "5y"} {
		w := ParseWindow(key, time.Now())
		if w.Key != "all" || !w.Since.IsZero() {
			t.Fatalf("ParseWindow(%q) = %+v, want the all-time window", key, w)
		}
	}
}

// The client renders its chips from this list, so the server and the UI
// cannot drift into offering different ranges.
func TestWindowsAreOfferedNarrowestFirst(t *testing.T) {
	ws := Windows(time.Now())
	if len(ws) != len(WindowKeys) || ws[0].Key != "today" || ws[len(ws)-1].Key != "all" {
		t.Fatalf("windows = %+v, want today…all", ws)
	}
	for i := 1; i < len(ws)-1; i++ {
		if !ws[i-1].Since.After(ws[i].Since) {
			t.Fatalf("window %q is not narrower than %q", ws[i-1].Key, ws[i].Key)
		}
	}
}

// The custom range is what makes a page with its own date picker agree
// with its token figures: the same two dates have to mean the same days.
func TestCustomWindowCoversWholeDays(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	from := time.Date(2026, 9, 1, 13, 0, 0, 0, loc) // a time of day, not a date
	to := time.Date(2026, 9, 7, 2, 0, 0, 0, loc)

	w := CustomWindow(from, to)
	if w.Since.Hour() != 0 || w.Since.Day() != 1 {
		t.Fatalf("since = %s, want 1 Sep at midnight", w.Since)
	}
	// The last instant of the 7th, so a turn at 23:59 on the last day is in.
	if w.Until.Day() != 7 || w.Until.Hour() != 23 {
		t.Fatalf("until = %s, want the end of 7 Sep", w.Until)
	}
	if w.CacheKey() == w.Key {
		t.Error("a custom range must not share the cache entry of every other custom range")
	}
}

// An inverted pair is a typo. Answering "zero spend" to it looks like a
// fact, so the upper bound is dropped instead.
func TestCustomWindowIgnoresAnInvertedPair(t *testing.T) {
	loc := time.UTC
	w := CustomWindow(time.Date(2026, 9, 7, 0, 0, 0, 0, loc), time.Date(2026, 9, 1, 0, 0, 0, 0, loc))
	if !w.Until.IsZero() {
		t.Fatalf("until = %s, want no upper bound", w.Until)
	}
}

// Explicit dates win over a named window: that is how a page with a
// custom picker asks for exactly the days it is showing.
func TestWindowFromQueryPrefersDates(t *testing.T) {
	now := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	w := WindowFromQuery("30d", "2026-09-01", "2026-09-07", now)
	if w.Key != "custom" || w.Since.Day() != 1 {
		t.Fatalf("w = %+v, want the custom range", w)
	}
	if w2 := WindowFromQuery("30d", "", "", now); w2.Key != "30d" {
		t.Fatalf("w2 = %+v, want the named window", w2)
	}
	// A malformed date is no bound rather than an error page.
	if w3 := WindowFromQuery("", "not-a-date", "", now); !w3.Since.IsZero() {
		t.Fatalf("w3 = %+v, want no lower bound", w3)
	}
}
