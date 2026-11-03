package codemode

import (
	"context"
	"testing"
	"time"
)

func TestParseLimits(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  map[string]string
		want Limits
	}{
		{"empty", nil, DefaultLimits()},
		{"set", map[string]string{KeyDefaultTimeout: "30", KeyMaxTimeout: "600", KeyCallTimeout: "90"},
			Limits{Default: 30 * time.Second, Max: 600 * time.Second, Call: 90 * time.Second}},
		{"nonsense", map[string]string{KeyDefaultTimeout: "abc", KeyMaxTimeout: "-5", KeyCallTimeout: "0"}, DefaultLimits()},
		{"spaces", map[string]string{KeyDefaultTimeout: " 45 "},
			Limits{Default: 45 * time.Second, Max: MaxTimeout, Call: CallTimeout}},
		{"default above max", map[string]string{KeyDefaultTimeout: "200", KeyMaxTimeout: "100"},
			Limits{Default: 100 * time.Second, Max: 100 * time.Second, Call: CallTimeout}},
		{"call above max", map[string]string{KeyMaxTimeout: "40", KeyCallTimeout: "90"},
			Limits{Default: 40 * time.Second, Max: 40 * time.Second, Call: 40 * time.Second}},
		{"over the hard cap", map[string]string{KeyMaxTimeout: "999999"}, DefaultLimits()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseLimits(func(k string) string { return tc.cfg[k] }); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLimitsClamp(t *testing.T) {
	l := Limits{Default: 30 * time.Second, Max: time.Minute}
	if got := l.Clamp(0); got != 30*time.Second {
		t.Fatalf("Clamp(0) = %s", got)
	}
	if got := l.Clamp(10 * time.Minute); got != time.Minute {
		t.Fatalf("Clamp(10m) = %s", got)
	}
	if got := l.Clamp(5 * time.Second); got != 5*time.Second {
		t.Fatalf("Clamp(5s) = %s", got)
	}
}

func TestCurrentLimitsFallsBackWithoutSource(t *testing.T) {
	SetLimitsSource(nil)
	if got := CurrentLimits(); got != DefaultLimits() {
		t.Fatalf("got %+v", got)
	}
	SetLimitsSource(func() Limits { return Limits{Max: -1} })
	t.Cleanup(func() { SetLimitsSource(nil) })
	if got := CurrentLimits(); got != DefaultLimits() {
		t.Fatalf("nonsense source: got %+v", got)
	}
}

// A settings change applies to the next run, with no restart.
func TestRunPicksUpChangedLimits(t *testing.T) {
	cfg := map[string]string{}
	SetLimitsSource(func() Limits { return ParseLimits(func(k string) string { return cfg[k] }) })
	t.Cleanup(func() { SetLimitsSource(nil) })
	f := &fakeRunner{}
	h := hostFor(t, f)
	run := func(req Request) {
		t.Helper()
		req.Code = "CALL conn:a/list"
		if _, err := h.Run(context.Background(), req); err != nil {
			t.Fatal(err)
		}
	}
	run(Request{})
	if f.gotTimeout.Load() != 120000 || f.gotCallTimeout.Load() != 60000 {
		t.Fatalf("defaults: timeout_ms=%d call_timeout_ms=%d", f.gotTimeout.Load(), f.gotCallTimeout.Load())
	}
	cfg[KeyDefaultTimeout], cfg[KeyMaxTimeout], cfg[KeyCallTimeout] = "30", "600", "90"
	run(Request{})
	if f.gotTimeout.Load() != 30000 || f.gotCallTimeout.Load() != 90000 {
		t.Fatalf("changed: timeout_ms=%d call_timeout_ms=%d", f.gotTimeout.Load(), f.gotCallTimeout.Load())
	}
	run(Request{Timeout: 500 * time.Second})
	if f.gotTimeout.Load() != 500000 {
		t.Fatalf("max raised: timeout_ms=%d, want 500000", f.gotTimeout.Load())
	}
	run(Request{Timeout: time.Hour})
	if f.gotTimeout.Load() != 600000 {
		t.Fatalf("capped: timeout_ms=%d, want 600000", f.gotTimeout.Load())
	}
}

// The per-call default from the settings applies to a call the script gave
// no timeout_ms (a runner that sends none).
func TestRunUsesCallLimit(t *testing.T) {
	SetLimitsSource(func() Limits { return Limits{Call: 300 * time.Millisecond} })
	t.Cleanup(func() { SetLimitsSource(nil) })
	h := hostFor(t, &fakeRunner{})
	res, err := h.Run(context.Background(), Request{Code: "CALL conn:a/hang\nCALL conn:a/list", Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || string(res.Result) != `"ERR timed out after 0.3s,{\"op\":\"list\"}"` {
		t.Fatalf("result = %s (%s)", res.Result, res.Error)
	}
}
