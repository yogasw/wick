package state

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
)

func TestEvictIndex_DropsCachedStore(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	s := New(layout)
	id := "wf-evict"

	if err := s.IndexAppend(id, IndexEntry{ID: "r1", Status: "success"}); err != nil {
		t.Fatalf("index append: %v", err)
	}
	key := layout.WorkflowIndexDir(id)

	indexStoresMu.Lock()
	_, cached := indexStores[key]
	indexStoresMu.Unlock()
	if !cached {
		t.Fatal("store should be cached after IndexAppend")
	}

	EvictIndex(key)

	indexStoresMu.Lock()
	_, stillCached := indexStores[key]
	indexStoresMu.Unlock()
	if stillCached {
		t.Fatal("EvictIndex should drop the cached store")
	}
}

func TestEvictIndex_RecreateSameIDStaysSane(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	s := New(layout)
	id := "wf-recreate"

	if err := s.IndexAppend(id, IndexEntry{ID: "r1"}); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	EvictIndex(layout.WorkflowIndexDir(id))

	if err := s.IndexAppend(id, IndexEntry{ID: "r2"}); err != nil {
		t.Fatalf("append after evict: %v", err)
	}
	entries, _, err := s.IndexList(id, 1, 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("index should still work after evict + re-append")
	}
}

func TestEvictIndex_UnknownKeyNoPanic(t *testing.T) {
	EvictIndex("/no/such/dir/index")
}
