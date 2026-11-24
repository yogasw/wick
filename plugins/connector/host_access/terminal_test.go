package main

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/safeexec"
)

func TestParseArgsUsesLiteralArguments(t *testing.T) {
	args, err := parseArgs(`["a b",";","$(echo surprise)"]`)
	if err != nil || len(args) != 3 || args[0] != "a b" || args[2] != "$(echo surprise)" {
		t.Fatalf("literal arguments changed: %v, %v", args, err)
	}
	for _, raw := range []string{`null`, `"git status"`, `[1]`, `["\u0000"]`} {
		if _, err := parseArgs(raw); err == nil {
			t.Errorf("accepted malformed arguments: %s", raw)
		}
	}
}

func TestOutputIsBounded(t *testing.T) {
	var out cappedOutput
	input := []byte(strings.Repeat("x", 300*1024))
	n, err := out.Write(input)
	if err != nil || n != len(input) {
		t.Fatalf("writer did not consume output: %d %v", n, err)
	}
	value, truncated := out.snapshot()
	if !truncated || len(value) != 256*1024 {
		t.Fatalf("unexpected output size: %d", len(value))
	}
}

func TestTrustedExecDoesNotExpandShellSyntax(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		t.Skip("secure filesystem backend is not available")
	}
	bin, err := safeexec.LookPath("printf")
	if err != nil {
		t.Skip("printf not installed")
	}
	root := t.TempDir()
	p := testPolicy(t, root)
	p.Terminal.Allow = []string{filepath.Base(bin)}
	raw, _ := json.Marshal(p)
	t.Setenv("WICK_HOST_ACCESS_TEST_SECRET", "do-not-inherit")
	for _, env := range cleanEnv() {
		if strings.HasPrefix(env, "WICK_HOST_ACCESS_TEST_SECRET=") {
			t.Fatal("arbitrary environment variable inherited")
		}
	}
	c := connector.NewPluginCtx(t.Context(), map[string]string{"policy_json": string(raw)}, map[string]string{
		"command": bin, "cwd": root, "args_json": `["%s","$(echo surprise)"]`,
	})
	out, err := executeCommand(c, false)
	if err != nil {
		t.Fatal(err)
	}
	result := out.(map[string]any)
	if result["ok"] != true || result["stdout"] != "$(echo surprise)" {
		t.Fatalf("unexpected result: %+v", result)
	}
}
