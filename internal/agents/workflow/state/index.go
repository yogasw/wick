package state

import (
	"sync"
	"time"

	"github.com/yogasw/wick/internal/shardedlog"
)

// IndexEntry is the row shape stored in the per-id index. Kept
// lean so a 100-row shard stays well under 10KB.
type IndexEntry struct {
	ID         string     `json:"id"`
	Status     string     `json:"status,omitempty"`
	StartedAt  time.Time  `json:"at"`
	EndedAt    *time.Time `json:"end,omitempty"`
	DurationMs int64      `json:"ms,omitempty"`
	// Source tags how the run was kicked off so the editor can
	// distinguish manual vs automation vs test fires in the runs
	// list. Persisted as a short slug; the FE maps it to a coloured
	// pill. Empty = legacy / unknown.
	Source string `json:"src,omitempty"`
	// TriggerID is the workflow.Trigger that fired, when one was
	// identified. Empty for runs that fell back to graph.entry.
	TriggerID string `json:"trig,omitempty"`
	// TriggerType mirrors workflow.TriggerType for the firing
	// trigger ("manual" / "cron" / "channel" / …). Kept separate
	// from Source so the FE can show "automation" buckets without
	// re-loading each run's state.json.
	TriggerType string `json:"tt,omitempty"`
}

// indexStores caches one shardedlog.Store per id so concurrent
// appends share the per-Store mutex (otherwise two fresh Stores
// would race on the shard-roll decision).
var (
	indexStoresMu sync.Mutex
	indexStores   = map[string]*shardedlog.Store[IndexEntry]{}
)

// EvictIndex drops the cached Store for an index dir after its workflow
// is deleted, so the global cache stops pinning a dead directory.
func EvictIndex(indexDir string) {
	indexStoresMu.Lock()
	defer indexStoresMu.Unlock()
	delete(indexStores, indexDir)
}

func (s *FileStore) indexStore(id string) *shardedlog.Store[IndexEntry] {
	indexStoresMu.Lock()
	defer indexStoresMu.Unlock()
	key := s.Layout.WorkflowIndexDir(id)
	if v, ok := indexStores[key]; ok {
		return v
	}
	v := &shardedlog.Store[IndexEntry]{Dir: key}
	indexStores[key] = v
	return v
}

// IndexAppend persists one summary row to the id's sharded index.
// Constant-time regardless of total run history (touches only the
// current shard).
func (s *FileStore) IndexAppend(id string, entry IndexEntry) error {
	return s.indexStore(id).Append(entry)
}

// IndexList returns one page of summaries, newest first.
// pageSize defaults to shardedlog.DefaultShardMax. hasMore=true
// when older pages exist.
func (s *FileStore) IndexList(id string, page, pageSize int) ([]IndexEntry, bool, error) {
	return s.indexStore(id).Page(page, pageSize)
}
