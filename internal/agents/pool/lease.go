package pool

// lease.go lets work that is not a session subprocess — a one-shot LLM
// helper such as "generate a system prompt" — borrow a slot from the same
// budget sessions spawn against. Without it such a helper would fork a CLI
// beside a full pool, which is exactly the overload MaxConcurrent exists
// to prevent.
//
// A lease counts against the global cap and its provider's cap like any
// active entry, but it never jumps the session queue: while a session is
// waiting for a slot, TryLease refuses, so a burst of helpers cannot
// starve the conversations people are waiting on. Releasing a lease runs
// the queue grant, as an exiting agent would.

// leaseEntry is one borrowed slot and the provider it is charged to.
type leaseEntry struct {
	provType string
	provName string
}

// TryLease reserves one slot for key on provider pType/pName. ok is false
// when the pool is closed, a session is queued, the key already holds a
// lease, or either cap (or the free-memory floor) is exhausted — the
// caller waits and tries again. release is idempotent.
func (p *Pool) TryLease(key, pType, pName string) (release func(), ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || len(p.queue) > 0 {
		return nil, false
	}
	if _, held := p.leases[key]; held {
		return nil, false
	}
	if !p.slotFreeLocked(pType, pName) {
		return nil, false
	}
	if p.leases == nil {
		p.leases = map[string]leaseEntry{}
	}
	p.leases[key] = leaseEntry{provType: pType, provName: pName}
	released := false
	return func() {
		p.mu.Lock()
		if released {
			p.mu.Unlock()
			return
		}
		released = true
		delete(p.leases, key)
		p.mu.Unlock()
		p.tryGrantQueue()
	}, true
}

// LeaseCount reports how many slots are currently lent out.
func (p *Pool) LeaseCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.leases)
}
