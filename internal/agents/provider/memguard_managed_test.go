package provider

import (
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider/memscope"
)

// An OnPath-only guard relies on a PATH shim, which a managed binary
// (spawned by absolute path under the data dir) never passes through, so
// wick must wrap managed binaries itself — and only those.
func TestWrapsManagedOnPathOnly(t *testing.T) {
	t.Setenv("WICK_DATA_DIR", t.TempDir())
	g := &MemGuard{Mode: config.MemGuardEnforce, Scopes: config.GuardScopes{OnPath: true}}
	managed := filepath.Join(ManagedBinRoot(), "omp", "versions", "18.4.3", "omp")
	if g.wraps() {
		t.Fatal("OnPath-only must not wrap ordinary spawns")
	}
	withBackend(t, memscope.BackendSystemd)
	if !g.wrapsManaged(managed) {
		t.Fatal("managed binary not wrapped under an OnPath-only guard")
	}
	if g.wrapsManaged("/usr/local/bin/omp") {
		t.Fatal("non-managed path wrapped")
	}
	off := &MemGuard{Mode: config.MemGuardOff, Scopes: config.GuardScopes{OnPath: true}}
	if off.wrapsManaged(managed) {
		t.Fatal("guard off must not wrap")
	}
}
