package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/gate"
)

func TestPathGuardHoldsRoleToolLimits(t *testing.T) {
	t.Setenv("WICK_DATA_DIR", t.TempDir())
	dir := t.TempDir()
	if _, err := gate.WritePathGuard(dir, "gate-bin", gate.PathGuardSpec{}); err != nil {
		t.Fatal(err)
	}
	spec := dir + "/pathguard-spec.json"
	call := func(tool string) (int, string) {
		var stderr bytes.Buffer
		in := strings.NewReader(`{"cwd":"` + dir + `","tool_name":"` + tool + `","tool_input":{}}`)
		return runPathGuard([]string{spec}, in, &stderr), stderr.String()
	}

	// No session in the env: no limit.
	if rc, _ := call("Bash"); rc != 0 {
		t.Fatalf("no session must not limit, rc=%d", rc)
	}
	t.Setenv(gate.SessionEnv, "sess-guest")
	if rc, _ := call("Bash"); rc != 0 {
		t.Fatalf("a session without a role file must not limit, rc=%d", rc)
	}
	if err := gate.WriteSessionRole(gate.AppName(), "sess-guest", gate.SessionRole{Role: "guest", Deny: []string{"Bash", "Write"}}); err != nil {
		t.Fatal(err)
	}
	rc, msg := call("Bash")
	if rc != 2 || !strings.Contains(msg, `role "guest"`) {
		t.Fatalf("guest Bash must be refused, rc=%d msg=%q", rc, msg)
	}
	if rc, _ := call("WebSearch"); rc != 0 {
		t.Fatalf("guest WebSearch must pass, rc=%d", rc)
	}
}
