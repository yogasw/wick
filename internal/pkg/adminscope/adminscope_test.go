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

// Providers default ON, and are their own knob: turning connectors off must
// not take provider instances away from an admin, nor the other way round.
func TestAdminSeeAllProviderInstancesDefaultsOn(t *testing.T) {
	if !AdminSeeAllProviderInstances(stubReader{}) {
		t.Fatal("unwritten config should read as on")
	}
	if !AdminSeeAllProviderInstances(nil) {
		t.Fatal("nil reader should read as on")
	}
	if AdminSeeAllProviderInstances(stubReader{"agents/admin_see_all_provider_instances": "false"}) {
		t.Fatal("explicit false should scope admins")
	}
	if !AdminSeeAllProviderInstances(stubReader{"agents/admin_see_all_provider_instances": "true"}) {
		t.Fatal("explicit true should stay on")
	}
	if !AdminSeeAllProviderInstances(stubReader{"agents/admin_see_all_connectors": "false"}) {
		t.Fatal("the connectors knob must not turn providers off")
	}
	if !AdminSeeAllConnectors(stubReader{"agents/admin_see_all_provider_instances": "false"}) {
		t.Fatal("the providers knob must not turn connectors off")
	}
}

func TestAdminSeeAllWorkflowsDefaultsOff(t *testing.T) {
	if AdminSeeAllWorkflows(stubReader{}) || AdminSeeAllWorkflows(nil) {
		t.Fatal("unwritten config should read as off")
	}
	if !AdminSeeAllWorkflows(stubReader{"agents/admin_see_all_workflows": "true"}) {
		t.Fatal("explicit true should turn it on")
	}
}
