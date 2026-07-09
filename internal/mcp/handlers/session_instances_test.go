package handlers

import (
	"os"
	"path/filepath"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/sessionworkspace"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

func newTestLayout(t *testing.T) (agentconfig.Layout, string) {
	t.Helper()
	base := t.TempDir()
	sid := "sess-abc"
	if err := os.MkdirAll(filepath.Join(base, "sessions", sid), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	return agentconfig.NewLayout(base), sid
}

func TestSessionInstanceForRouting(t *testing.T) {
	layout, sid := newTestLayout(t)
	inst, err := sessionworkspace.Add(layout, sid, sessionworkspace.Instance{
		BaseKey: "httprest", Label: "Staging", Config: map[string]string{"base_url": "https://x"},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Normal (non-sw_) id → not a session instance, no session_id needed.
	if _, ok, err := SessionInstanceFor(layout, map[string]any{}, "conn-123"); ok || err != nil {
		t.Fatalf("non-sw_ id: ok=%v err=%v (want false,nil)", ok, err)
	}

	// sw_ id WITHOUT session_id → error (must be passed).
	if _, ok, err := SessionInstanceFor(layout, map[string]any{}, inst.ID); ok || err == nil {
		t.Fatalf("sw_ id without session_id: ok=%v err=%v (want false,err)", ok, err)
	}

	// sw_ id WITH session_id → resolves the instance.
	target, ok, err := SessionInstanceFor(layout, map[string]any{"session_id": sid}, inst.ID)
	if err != nil || !ok {
		t.Fatalf("resolve: ok=%v err=%v", ok, err)
	}
	if target.BaseKey != "httprest" || target.Config["base_url"] != "https://x" {
		t.Fatalf("wrong target: %+v", target)
	}

	// sw_ id for a missing instance → error.
	if _, ok, err := SessionInstanceFor(layout, map[string]any{"session_id": sid}, "sw_missing"); ok || err == nil {
		t.Fatalf("missing instance: ok=%v err=%v (want false,err)", ok, err)
	}
}

func TestSessionInstanceStatus(t *testing.T) {
	mod := connector.Module{
		Configs: []entity.Config{
			{Key: "base_url", Required: true},
			{Key: "note"}, // optional
			{Key: "secret_hidden", Required: true, Hidden: true},
		},
	}
	if got := sessionInstanceStatus(mod, map[string]string{}); got != "needs_setup" {
		t.Fatalf("empty config: got %q want needs_setup", got)
	}
	// Required non-hidden field filled → ready (hidden required ignored).
	if got := sessionInstanceStatus(mod, map[string]string{"base_url": "https://x"}); got != "ready" {
		t.Fatalf("required filled: got %q want ready", got)
	}

	// A required field that ships a non-empty default is satisfied WITHOUT
	// anything in the instance config — the runtime falls back to the
	// default and the form renders it, so a fresh instance must read ready.
	// This is the regression guard for the "always needs setup until I
	// re-save" bug.
	defaulted := connector.Module{
		Configs: []entity.Config{
			{Key: "base_url", Required: true, Value: "https://default.example.com"},
			{Key: "api_key", Required: true, IsSecret: true},
		},
	}
	if got := sessionInstanceStatus(defaulted, map[string]string{"api_key": "wick_cenc_x"}); got != "ready" {
		t.Fatalf("required-with-default + secret filled: got %q want ready", got)
	}
	if got := sessionInstanceStatus(defaulted, map[string]string{}); got != "needs_setup" {
		t.Fatalf("required-with-default but secret unset: got %q want needs_setup", got)
	}
}
