package pool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/provider"
)

// noopSpawner satisfies provider.Spawner without ever launching a
// subprocess. attachGate runs before Spawn, so the body never gets
// hit during these tests; if it does, a clear error pops out.
type noopSpawner struct{}

func (noopSpawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	return nil, errors.New("noopSpawner: Spawn must not be called in attachGate tests")
}

// readSpec opens the spec.json the factory wrote and returns the
// parsed gate.Spec for assertions.
func readSpec(t *testing.T, dir string) gate.Spec {
	t.Helper()
	bytes, err := os.ReadFile(filepath.Join(dir, "spec.json"))
	if err != nil {
		t.Fatalf("read spec.json: %v", err)
	}
	var s gate.Spec
	if err := json.Unmarshal(bytes, &s); err != nil {
		t.Fatalf("parse spec.json: %v", err)
	}
	return s
}

func TestFactoryAttachGate_SocketPath(t *testing.T) {
	tmp := t.TempDir()
	gateDir := filepath.Join(tmp, "gate-artifacts")
	socketDir := filepath.Join(tmp, "socket")
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		t.Fatal(err)
	}

	f := &ClaudeFactory{
		Layout:  config.NewLayout(tmp),
		Spawner: noopSpawner{},
		Gate: &GateConfig{
			GateBinary:     "/bin/gate",
			Rules:          []gate.CommandRule{{Pattern: "ls *"}},
			TempDirRoot:    gateDir,
			SocketDir:      socketDir,
		},
	}

	if _, err := f.Build(FactoryOptions{SessionID: "S1", AgentName: "main"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	spec := readSpec(t, gateDir)
	want := filepath.Join(socketDir, "gate.sock")
	if spec.SocketPath != want {
		t.Errorf("SocketPath: got %q, want %q", spec.SocketPath, want)
	}
}

func TestFactoryAttachGate_AutoApprovedFor(t *testing.T) {
	tmp := t.TempDir()
	gateDir := filepath.Join(tmp, "gate-artifacts")

	var asked string
	f := &ClaudeFactory{
		Layout:  config.NewLayout(tmp),
		Spawner: noopSpawner{},
		Gate: &GateConfig{
			GateBinary:     "/bin/gate",
			TempDirRoot:    gateDir,
			AutoApprovedFor: func(sid string) []string {
				asked = sid
				return []string{"hash-aaa", "hash-bbb"}
			},
		},
	}

	if _, err := f.Build(FactoryOptions{SessionID: "S99", AgentName: "main"}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if asked != "S99" {
		t.Errorf("AutoApprovedFor called with %q, want %q", asked, "S99")
	}
	spec := readSpec(t, gateDir)
	if len(spec.AutoApproved) != 2 || spec.AutoApproved[0] != "hash-aaa" || spec.AutoApproved[1] != "hash-bbb" {
		t.Errorf("AutoApproved: %+v", spec.AutoApproved)
	}
}

func TestFactoryAttachGate_NoSocketDir(t *testing.T) {
	// Empty SocketDir = whitelist-only mode. spec.SocketPath stays
	// empty so the gate binary (Stage 3) never tries to dial.
	tmp := t.TempDir()
	gateDir := filepath.Join(tmp, "gate-artifacts")

	f := &ClaudeFactory{
		Layout:  config.NewLayout(tmp),
		Spawner: noopSpawner{},
		Gate: &GateConfig{
			GateBinary:     "/bin/gate",
			TempDirRoot:    gateDir,
		},
	}

	if _, err := f.Build(FactoryOptions{SessionID: "S1", AgentName: "main"}); err != nil {
		t.Fatalf("Build: %v", err)
	}

	spec := readSpec(t, gateDir)
	if spec.SocketPath != "" {
		t.Errorf("SocketPath should be empty when SocketDir unset, got %q", spec.SocketPath)
	}
	if len(spec.AutoApproved) != 0 {
		t.Errorf("AutoApproved should be empty when AutoApprovedFor unset, got %+v", spec.AutoApproved)
	}
}
