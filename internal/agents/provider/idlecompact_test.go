package provider

import (
	"testing"
	"time"
)

func TestIdleCompactPolicyDefaults(t *testing.T) {
	p := IdleCompactPolicyOf(Instance{IdleCompact: true})
	if p.Idle != 30*time.Minute || p.Trigger != IdleCompactPercent || p.Threshold != 40 {
		t.Fatalf("defaults = %+v", p)
	}
	p = IdleCompactPolicyOf(Instance{IdleCompact: true, IdleCompactTrigger: IdleCompactTokens})
	if p.Threshold != 100_000 {
		t.Fatalf("tokens default threshold = %d, want 100000", p.Threshold)
	}
	// Saved before seconds and plain tokens: 15 minutes, 120k.
	p = IdleCompactPolicyOf(Instance{IdleCompact: true, IdleCompactMinutes: 15, IdleCompactTrigger: IdleCompactTokens, IdleCompactThreshold: 120})
	if p.Idle != 15*time.Minute || p.Threshold != 120_000 {
		t.Fatalf("legacy = %+v", p)
	}
	p = IdleCompactPolicyOf(Instance{IdleCompact: true, IdleCompactSeconds: 90, IdleCompactMinutes: 15})
	if p.Idle != 90*time.Second {
		t.Fatalf("seconds over minutes = %v", p.Idle)
	}
}

func TestParseIdleCompactThreshold(t *testing.T) {
	for in, want := range map[string]int{"": 0, "40": 40, "40%": 40, " 10 % ": 10, "100000": 100_000, "100k": 100_000, "10K": 10_000} {
		if got, err := ParseIdleCompactThreshold(in); err != nil || got != want {
			t.Errorf("%q = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"abc", "-1", "1.5k", "k"} {
		if _, err := ParseIdleCompactThreshold(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
	if got := FormatIdleCompactThreshold(IdleCompactTokens, 120); got != "120000" {
		t.Errorf("legacy tokens shown as %q", got)
	}
	if got := FormatIdleCompactThreshold(IdleCompactTokens, 1199); got != "1199" {
		t.Errorf("tokens shown as %q", got)
	}
	if got := FormatIdleCompactThreshold(IdleCompactPercent, 40); got != "40%" {
		t.Errorf("percent shown as %q", got)
	}
}

func TestIdleCompactPolicyDue(t *testing.T) {
	pct := IdleCompactPolicy{Enabled: true, Idle: 10 * time.Minute, Trigger: IdleCompactPercent, Threshold: 40}
	tok := IdleCompactPolicy{Enabled: true, Idle: 10 * time.Minute, Trigger: IdleCompactTokens, Threshold: 100_000}
	cases := []struct {
		name         string
		p            IdleCompactPolicy
		used, window int
		idle         time.Duration
		want         bool
	}{
		{"percent past threshold", pct, 80_000, 200_000, 11 * time.Minute, true},
		{"percent below threshold", pct, 79_999, 200_000, time.Hour, false},
		{"not idle long enough", pct, 150_000, 200_000, 9 * time.Minute, false},
		{"percent without window never fires", pct, 150_000, 0, time.Hour, false},
		{"tokens past threshold", tok, 100_000, 0, time.Hour, true},
		{"tokens below threshold", tok, 99_999, 1_000_000, time.Hour, false},
		{"disabled", IdleCompactPolicy{Idle: time.Minute, Trigger: IdleCompactTokens, Threshold: 1}, 50_000, 0, time.Hour, false},
		{"empty context", pct, 0, 200_000, time.Hour, false},
	}
	for _, c := range cases {
		if got := c.p.Due(c.used, c.window, c.idle); got != c.want {
			t.Errorf("%s: Due = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIdleCompactConfigKeys(t *testing.T) {
	var ins Instance
	ApplyInstanceConfigKey(&ins, "idle_compact", "true")
	ins.IdleCompactMinutes = 30
	ApplyInstanceConfigKey(&ins, "idle_compact_seconds", "90")
	ApplyInstanceConfigKey(&ins, "idle_compact_trigger", "tokens")
	ApplyInstanceConfigKey(&ins, "idle_compact_threshold", "120k")
	if !ins.IdleCompact || ins.IdleCompactSeconds != 90 || ins.IdleCompactMinutes != 0 || ins.IdleCompactTrigger != "tokens" || ins.IdleCompactThreshold != 120_000 {
		t.Fatalf("applied = %+v", ins)
	}
	if err := ValidateInstanceConfigKey("idle_compact_trigger", "window"); err == nil {
		t.Fatal("unknown trigger accepted")
	}
	if err := ValidateInstanceConfigKey("idle_compact_seconds", "-1"); err == nil {
		t.Fatal("negative seconds accepted")
	}
	if err := ValidateInstanceConfigKey("idle_compact_threshold", ""); err != nil {
		t.Fatalf("empty threshold refused: %v", err)
	}
	keys := map[string]bool{}
	for _, r := range SeedInstanceConfig(Instance{Type: TypeClaude, Name: "claude"}) {
		keys[r.Key] = true
	}
	for _, k := range []string{"idle_compact", "idle_compact_seconds", "idle_compact_trigger", "idle_compact_threshold"} {
		if !keys[k] {
			t.Errorf("seed rows miss %s", k)
		}
	}
}
