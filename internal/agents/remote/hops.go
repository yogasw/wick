package remote

import (
	"strconv"
	"sync"
)

// HopsKey is the A2A message metadata key a wick A2A call carries: how
// many wick A2A servers the turn has passed through, this call included.
// A remote agent pointing back at wick (at itself, or along a chain that
// loops) would otherwise call itself without end.
const HopsKey = "wick_hops"

// MaxHops is the most wick A2A servers one turn may pass through; a
// request that arrives counting more is refused before it reaches the
// pool, and the refusal fails every turn up the chain.
const MaxHops = 3

// hops maps a session id to the hop count of the A2A request that last
// dispatched a turn into it.
var hops sync.Map

// SetHops records the hop count of the request now running in sessionID;
// 0 (a turn that did not come over A2A) forgets it.
func SetHops(sessionID string, n int) {
	if n <= 0 {
		hops.Delete(sessionID)
		return
	}
	hops.Store(sessionID, n)
}

// Hops is the hop count SetHops recorded for sessionID, 0 when none. An
// outgoing A2A call of that session sends Hops+1.
func Hops(sessionID string) int {
	if v, ok := hops.Load(sessionID); ok {
		return v.(int)
	}
	return 0
}

// HopsOf reads a HopsKey metadata value: a JSON number, an int, or a
// numeric string. Anything else is 0.
func HopsOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}
