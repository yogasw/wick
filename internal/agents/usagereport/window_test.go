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
	for _, key := range []string{"", "all", "banana", "1y"} {
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
