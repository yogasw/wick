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
	if p.Threshold != 100 {
		t.Fatalf("tokens default threshold = %d, want 100", p.Threshold)
	}
}

func TestIdleCompactPolicyDue(t *testing.T) {
	pct := IdleCompactPolicy{Enabled: true, Idle: 10 * time.Minute, Trigger: IdleCompactPercent, Threshold: 40}
	tok := IdleCompactPolicy{Enabled: true, Idle: 10 * time.Minute, Trigger: IdleCompactTokens, Threshold: 100}
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
	ApplyInstanceConfigKey(&ins, "idle_compact_minutes", "15")
	ApplyInstanceConfigKey(&ins, "idle_compact_trigger", "tokens")
	ApplyInstanceConfigKey(&ins, "idle_compact_threshold", "120")
	if !ins.IdleCompact || ins.IdleCompactMinutes != 15 || ins.IdleCompactTrigger != "tokens" || ins.IdleCompactThreshold != 120 {
		t.Fatalf("applied = %+v", ins)
	}
	if err := ValidateInstanceConfigKey("idle_compact_trigger", "window"); err == nil {
		t.Fatal("unknown trigger accepted")
	}
	if err := ValidateInstanceConfigKey("idle_compact_minutes", "-1"); err == nil {
		t.Fatal("negative minutes accepted")
	}
	if err := ValidateInstanceConfigKey("idle_compact_threshold", ""); err != nil {
		t.Fatalf("empty threshold refused: %v", err)
	}
	keys := map[string]bool{}
	for _, r := range SeedInstanceConfig(Instance{Type: TypeClaude, Name: "claude"}) {
		keys[r.Key] = true
	}
	for _, k := range []string{"idle_compact", "idle_compact_minutes", "idle_compact_trigger", "idle_compact_threshold"} {
		if !keys[k] {
			t.Errorf("seed rows miss %s", k)
		}
	}
}
