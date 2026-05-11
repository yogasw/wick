package provider

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/userconfig"
)

// setIsolatedAppName forces the userconfig persistence layer to write
// inside a per-test path by using a unique app name keyed off the
// test's temp dir. Restores the previous AppName on cleanup.
func setIsolatedAppName(t *testing.T) {
	t.Helper()
	prev := AppName
	dir := t.TempDir()
	AppName = "wick-test-" + filepath.Base(dir)
	t.Cleanup(func() {
		if d, err := userconfig.Dir(AppName); err == nil {
			_ = removeAll(d)
		}
		AppName = prev
	})
}

func TestMergeHookCapabilityRoundtrip(t *testing.T) {
	setIsolatedAppName(t)

	// Seed an existing version probe so we can prove the merge
	// leaves it intact.
	persistFromStatus("claude", "claude", Status{
		Path:      "/usr/bin/claude",
		PathFound: true,
		Version:   "claude 2.1.142",
	})

	probedAt := time.Now().UTC().Truncate(time.Second)
	MergeHookCapability("claude", "claude", HookEventPreToolUse, HookCapability{
		Supported: true,
		Verified:  true,
		ProbedAt:  probedAt,
		Scope:     "bash+edit+mcp",
	})

	cfg, err := userconfig.Load(AppName)
	if err != nil {
		t.Fatal(err)
	}
	ps, ok := cfg.ProviderStatuses["claude/claude"]
	if !ok {
		t.Fatal("expected persisted status for claude/claude")
	}
	hc, ok := ps.Hooks[HookEventPreToolUse]
	if !ok {
		t.Fatal("expected Hooks[PreToolUse] to exist")
	}
	if !hc.Supported || !hc.Verified {
		t.Errorf("flags not persisted: %+v", hc)
	}
	if hc.Scope != "bash+edit+mcp" {
		t.Errorf("Scope = %q", hc.Scope)
	}
	if ps.Version != "claude 2.1.142" {
		t.Errorf("Version overwritten by merge: %q", ps.Version)
	}
	if ps.Path != "/usr/bin/claude" {
		t.Errorf("Path overwritten by merge: %q", ps.Path)
	}
}

func TestMergeHookCapabilityCreatesEntry(t *testing.T) {
	setIsolatedAppName(t)

	MergeHookCapability("codex", "codex", HookEventPreToolUse, HookCapability{
		Supported: true,
		Verified:  false,
		ProbedAt:  time.Now(),
		Error:     "untested",
		Scope:     "shell-only",
	})

	cfg, _ := userconfig.Load(AppName)
	ps, ok := cfg.ProviderStatuses["codex/codex"]
	if !ok {
		t.Fatal("expected merge to create new entry")
	}
	hc, ok := ps.Hooks[HookEventPreToolUse]
	if !ok {
		t.Fatal("expected Hooks[PreToolUse] populated")
	}
	if !hc.Supported {
		t.Error("Supported lost")
	}
	if hc.Verified {
		t.Error("Verified should be false")
	}
	if hc.Error != "untested" {
		t.Errorf("Error = %q", hc.Error)
	}
}

func TestMergeHookCapabilityPreservesOtherEvents(t *testing.T) {
	setIsolatedAppName(t)

	// Seed two events.
	MergeHookCapability("claude", "claude", "PreToolUse", HookCapability{
		Supported: true, Verified: true, Scope: "bash+edit+mcp",
	})
	MergeHookCapability("claude", "claude", "SessionStart", HookCapability{
		Supported: true, Verified: false, Error: "not yet probed",
	})

	// Now update one — the other must survive untouched.
	MergeHookCapability("claude", "claude", "PreToolUse", HookCapability{
		Supported: true, Verified: false, Error: "regression",
	})

	cfg, _ := userconfig.Load(AppName)
	ps := cfg.ProviderStatuses["claude/claude"]
	if len(ps.Hooks) != 2 {
		t.Fatalf("expected 2 hook entries preserved, got %d", len(ps.Hooks))
	}
	if ps.Hooks["PreToolUse"].Verified {
		t.Error("PreToolUse should reflect last write (Verified=false)")
	}
	if ps.Hooks["SessionStart"].Error != "not yet probed" {
		t.Errorf("SessionStart was clobbered: %+v", ps.Hooks["SessionStart"])
	}
}

func TestStatusFromPersistedRoundtripHooks(t *testing.T) {
	ps := userconfig.ProviderStatus{
		Path:      "/x/y",
		PathFound: true,
		Version:   "v1",
		Hooks: map[string]userconfig.HookCapability{
			HookEventPreToolUse: {
				Supported: true,
				Verified:  true,
				ProbedAt:  "2026-05-11T10:23:00Z",
				Scope:     "bash",
			},
		},
	}
	st := statusFromPersisted(Instance{Type: "claude", Name: "claude"}, ps)
	hc, ok := st.Hooks[HookEventPreToolUse]
	if !ok {
		t.Fatal("expected Hooks[PreToolUse] after conversion")
	}
	if !hc.Supported || !hc.Verified {
		t.Errorf("flags lost: %+v", hc)
	}
	if hc.Scope != "bash" {
		t.Errorf("Scope = %q", hc.Scope)
	}
	if hc.ProbedAt.IsZero() {
		t.Error("ProbedAt should parse non-zero")
	}
}
