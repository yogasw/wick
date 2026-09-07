package logintty

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestLoginCommandClaudeAppendsSlashLoginAfterExtraArgs(t *testing.T) {
	args, ok := LoginCommand(provider.TypeClaude, []string{"--permission-mode", "plan"})
	if !ok {
		t.Fatal("claude must support tty login")
	}
	want := []string{"--permission-mode", "plan", "/login"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

func TestLoginCommandCodexGeminiNotWiredYet(t *testing.T) {
	// codex/gemini reconnect lands later in codex.go / gemini.go; until
	// then Start must refuse them cleanly.
	if _, ok := LoginCommand(provider.TypeCodex, nil); ok {
		t.Fatal("codex tty login not wired yet")
	}
	if _, ok := LoginCommand(provider.TypeGemini, nil); ok {
		t.Fatal("gemini tty login not wired yet")
	}
}

func TestLoginCommandWickUnsupported(t *testing.T) {
	if _, ok := LoginCommand(provider.TypeWick, nil); ok {
		t.Fatal("wick is in-process, must not support tty login")
	}
}

// TestLoginEnvClaudeSuppressesBrowserAutoOpen: claude treats an SSH
// session as remote and prints the OAuth URL instead of opening the
// local browser. The login TTY wants exactly that behavior — wick
// parses the link and the user opens it themselves.
func TestLoginEnvClaudeSuppressesBrowserAutoOpen(t *testing.T) {
	env := LoginEnv(provider.TypeClaude)
	// BROWSER=<nonexistent> is what actually stops the tab on Windows
	// (the CLI's win32 open path has no ssh check); the SSH markers
	// cover the unix branch.
	for _, key := range []string{"BROWSER=", "SSH_TTY=", "SSH_CLIENT=", "SSH_CONNECTION="} {
		found := false
		for _, kv := range env {
			if strings.HasPrefix(kv, key) {
				found = true
			}
		}
		if !found {
			t.Fatalf("LoginEnv(claude) = %v, missing %s marker", env, key)
		}
	}
	if got := LoginEnv(provider.TypeCodex); len(got) != 0 {
		t.Fatalf("LoginEnv(codex) = %v, want empty until wired", got)
	}
}
