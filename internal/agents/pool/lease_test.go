package pool

import "testing"

func TestTryLease_CountsAgainstGlobalCap(t *testing.T) {
	p := newCapPool(2)
	p.addActive("s1", "claude", "claude")
	rel, ok := p.TryLease("gen-1", "claude", "claude")
	if !ok {
		t.Fatal("lease refused with one slot free")
	}
	if c := p.Capacity(); c.Used != 2 || c.Remaining != 0 {
		t.Fatalf("lease not counted: used=%d rem=%d", c.Used, c.Remaining)
	}
	if _, ok := p.TryLease("gen-2", "claude", "claude"); ok {
		t.Fatal("second lease granted past the global cap")
	}
	rel()
	rel() // idempotent
	if c := p.Capacity(); c.Used != 1 {
		t.Fatalf("release did not free the slot: used=%d", c.Used)
	}
	if _, ok := p.TryLease("gen-2", "claude", "claude"); !ok {
		t.Fatal("lease refused after release")
	}
}

func TestTryLease_CountsAgainstProviderCap(t *testing.T) {
	p := newCapPool(5)
	p.withProviderCaps(map[string]int{"codex/codex": 1})
	if _, ok := p.TryLease("gen-1", "codex", "codex"); !ok {
		t.Fatal("first codex lease refused")
	}
	if c := p.ProviderCapacity("codex", "codex"); c.Used != 1 || c.Remaining != 0 {
		t.Fatalf("provider scope ignores lease: used=%d rem=%d", c.Used, c.Remaining)
	}
	if _, ok := p.TryLease("gen-2", "codex", "codex"); ok {
		t.Fatal("lease granted past the provider cap")
	}
	if _, ok := p.TryLease("gen-3", "claude", "claude"); !ok {
		t.Fatal("other provider blocked by codex cap")
	}
}

func TestTryLease_YieldsToQueuedSessions(t *testing.T) {
	p := newCapPool(3)
	p.queue = append(p.queue, queueEntry{sessionID: "waiting", agentName: "a"})
	if _, ok := p.TryLease("gen-1", "claude", "claude"); ok {
		t.Fatal("lease jumped a queued session")
	}
}

func TestTryLease_DuplicateKeyAndClosed(t *testing.T) {
	p := newCapPool(3)
	if _, ok := p.TryLease("gen-1", "claude", "claude"); !ok {
		t.Fatal("first lease refused")
	}
	if _, ok := p.TryLease("gen-1", "claude", "claude"); ok {
		t.Fatal("same key leased twice")
	}
	p.closed = true
	if _, ok := p.TryLease("gen-2", "claude", "claude"); ok {
		t.Fatal("lease granted on a closed pool")
	}
}
