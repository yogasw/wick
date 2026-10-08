package agents

import (
	"os"
	"path/filepath"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/storage"
	agentstore "github.com/yogasw/wick/internal/agents/store"
)

// A turn left in inflight.jsonl with no live pool entry has no process to
// finish it: the replay must end with a "killed" lifecycle, or the FE keeps
// showing "thinking…" with nothing left to clear it.
func TestSnapshotEventsInflightFallbackEndsKilled(t *testing.T) {
	prevBcast, prevPool, prevLayout := globalBcast, globalPool, globalLayout
	t.Cleanup(func() { globalBcast, globalPool, globalLayout = prevBcast, prevPool, prevLayout })

	layout := agentconfig.NewLayout(t.TempDir())
	globalBcast, globalLayout = nil, layout
	globalPool = pool.New(pool.PoolConfig{Layout: layout})

	const sid = "sess-dead"
	if evs := snapshotEvents(sid); len(evs) != 0 {
		t.Fatalf("no inflight file must replay nothing, got %#v", evs)
	}

	path := layout.SessionInflight(sid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, e := range []agentstore.InflightEntry{
		{Type: "thinking", Text: "hmm"},
		{Type: "text_delta", Text: "half "},
		{Type: "text_delta", Text: "done"},
	} {
		if err := storage.AppendJSONL(path, "wick-inflight-v1", sid, e); err != nil {
			t.Fatal(err)
		}
	}
	evs := snapshotEvents(sid)
	if len(evs) != 3 || evs[0].Type != "thinking" || evs[1].Type != "text_snapshot" || evs[1].Data != "half done" {
		t.Fatalf("replay = %#v", evs)
	}
	if last := evs[len(evs)-1]; last.Type != "lifecycle" || last.Lifecycle != "killed" {
		t.Fatalf("replay must end with lifecycle killed, got %#v", last)
	}
}
