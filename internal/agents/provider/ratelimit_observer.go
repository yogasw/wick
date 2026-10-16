package provider

import (
	"sync"

	"github.com/yogasw/wick/internal/agents/event"
)

var (
	rateLimitObserverMu sync.RWMutex
	rateLimitObserver   func(ins Instance, info *event.RateLimitInfo)
)

// SetRateLimitObserver installs what receives the rate-limit windows a
// running CLI reports mid-stream (tools/agents feeds them into the usage
// cache). Lives here, not there, because tools/agents imports this
// package. The observer runs on the agent's reader goroutine, so it
// must be quick.
func SetRateLimitObserver(f func(ins Instance, info *event.RateLimitInfo)) {
	rateLimitObserverMu.Lock()
	rateLimitObserver = f
	rateLimitObserverMu.Unlock()
}

// observeRateLimit hands one event's rate-limit report to the observer.
// No instance (tests, bare agents) or no windows means nothing to say.
func observeRateLimit(ins *Instance, info *event.RateLimitInfo) {
	if ins == nil || info == nil || len(info.Windows) == 0 {
		return
	}
	rateLimitObserverMu.RLock()
	f := rateLimitObserver
	rateLimitObserverMu.RUnlock()
	if f != nil {
		f(*ins, info)
	}
}
