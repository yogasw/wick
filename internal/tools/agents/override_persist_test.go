package agents

import (
	"os"
	"path/filepath"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider/sessionoverride"
)

// SetLayout is the single place the override store learns where session dirs
// live. This asserts that wiring really lands the sidecar next to meta.json /
// agents.json for a realistic session id, rather than silently staying
// memory-only (which is what made a /thinking toggle vanish on refresh).
func TestSetLayoutEnablesOverridePersistence(t *testing.T) {
	base := t.TempDir()
	sid := "8bbd593c-8f58-4831-8a33-33e19a201483"

	layout := agentconfig.NewLayout(base)
	if err := os.MkdirAll(layout.SessionDir(sid), 0o755); err != nil {
		t.Fatal(err)
	}

	prevLayout := globalLayout
	SetLayout(layout)
	t.Cleanup(func() {
		sessionoverride.Clear(sid)
		SetLayout(prevLayout)
		sessionoverride.SetSessionDirResolver(nil)
	})

	sessionoverride.Set(sid, "thinking_on", "true")

	want := filepath.Join(layout.SessionDir(sid), "overrides.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("sidecar not written beside agents.json: %v", err)
	}
	if v := sessionoverride.Values(sid)["thinking_on"]; v != "true" {
		t.Fatalf("value not readable back: %q", v)
	}
}

// A traversal-shaped session id must not write outside the sessions dir.
func TestOverrideResolverRejectsBadSessionID(t *testing.T) {
	base := t.TempDir()
	layout := agentconfig.NewLayout(base)

	prevLayout := globalLayout
	SetLayout(layout)
	t.Cleanup(func() {
		SetLayout(prevLayout)
		sessionoverride.SetSessionDirResolver(nil)
	})

	bad := "../../escape"
	sessionoverride.Set(bad, "thinking_on", "true")

	if _, err := os.Stat(filepath.Join(base, "escape", "overrides.json")); !os.IsNotExist(err) {
		t.Fatalf("traversal id escaped the sessions dir (err=%v)", err)
	}
	// The in-memory value is still fine — only persistence is refused.
	if v := sessionoverride.Values(bad)["thinking_on"]; v != "true" {
		t.Fatalf("in-memory value should still work: %q", v)
	}
	sessionoverride.Clear(bad)
}
