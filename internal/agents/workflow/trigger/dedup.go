package trigger

import (
	"container/list"
	"sync"
	"time"
)

// Dedup tracks already-seen (channel, event_id) pairs with LRU + TTL
// semantics.
type Dedup struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	items    map[string]*list.Element
	order    *list.List
	clock    func() time.Time
}

type dedupEntry struct {
	key string
	ts  time.Time
}

// NewDedup constructs a dedup with the given capacity + TTL.
func NewDedup(capacity int, ttl time.Duration) *Dedup {
	return &Dedup{
		capacity: capacity,
		ttl:      ttl,
		items:    map[string]*list.Element{},
		order:    list.New(),
		clock:    func() time.Time { return time.Now() },
	}
}

// Seen reports whether key was inserted recently and not yet expired.
// Inserts key on miss, refreshes timestamp on hit.
func (d *Dedup) Seen(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.clock()
	if el, ok := d.items[key]; ok {
		entry := el.Value.(*dedupEntry)
		if d.ttl > 0 && now.Sub(entry.ts) > d.ttl {
			d.order.Remove(el)
			delete(d.items, key)
		} else {
			entry.ts = now
			d.order.MoveToBack(el)
			return true
		}
	}
	entry := &dedupEntry{key: key, ts: now}
	el := d.order.PushBack(entry)
	d.items[key] = el
	d.evictIfNeeded()
	return false
}

func (d *Dedup) evictIfNeeded() {
	if d.capacity <= 0 {
		return
	}
	for d.order.Len() > d.capacity {
		front := d.order.Front()
		if front == nil {
			return
		}
		entry := front.Value.(*dedupEntry)
		d.order.Remove(front)
		delete(d.items, entry.key)
	}
}

// Len returns current entry count.
func (d *Dedup) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.order.Len()
}
