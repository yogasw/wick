package config

import "testing"

// A feature whose master switch has no declared config row cannot be turned
// on at all.
//
// This is not hypothetical. Agent Memory shipped with its nav entry, its
// panel and all of its endpoints gated on agents/agentmemory_enabled, which
// defaults to off — and the key was never declared here, so it rendered on no
// settings page and there was no supported way to flip it. The only door to
// the feature was behind the door. It looked exactly like a broken deploy:
// the binary was serving, the code was in it, and the menu simply was not
// there.
//
// So: every master switch a surface hides itself behind is a row somebody can
// see and set. Hidden rows are for bookkeeping (see
// TestBootMarkersAreDeclaredConfigs), never for a switch.
func TestFeatureSwitchesAreSettable(t *testing.T) {
	// key → the surface that disappears when it is off.
	switches := map[string]string{
		"agentmemory_enabled": "the Agent Memory nav entry, panel and endpoints",
		"airouter_enabled":    "the AI Router dashboards and proxies",
		"sub_agents_enabled":  "sub-agent delegation",
		"gate_enabled":        "the command permission gate",
	}

	// Every struct that reaches the settings page, because a switch is
	// reachable if it is declared in ANY of them — the point is that it is
	// declared somewhere, not which file it lives in.
	rows := append(SeedGeneralConfig(), SeedGateConfig()...)

	seen := map[string]bool{}
	for _, r := range rows {
		want, ok := switches[r.Key]
		if !ok {
			continue
		}
		seen[r.Key] = true
		if r.Hidden {
			t.Errorf("%q gates %s but is hidden — nobody can turn it on", r.Key, want)
		}
		if r.Description == "" {
			t.Errorf("%q gates %s and says nothing about what it does", r.Key, want)
		}
	}
	for k, want := range switches {
		if !seen[k] {
			t.Errorf("%q is not a declared config row, so %s can never be enabled", k, want)
		}
	}
}
