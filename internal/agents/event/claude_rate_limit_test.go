package event

import (
	"testing"
	"time"
)

// A rate_limit_event as the CLI emits it in stream-json mode.
const claudeRateLimitLine = `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1792134600,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"out_of_credits","isUsingOverage":false,"unifiedWindows":{"five_hour":{"utilization":0.39,"resetsAt":1792134600},"seven_day":{"utilization":0.66,"resetsAt":1792270800}}},"uuid":"u1","session_id":"s1"}`

func TestClaudeParseRateLimitEvent(t *testing.T) {
	ev, err := NewClaudeParser().Parse(claudeRateLimitLine)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Type != Unknown {
		t.Fatalf("type = %v, want unknown (the turn is not affected)", ev.Type)
	}
	rl := ev.RateLimit
	if rl == nil {
		t.Fatal("RateLimit = nil")
	}
	if rl.Status != "allowed" || rl.ObservedAt.IsZero() {
		t.Fatalf("status/observed = %q/%v", rl.Status, rl.ObservedAt)
	}
	want := map[string]RateLimitWindow{
		"five_hour": {Utilization: 39, ResetsAt: time.Unix(1792134600, 0)},
		"seven_day": {Utilization: 66, ResetsAt: time.Unix(1792270800, 0)},
	}
	if len(rl.Windows) != len(want) {
		t.Fatalf("windows = %+v", rl.Windows)
	}
	for k, w := range want {
		got := rl.Windows[k]
		if got.Utilization != w.Utilization || !got.ResetsAt.Equal(w.ResetsAt) {
			t.Fatalf("%s = %+v, want %+v", k, got, w)
		}
	}
}

func TestClaudeParseRateLimitWithoutUnifiedWindows(t *testing.T) {
	cases := []struct {
		name, line string
		windows    map[string]float64
	}{
		{
			name:    "top-level utilization names its window",
			line:    `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","rateLimitType":"seven_day","utilization":0.9,"resetsAt":1792270800}}`,
			windows: map[string]float64{"seven_day": 90},
		},
		{
			name:    "no utilization anywhere",
			line:    `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1792134600,"rateLimitType":"five_hour"}}`,
			windows: map[string]float64{},
		},
		{
			name:    "odd field types are skipped, not fatal",
			line:    `{"type":"rate_limit_event","rate_limit_info":{"status":7,"unifiedWindows":{"five_hour":{"utilization":"x"},"seven_day":{"utilization":1.25,"resetsAt":"soon"},"bad":3}}}`,
			windows: map[string]float64{"seven_day": 125},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := NewClaudeParser().Parse(tc.line)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if ev.RateLimit == nil {
				t.Fatal("RateLimit = nil")
			}
			if len(ev.RateLimit.Windows) != len(tc.windows) {
				t.Fatalf("windows = %+v, want %v", ev.RateLimit.Windows, tc.windows)
			}
			for k, u := range tc.windows {
				if got := ev.RateLimit.Windows[k].Utilization; got != u {
					t.Fatalf("%s = %v, want %v", k, got, u)
				}
			}
		})
	}
}

func TestClaudeParseRateLimitMissingInfo(t *testing.T) {
	for _, line := range []string{
		`{"type":"rate_limit_event"}`,
		`{"type":"rate_limit_event","rate_limit_info":"nope"}`,
		`{"type":"rate_limit_event","rate_limit_info":null}`,
	} {
		ev, err := NewClaudeParser().Parse(line)
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if ev.Type != Unknown || ev.RateLimit != nil {
			t.Fatalf("%s: got %+v", line, ev)
		}
	}
}
