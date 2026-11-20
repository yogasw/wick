package main

import "testing"

// TestWindowSize pins the size every live session is locked to: the configured
// viewport, 1280x800 when unset or not a positive number.
func TestWindowSize(t *testing.T) {
	cases := []struct {
		name         string
		cfg          map[string]string
		wantW, wantH int
	}{
		{"unset", map[string]string{}, 1280, 800},
		{"custom", map[string]string{"viewport_width": "1440", "viewport_height": "900"}, 1440, 900},
		{"width only", map[string]string{"viewport_width": "1024"}, 1024, 800},
		{"zero falls back", map[string]string{"viewport_width": "0", "viewport_height": "0"}, 1280, 800},
		{"negative falls back", map[string]string{"viewport_width": "-5", "viewport_height": "-1"}, 1280, 800},
	}
	for _, tc := range cases {
		w, h := windowSize(ctxWith(tc.cfg))
		if w != tc.wantW || h != tc.wantH {
			t.Errorf("%s: windowSize = %dx%d, want %dx%d", tc.name, w, h, tc.wantW, tc.wantH)
		}
	}
}

// TestCloakWindowArg: the flag is only added on binaries new enough to stay
// coherent, and carries the configured size (not a maximized window).
func TestCloakWindowArg(t *testing.T) {
	if got := cloakWindowArg(true, 1280, 800); got != "--window-size=1280,800" {
		t.Errorf("supported binary: got %q", got)
	}
	if got := cloakWindowArg(true, 1440, 900); got != "--window-size=1440,900" {
		t.Errorf("custom size: got %q", got)
	}
	if got := cloakWindowArg(false, 1280, 800); got != "" {
		t.Errorf("older binary: got %q, want no flag", got)
	}
}
