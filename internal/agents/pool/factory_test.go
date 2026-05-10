package pool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
)

// noopSpawner satisfies provider.Spawner without ever launching a
// subprocess. attachGate runs before Spawn, so the body never gets
// hit during these tests; if it does, a clear error pops out.
type noopSpawner struct{}

func (noopSpawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	return nil, errors.New("noopSpawner: Spawn must not be called in attachGate tests")
}

// TestFactoryAttachGate_WritesClaudeSettings verifies the per-spawn
// settings.json gets written under the temp dir with the gate binary
// path embedded. Stage 9 removed the per-spawn spec.json — only
// settings.json is per-spawn now; the spec lives at SharedSpecPath.
func TestFactoryAttachGate_WritesClaudeSettings(t *testing.T) {
	tmp := t.TempDir()
	gateDir := filepath.Join(tmp, "gate-artifacts")

	f := &ClaudeFactory{
		Layout:  config.NewLayout(tmp),
		Spawner: noopSpawner{},
		Gate: &GateConfig{
			GateBinary:  "/bin/gate",
			TempDirRoot: gateDir,
		},
	}

	if _, err := f.Build(FactoryOptions{SessionID: "S1", AgentName: "main"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	if _, err := os.Stat(filepath.Join(gateDir, ".claude", "settings.local.json")); err != nil {
		t.Fatalf(".claude/settings.local.json missing: %v", err)
	}
	// Stage 9 invariant: factory MUST NOT write spec.json — that
	// file lives at SharedSpecPath and is owned by the daemon.
	if _, err := os.Stat(filepath.Join(gateDir, "spec.json")); err == nil {
		t.Errorf("factory wrote spec.json — should be daemon-only post Stage 9")
	}
}

// TestFactoryAttachGate_DefaultsTempDir confirms an empty
// TempDirRoot falls back to <Layout.SessionDir(id)>/gate.
func TestFactoryAttachGate_DefaultsTempDir(t *testing.T) {
	tmp := t.TempDir()
	f := &ClaudeFactory{
		Layout:  config.NewLayout(tmp),
		Spawner: noopSpawner{},
		Gate: &GateConfig{
			GateBinary: "/bin/gate",
		},
	}
	if _, err := f.Build(FactoryOptions{SessionID: "S2", AgentName: "main"}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	want := filepath.Join(f.Layout.SessionDir("S2"), "gate", ".claude", "settings.local.json")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("default settings path missing: %v", err)
	}
}
