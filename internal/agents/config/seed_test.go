package config

import "testing"

// The boot markers must be DECLARED rows: configs.SetOwned refuses a key
// with no meta entry, so a marker missing from the reflected struct is
// silently never persisted and its one-shot boot step re-runs forever.
// That exact bug shipped once — the role seeder's marker was never
// declared, so every boot re-seeded and the mode migration re-flipped
// roles an operator had deliberately set back.
func TestBootMarkersAreDeclaredConfigs(t *testing.T) {
	rows := SeedGeneralConfig()
	want := map[string]bool{
		"sub_agents_seeded_roles_v1":       false,
		"sub_agents_background_default_v1": false,
	}
	for _, r := range rows {
		if _, ok := want[r.Key]; ok {
			want[r.Key] = true
			if !r.Hidden {
				t.Errorf("marker %q must be hidden — it is bookkeeping, not a setting", r.Key)
			}
		}
	}
	for k, seen := range want {
		if !seen {
			t.Errorf("marker %q is not declared; SetOwned will refuse it and the boot step will re-run every start", k)
		}
	}
}
