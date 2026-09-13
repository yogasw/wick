package adminscope

import "testing"

// stubReader is a config map standing in for the configs service.
type stubReader map[string]string

func (s stubReader) GetOwned(owner, key string) string { return s[owner+"/"+key] }

func TestAdminSeeAllSessionsFallsBackToLegacyKey(t *testing.T) {
	cases := []struct {
		name string
		cfg  stubReader
		want bool
	}{
		{"nothing written", stubReader{}, false},
		{"legacy on, new never written", stubReader{"agents/admin_see_all": "true"}, true},
		{"new wins over legacy", stubReader{"agents/admin_see_all": "true", "agents/admin_see_all_sessions": "false"}, false},
		{"new on", stubReader{"agents/admin_see_all_sessions": "true"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AdminSeeAllSessions(tc.cfg); got != tc.want {
				t.Fatalf("AdminSeeAllSessions = %v, want %v", got, tc.want)
			}
		})
	}
	if AdminSeeAllSessions(nil) {
		t.Fatal("a nil reader must not grant the legacy unrestricted view")
	}
}

// Connectors default ON: an install that never wrote the knob must not have
// connectors taken away from its admins by the upgrade that added it.
func TestAdminSeeAllConnectorsDefaultsOn(t *testing.T) {
	if !AdminSeeAllConnectors(stubReader{}) {
		t.Fatal("unwritten config should read as on")
	}
	if !AdminSeeAllConnectors(nil) {
		t.Fatal("nil reader should read as on")
	}
	if AdminSeeAllConnectors(stubReader{"agents/admin_see_all_connectors": "false"}) {
		t.Fatal("explicit false should scope admins")
	}
	if !AdminSeeAllConnectors(stubReader{"agents/admin_see_all_connectors": "true"}) {
		t.Fatal("explicit true should stay on")
	}
	// The sessions knob must not leak into this answer.
	if !AdminSeeAllConnectors(stubReader{"agents/admin_see_all": "false"}) {
		t.Fatal("the legacy sessions key must not turn connectors off")
	}
}
