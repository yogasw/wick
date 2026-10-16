package agents

import (
	"context"
	"os"
	"strings"
	"testing"

	agentsconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/teamlink"
)

// A team turn aimed at a session that does not exist fails instead of
// letting globalPool.Send create a new, empty one.
func TestRunInMissingSessionDoesNotCreateIt(t *testing.T) {
	layout := agentsconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	prevLayout, prevPool := globalLayout, globalPool
	globalLayout = layout
	globalPool = pool.New(pool.PoolConfig{Layout: layout})
	t.Cleanup(func() {
		globalPool.Stop()
		globalLayout, globalPool = prevLayout, prevPool
	})

	const sid = "slack-owner-1789014375.645429"
	_, _, err := poolTurns{}.RunIn(context.Background(), teamlink.Peer{ID: "a1", Handle: "moody"}, sid, "hello")
	if err == nil || !strings.Contains(err.Error(), "session "+sid+" not found") {
		t.Fatalf("want session not found, got %v", err)
	}
	if _, statErr := os.Stat(layout.SessionDir(sid)); !os.IsNotExist(statErr) {
		t.Fatalf("session dir must not be created, stat err = %v", statErr)
	}
}
