package pool

// capacity.go centralises every "how many slots are free" calculation so
// the spawn gate, the queue grant, and the UI all read the same numbers.
//
// Two scopes:
//
//   - Global: one cap across ALL providers (PoolConfig.MaxConcurrent).
//   - Provider: a per-instance cap (provider.Instance.MaxConcurrent).
//     0 = unlimited at provider scope — that instance is bounded only by
//     the global cap, so it can consume the entire pool if free.
//
// The effective ceiling for a provider is min(providerMax, globalRemaining):
// a provider may never exceed its own cap, nor push total spawns past the
// global cap. Example — global 10, claude 0 (unlimited), codex 1:
//   - claude can hold up to 10 (all of global) when nothing else runs.
//   - codex is hard-capped at 1 no matter how much global headroom exists.

// Capacity is a used / max / free snapshot for one scope. Max == 0 means
// "unlimited" at that scope (only meaningful for the provider scope; the
// global cap is always a positive number after New() normalises it).
type Capacity struct {
	Scope     string // "global" or "type/name"
	Used      int    // active + in-flight spawns counted against this scope
	Max       int    // configured cap; 0 = unlimited (provider scope only)
	Remaining int    // slots still grantable right now (Max<=0 → -1 = unlimited)
}

// Unlimited reports whether this scope imposes no finite cap.
func (c Capacity) Unlimited() bool { return c.Max <= 0 }

// Capacity returns the global slot usage. Caller need not hold p.mu.
func (p *Pool) Capacity() Capacity {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.capacityLocked()
}

func (p *Pool) capacityLocked() Capacity {
	used := len(p.active) + len(p.spawningKeys)
	max := p.cfg.MaxConcurrent
	if max <= 0 {
		// Unlimited global: -1 remaining sentinel so providers see endless
		// headroom (their own cap is then the only limit).
		return Capacity{Scope: "global", Used: used, Max: 0, Remaining: -1}
	}
	rem := max - used
	if rem < 0 {
		rem = 0
	}
	return Capacity{Scope: "global", Used: used, Max: max, Remaining: rem}
}

// ProviderCapacity returns the EFFECTIVE capacity for one provider
// instance: bounded by both its own per-instance cap and the global
// remaining. Used == active+spawning entries for that provider.
// Remaining is what can actually be granted right now (the min of the
// two scopes). Caller need not hold p.mu.
func (p *Pool) ProviderCapacity(pType, pName string) Capacity {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.providerCapacityLocked(pType, pName)
}

func (p *Pool) providerCapacityLocked(pType, pName string) Capacity {
	used := p.providerUsedLocked(pType, pName)
	max := providerMaxConcurrent(pType, pName)

	global := p.capacityLocked()

	// Own-scope remaining: unlimited if max<=0 (then bounded only by
	// global), else max-used floored, capped by global headroom.
	// global.Remaining == -1 means the global scope is unlimited.
	var ownRem int
	switch {
	case max <= 0:
		// Provider unlimited → exactly the global headroom (-1 if global
		// is also unlimited).
		ownRem = global.Remaining
	default:
		ownRem = max - used
		if ownRem < 0 {
			ownRem = 0
		}
		// Cap by global headroom unless global is unlimited (-1).
		if global.Remaining >= 0 && ownRem > global.Remaining {
			ownRem = global.Remaining
		}
	}

	return Capacity{
		Scope:     pType + "/" + pName,
		Used:      used,
		Max:       max,
		Remaining: ownRem,
	}
}

// providerUsedLocked counts active + in-flight spawns for a provider.
// Caller MUST hold p.mu.
func (p *Pool) providerUsedLocked(pType, pName string) int {
	used := 0
	for _, e := range p.active {
		if e.provType == pType && e.provName == pName {
			used++
		}
	}
	for k := range p.spawningKeys {
		if e, ok := p.active[k]; ok && e.provType == pType && e.provName == pName {
			used++
		}
	}
	return used
}
