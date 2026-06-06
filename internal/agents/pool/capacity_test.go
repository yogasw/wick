package pool

import (
	"testing"
)

// newCapPool builds a bare Pool with just the fields capacity math reads
// (active, spawningKeys, cfg.MaxConcurrent). No spawner / factory — we
// poke p.active directly to model "N entries for provider X running".
//
// providerMax defaults to "everything unlimited" (0) so tests that only
// exercise the global scope need no per-provider config. Tests that need a
// finite per-provider cap call withProviderCaps to inject one — a pure
// in-memory stub, so no disk write / async provider rescan / global
// AppName mutation (all of which raced under -race in the old version).
func newCapPool(globalMax int) *Pool {
	return &Pool{
		cfg:          PoolConfig{MaxConcurrent: globalMax},
		active:       map[string]*runEntry{},
		spawningKeys: map[string]struct{}{},
		stopCh:       make(chan struct{}),
		providerMax:  func(_, _ string) int { return 0 },
	}
}

// withProviderCaps points the pool's cap resolver at a fixed map keyed by
// "type/name". Missing keys = 0 (unlimited).
func (p *Pool) withProviderCaps(caps map[string]int) {
	p.providerMax = func(pType, pName string) int { return caps[pType+"/"+pName] }
}

func (p *Pool) addActive(key, pType, pName string) {
	p.active[key] = &runEntry{sessID: key, provType: pType, provName: pName}
}

func TestCapacity_Global(t *testing.T) {
	p := newCapPool(3)
	if c := p.Capacity(); c.Used != 0 || c.Max != 3 || c.Remaining != 3 {
		t.Fatalf("empty: got used=%d max=%d rem=%d", c.Used, c.Max, c.Remaining)
	}
	p.addActive("a", "claude", "claude")
	p.addActive("b", "codex", "codex")
	if c := p.Capacity(); c.Used != 2 || c.Remaining != 1 {
		t.Fatalf("2 active: got used=%d rem=%d", c.Used, c.Remaining)
	}
	// In-flight spawns count too.
	p.spawningKeys["c"] = struct{}{}
	if c := p.Capacity(); c.Used != 3 || c.Remaining != 0 {
		t.Fatalf("full: got used=%d rem=%d", c.Used, c.Remaining)
	}
}

// TestCapacity_GlobalUnlimited: MaxConcurrent 0 = unlimited. Remaining is
// the -1 sentinel and slotFree is always true regardless of load.
func TestCapacity_GlobalUnlimited(t *testing.T) {
	p := newCapPool(0) // 0 = unlimited
	for i := 0; i < 50; i++ {
		p.addActive(string(rune('a'+i%26))+string(rune('0'+i/26)), "claude", "claude")
	}
	c := p.Capacity()
	if c.Max != 0 || c.Remaining != -1 || !c.Unlimited() {
		t.Fatalf("unlimited global: got max=%d rem=%d unlimited=%v", c.Max, c.Remaining, c.Unlimited())
	}
	// Provider with no cap under unlimited global → also unlimited (-1).
	pc := p.ProviderCapacity("claude", "claude")
	if pc.Remaining != -1 {
		t.Fatalf("unlimited provider under unlimited global: rem got %d want -1", pc.Remaining)
	}
	p.mu.Lock()
	free := p.slotFreeLocked("claude", "claude")
	p.mu.Unlock()
	if !free {
		t.Fatal("unlimited global: slot must always be free")
	}
}

func TestCapacity_GlobalRemainingNeverNegative(t *testing.T) {
	p := newCapPool(1)
	p.addActive("a", "claude", "claude")
	p.addActive("b", "claude", "claude") // over cap (e.g. preempt race)
	if c := p.Capacity(); c.Remaining != 0 {
		t.Fatalf("over cap: remaining should floor at 0, got %d", c.Remaining)
	}
}

// TestProviderCapacity_Unlimited: provider with no per-instance cap is
// bounded only by the global remaining — it can consume the whole pool.
// Uses a provider type that isn't configured on disk, so
// providerMaxConcurrent returns 0 (unlimited) without needing userconfig.
func TestProviderCapacity_Unlimited(t *testing.T) {
	p := newCapPool(10)
	// 3 claude running; claude has no configured cap → unlimited.
	p.addActive("a", "claude", "claude")
	p.addActive("b", "claude", "claude")
	p.addActive("c", "claude", "claude")

	c := p.ProviderCapacity("claude", "claude")
	if c.Used != 3 {
		t.Fatalf("used: got %d want 3", c.Used)
	}
	if !c.Unlimited() {
		t.Fatalf("claude should be unlimited (Max=0), got Max=%d", c.Max)
	}
	// Effective remaining = global remaining = 10 - 3 = 7.
	if c.Remaining != 7 {
		t.Fatalf("remaining: got %d want 7 (global headroom)", c.Remaining)
	}
}

// TestProviderCapacity_CappedByGlobal: even an unlimited provider can't
// exceed the global cap. Global 2, 2 claude running → remaining 0.
func TestProviderCapacity_CappedByGlobal(t *testing.T) {
	p := newCapPool(2)
	p.addActive("a", "claude", "claude")
	p.addActive("b", "claude", "claude")
	c := p.ProviderCapacity("claude", "claude")
	if c.Remaining != 0 {
		t.Fatalf("global full: remaining should be 0, got %d", c.Remaining)
	}
}

// TestProviderCapacity_PerInstanceCap exercises the finite per-provider
// path: global 10, codex capped at 1 → codex hard-capped at 1 regardless
// of global headroom.
func TestProviderCapacity_PerInstanceCap(t *testing.T) {
	p := newCapPool(10)
	p.withProviderCaps(map[string]int{"codex/codex": 1})

	// No codex running yet: effective remaining = min(cap 1, global 10) = 1.
	c := p.ProviderCapacity("codex", "codex")
	if c.Max != 1 {
		t.Fatalf("codex Max: got %d want 1", c.Max)
	}
	if c.Remaining != 1 {
		t.Fatalf("codex remaining (idle): got %d want 1", c.Remaining)
	}

	// One codex running: cap reached, remaining 0 — even with global 9 free.
	p.addActive("x", "codex", "codex")
	c = p.ProviderCapacity("codex", "codex")
	if c.Used != 1 {
		t.Fatalf("codex used: got %d want 1", c.Used)
	}
	if c.Remaining != 0 {
		t.Fatalf("codex at cap: remaining should be 0 (hard cap), got %d", c.Remaining)
	}

	// A different unlimited provider still has the rest of global free.
	cc := p.ProviderCapacity("claude", "claude")
	if cc.Remaining != 9 { // 10 global - 1 codex
		t.Fatalf("claude remaining alongside codex: got %d want 9", cc.Remaining)
	}
}

// TestProviderCapacity_FiniteUnderUnlimitedGlobal: a provider's own cap
// still applies even when the global cap is unlimited. global 0, codex 1
// → codex hard-capped at 1.
func TestProviderCapacity_FiniteUnderUnlimitedGlobal(t *testing.T) {
	p := newCapPool(0) // global unlimited
	p.withProviderCaps(map[string]int{"codex/codex": 1})
	c := p.ProviderCapacity("codex", "codex")
	if c.Remaining != 1 {
		t.Fatalf("codex idle under unlimited global: rem got %d want 1", c.Remaining)
	}
	p.addActive("x", "codex", "codex")
	c = p.ProviderCapacity("codex", "codex")
	if c.Remaining != 0 {
		t.Fatalf("codex at cap under unlimited global: rem got %d want 0", c.Remaining)
	}
}

// TestSlotFreeLocked_GatesOnBothScopes: the spawn gate honours whichever
// scope is tighter.
func TestSlotFreeLocked_PerProviderTighter(t *testing.T) {
	p := newCapPool(10)
	p.withProviderCaps(map[string]int{"codex/codex": 1})
	p.mu.Lock()
	free := p.slotFreeLocked("codex", "codex")
	p.mu.Unlock()
	if !free {
		t.Fatal("codex should have a free slot when idle")
	}

	p.addActive("x", "codex", "codex")
	p.mu.Lock()
	free = p.slotFreeLocked("codex", "codex")
	p.mu.Unlock()
	if free {
		t.Fatal("codex at its cap of 1 — slot must NOT be free even with global headroom")
	}

	// claude (unlimited) still free.
	p.mu.Lock()
	free = p.slotFreeLocked("claude", "claude")
	p.mu.Unlock()
	if !free {
		t.Fatal("claude (unlimited) should still be free")
	}
}
