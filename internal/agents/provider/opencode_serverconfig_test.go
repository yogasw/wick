package provider

import "testing"

// Server mode is ON and external skills OFF unless the operator says
// otherwise; both round-trip through the config rows.
func TestOpencodeServerModeDefaults(t *testing.T) {
	ins := Instance{Type: TypeOpencode, Name: "oc"}
	vals := map[string]string{}
	for _, r := range SeedInstanceConfig(ins) {
		vals[r.Key] = r.Value
	}
	if vals["server_mode"] != "true" {
		t.Fatalf("server mode default = %q, want true", vals["server_mode"])
	}
	if vals["load_external_skills"] != "false" {
		t.Fatalf("external skills default = %q, want false", vals["load_external_skills"])
	}
	ApplyInstanceConfigKey(&ins, "server_mode", "false")
	ApplyInstanceConfigKey(&ins, "load_external_skills", "true")
	ApplyInstanceConfigKey(&ins, "server_idle_minutes", "7")
	if !ins.RunPerTurn || !ins.LoadExternalSkills || ins.ServerIdleMinutes != 7 {
		t.Fatalf("apply: %+v", ins)
	}
	vals = map[string]string{}
	for _, r := range SeedInstanceConfig(ins) {
		vals[r.Key] = r.Value
	}
	if vals["server_mode"] != "false" || vals["load_external_skills"] != "true" {
		t.Fatalf("round trip: %v", vals)
	}
	if err := ValidateInstanceConfigKey("server_idle_minutes", "-1"); err == nil {
		t.Fatal("negative idle accepted")
	}
}

func TestServerModeRowsOnlyWhereSupported(t *testing.T) {
	for _, r := range SeedInstanceConfig(Instance{Type: TypeCodex, Name: "cx"}) {
		if r.Key == "server_mode" {
			t.Fatal("codex has no server mode")
		}
	}
}
