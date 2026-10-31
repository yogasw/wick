package provider

import "testing"

func TestCodexSandboxConfigRoundTrip(t *testing.T) {
	ins := Instance{Type: TypeCodex, Name: "mac2"}
	assertSandbox := func(want string) {
		t.Helper()
		for _, row := range SeedInstanceConfig(ins) {
			if row.Key == "sandbox_mode" {
				if row.Value != want || row.Type != "dropdown" || row.Options != "read-only|workspace-write|danger-full-access" {
					t.Fatalf("sandbox row = %+v, want dropdown value %q", row, want)
				}
				return
			}
		}
		t.Fatal("sandbox selector missing")
	}
	assertSandbox(string(CodexSandboxFullAccess))
	if ins.CodexConfig != nil {
		t.Fatal("rendering defaults mutated the instance")
	}
	if err := ValidateInstanceConfigKey("sandbox_mode", "read-only"); err != nil {
		t.Fatal(err)
	}
	ApplyInstanceConfigKey(&ins, "sandbox_mode", "read-only")
	if ins.CodexConfig == nil || ins.CodexConfig.SandboxMode != CodexSandboxReadOnly {
		t.Fatal("read-only not applied to CodexConfig")
	}
	assertSandbox("read-only")
	raw := toUserInstance(ins)
	if raw.SandboxMode != string(CodexSandboxReadOnly) {
		t.Fatal("sandbox lost in persistence conversion")
	}
}

func TestCodexSandboxConfigValidation(t *testing.T) {
	for _, value := range []string{"", "read-only", "workspace-write", "danger-full-access"} {
		if err := ValidateInstanceConfigKey("sandbox_mode", value); err != nil {
			t.Errorf("%q: %v", value, err)
		}
	}
	if err := ValidateInstanceConfigKey("sandbox_mode", "full-auto"); err == nil {
		t.Fatal("invalid sandbox accepted")
	}
}

func TestSandboxConfigOnlyForCodex(t *testing.T) {
	isolateConfig(t)
	for _, typ := range []Type{TypeClaude, TypeGemini, TypeOMP, TypeOpencode, TypeWick} {
		ins := Instance{Type: typ, Name: "test"}
		for _, row := range SeedInstanceConfig(ins) {
			if row.Key == "sandbox_mode" {
				t.Fatalf("sandbox offered for %s", typ)
			}
		}
		ApplyInstanceConfigKey(&ins, "sandbox_mode", "read-only")
		if ins.CodexConfig != nil {
			t.Fatalf("Codex config created for %s", typ)
		}
	}
}
