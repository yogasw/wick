package main

import (
	"testing"
	"time"
)

func TestBehindSchedule(t *testing.T) {
	start := time.Now()
	if behindSchedule(start, 5) {
		t.Fatal("a path that just started is not behind")
	}
	late := time.Now().Add(-500 * time.Millisecond)
	if !behindSchedule(late, 5) {
		t.Fatal("point 5 is due at ~96ms, 500ms in is behind")
	}
	if behindSchedule(late, 40) {
		t.Fatal("point 40 is due at ~656ms, 500ms in is not behind yet")
	}
}

func TestGlobToRegexp(t *testing.T) {
	cases := []struct {
		glob, url string
		want      bool
	}{
		{"**/contact", "https://example.com/id/contact", true},
		{"**/contact", "https://example.com/id/contact/more", false},
		{"https://example.com/id", "https://example.com/id", true},
		{"https://example.com/id", "https://example.com/id/contact", false},
		{"https://example.com/*", "https://example.com/id", true},
		{"https://example.com/*", "https://example.com/id/x", false},
	}
	for _, c := range cases {
		if got := globToRegexp(c.glob).MatchString(c.url); got != c.want {
			t.Errorf("%q vs %q = %v, want %v", c.glob, c.url, got, c.want)
		}
	}
}
