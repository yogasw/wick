package config

import "testing"

// A zero WidgetPolicy is what callers hand over when config is not wired
// yet (nil service at boot, tests, an unconfigured install). It must
// resolve to the fully-blocked policy — the whole design rests on an
// unconfigured system behaving exactly like the hardcoded CSP that
// preceded it.
func TestZeroPolicyResolvesToFullyBlocked(t *testing.T) {
	got := Resolve(WidgetPolicy{}, WidgetPolicy{})

	for name, v := range map[string]string{
		"FrameSrc": got.FrameSrc, "ImgSrc": got.ImgSrc,
		"MediaSrc": got.MediaSrc, "ConnectSrc": got.ConnectSrc,
	} {
		if v != ModeBlock {
			t.Errorf("%s = %q, want %q", name, v, ModeBlock)
		}
	}
	if got.AllowPopups {
		t.Error("AllowPopups true on a zero policy")
	}
	if len(got.Allowlist) != 0 {
		t.Errorf("Allowlist = %v, want empty", got.Allowlist)
	}
}

// GlobalWidgetPolicy projects the flat config knobs. A GeneralConfig whose
// widget fields were never set must produce a policy that resolves to
// blocked, not one that resolves to something the operator never chose.
func TestGlobalWidgetPolicyFromUnsetConfig(t *testing.T) {
	got := Resolve(GlobalWidgetPolicy(GeneralConfig{}), WidgetPolicy{})
	if got.FrameSrc != ModeBlock || got.AllowPopups || len(got.Allowlist) != 0 {
		t.Fatalf("unset GeneralConfig produced %+v", got)
	}
}

// The seeded defaults must agree with the blocked posture too — a default
// that disagreed with the fail-closed path would make a fresh install
// behave differently from an upgraded one.
func TestSeededDefaultsAreBlocked(t *testing.T) {
	got := Resolve(GlobalWidgetPolicy(DefaultGeneralConfig()), WidgetPolicy{})
	for name, v := range map[string]string{
		"FrameSrc": got.FrameSrc, "ImgSrc": got.ImgSrc,
		"MediaSrc": got.MediaSrc, "ConnectSrc": got.ConnectSrc,
	} {
		if v != ModeBlock {
			t.Errorf("default %s = %q, want %q", name, v, ModeBlock)
		}
	}
	if got.AllowPopups {
		t.Error("default AllowPopups is on")
	}
}
