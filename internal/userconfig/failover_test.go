package userconfig

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The failover block round-trips through JSON, an instance without one
// writes no key at all, and a stored "off" survives as an explicit block.
func TestProviderInstanceFailoverJSON(t *testing.T) {
	in := ProviderInstance{Name: "yoga", Failover: &FailoverConfig{
		Enabled:        true,
		Triggers:       []string{"limit", "auth"},
		Chain:          []FailoverTarget{{Instance: "codex/waba", ModelID: "acc@gpt-5"}, {Instance: "omp/wick"}},
		RecheckMinutes: 15,
		ReturnHome:     true,
	}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"failover":{"enabled":true,"triggers":["limit","auth"],"chain":[{"instance":"codex/waba","model_id":"acc@gpt-5"},{"instance":"omp/wick"}],"recheck_minutes":15,"return_home":true}`) {
		t.Fatalf("encoded = %s", b)
	}
	var out ProviderInstance
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out.Failover, in.Failover) {
		t.Fatalf("round trip = %+v, want %+v", out.Failover, in.Failover)
	}

	b, _ = json.Marshal(ProviderInstance{Name: "plain"})
	if strings.Contains(string(b), "failover") {
		t.Fatalf("unconfigured instance wrote a failover key: %s", b)
	}

	b, _ = json.Marshal(ProviderInstance{Name: "off", Failover: &FailoverConfig{RecheckMinutes: 3}})
	var off ProviderInstance
	_ = json.Unmarshal(b, &off)
	if off.Failover == nil || off.Failover.Enabled || off.Failover.ReturnHome || off.Failover.RecheckMinutes != 3 {
		t.Fatalf("stored off block = %s → %+v", b, off.Failover)
	}
}
