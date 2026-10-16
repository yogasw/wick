package teamlink

import "time"

// Wake marks tie a prompt the Hub delivered to the turn that answers it,
// for the channels' fallback (a Team agent that ends its answer to its
// teammates' results empty or [silent]): the turn starting after a mark
// is the one the prompt woke. A turn already running when the prompt was
// queued started before its mark, so it never takes it.

// markWake records that a prompt was handed to session at at.
func (h *Hub) markWake(session string, at time.Time) {
	h.wakeMu.Lock()
	defer h.wakeMu.Unlock()
	if h.wakes == nil {
		h.wakes = map[string][]time.Time{}
	}
	h.wakes[session] = append(h.wakes[session], at)
}

// unmarkWake drops the mark of a prompt that was not delivered after all.
func (h *Hub) unmarkWake(session string, at time.Time) {
	h.wakeMu.Lock()
	defer h.wakeMu.Unlock()
	ms := h.wakes[session]
	for i, t := range ms {
		if t.Equal(at) {
			h.wakes[session] = append(ms[:i:i], ms[i+1:]...)
			break
		}
	}
	if len(h.wakes[session]) == 0 {
		delete(h.wakes, session)
	}
}

// TakeWakes consumes session's marks made at or before turnStart — the
// prompts the turn that started then answered (or an earlier turn did) —
// and returns the earliest of them. ok false when none: the turn was not
// woken by the Hub. Marks after turnStart wait for a later turn.
func (h *Hub) TakeWakes(session string, turnStart time.Time) (earliest time.Time, ok bool) {
	h.wakeMu.Lock()
	defer h.wakeMu.Unlock()
	var keep []time.Time
	for _, t := range h.wakes[session] {
		if t.After(turnStart) {
			keep = append(keep, t)
			continue
		}
		if !ok || t.Before(earliest) {
			earliest = t
		}
		ok = true
	}
	if len(keep) == 0 {
		delete(h.wakes, session)
	} else {
		h.wakes[session] = keep
	}
	return earliest, ok
}

// HasWake reports whether session has a prompt no turn has taken yet.
func (h *Hub) HasWake(session string) bool {
	h.wakeMu.Lock()
	defer h.wakeMu.Unlock()
	return len(h.wakes[session]) > 0
}
