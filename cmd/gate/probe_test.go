package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestParseProviderArg(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"empty", nil, ""},
		{"absent", []string{"--other"}, ""},
		{"equals form", []string{"--provider=codex"}, "codex"},
		{"space form", []string{"--provider", "gemini"}, "gemini"},
		{"space form trailing", []string{"--provider"}, ""},
		{"mixed flags", []string{"--debug", "--provider=claude", "--verbose"}, "claude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseProviderArg(tc.args); got != tc.want {
				t.Errorf("parseProviderArg(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// captureStdout swaps os.Stdout, runs fn, returns what fn wrote. Used
// because emitBlockForProvider writes via fmt.Fprintln(os.Stdout).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	fn()

	_ = w.Close()
	<-done
	os.Stdout = old
	return buf.String()
}

func TestEmitBlockForProvider_Claude(t *testing.T) {
	out := captureStdout(t, func() {
		emitBlockForProvider("", "test reason")
	})
	if !strings.Contains(out, `"hookSpecificOutput"`) {
		t.Errorf("default (claude) shape missing hookSpecificOutput: %q", out)
	}
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("missing deny decision: %q", out)
	}
}

func TestEmitBlockForProvider_Codex(t *testing.T) {
	out := captureStdout(t, func() {
		emitBlockForProvider("codex", "test reason")
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("unmarshal codex output %q: %v", out, err)
	}
	if got["permissionDecision"] != "deny" {
		t.Errorf("codex permissionDecision = %v, want deny", got["permissionDecision"])
	}
	if got["reason"] != "test reason" {
		t.Errorf("codex reason = %v, want test reason", got["reason"])
	}
	// Codex shape is flat — should NOT have hookSpecificOutput envelope.
	if _, has := got["hookSpecificOutput"]; has {
		t.Error("codex output should be flat, found nested hookSpecificOutput")
	}
}

func TestEmitBlockForProvider_Gemini(t *testing.T) {
	out := captureStdout(t, func() {
		emitBlockForProvider("gemini", "test reason")
	})
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("unmarshal gemini output %q: %v", out, err)
	}
	if got["decision"] != "deny" {
		t.Errorf("gemini decision = %v, want deny", got["decision"])
	}
	if got["reason"] != "test reason" {
		t.Errorf("gemini reason = %v, want test reason", got["reason"])
	}
}

func TestEmitBlockForProvider_UnknownFallsBackToClaude(t *testing.T) {
	out := captureStdout(t, func() {
		emitBlockForProvider("unknown-provider-xyz", "x")
	})
	if !strings.Contains(out, `"hookSpecificOutput"`) {
		t.Errorf("unknown provider should fall back to claude shape, got %q", out)
	}
}
