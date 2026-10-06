package agents

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/store"
)

// Provider idle-compact: every instance can ask for its sessions to be
// compacted once they sit idle with a full context (provider.IdleCompactConfig).
const (
	// providerIdleCompactInterval is how often sessions are checked. The
	// idle threshold is whole minutes, so a minute of lag is invisible.
	providerIdleCompactInterval = time.Minute
	// providerIdleCompactLookback bounds which sessions are looked at: a
	// conversation last used before this is history, and waking it up
	// (spawning a CLI just to summarise it) would cost more than it saves.
	providerIdleCompactLookback = 24 * time.Hour
	// providerIdleCompactPerTick caps the /compact turns started per pass.
	// Each is a real CLI turn; on a small host a burst of them is a burst
	// of processes.
	providerIdleCompactPerTick = 2
)

// idleCandidate is what the provider idle compactor needs to know about one
// session.
type idleCandidate struct {
	SessionID string
	Title     string
	// Project is the session's project name, "" when it has none.
	Project    string
	LastActive time.Time
	// Busy is true while a turn is running.
	Busy bool
}

// providerIdleCompactor sends /compact to sessions whose provider instance
// asks for it. Each session is weighed once per idle stretch: the
// LastActive it was looked at for is remembered, and only new activity
// arms it again. That also keeps the usage ledger off disk for sessions
// already decided.
type providerIdleCompactor struct {
	// Sessions lists the sessions to consider.
	Sessions func() []idleCandidate
	// Usage returns the active provider key and its context reading.
	Usage func(sessionID string) (key string, used, window int, ok bool)
	// Policy resolves an instance key's idle-compact policy.
	Policy func(key string) provider.IdleCompactPolicy
	// Compact sends /compact into the session.
	Compact func(ctx context.Context, sessionID string) error
	// Now is the clock; time.Now when nil.
	Now func() time.Time

	mu sync.Mutex
	// seen maps a session to the LastActive it was last decided for;
	// settle holds a just-compacted session until its post-compact
	// LastActive has been seen once.
	seen   map[string]time.Time
	settle map[string]bool
}

// Tick runs one pass and returns the sessions it compacted.
func (c *providerIdleCompactor) Tick(ctx context.Context) []string {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen, c.settle = map[string]time.Time{}, map[string]bool{}
	}
	var out []string
	for _, s := range c.Sessions() {
		if len(out) >= providerIdleCompactPerTick {
			break
		}
		if s.SessionID == "" || s.Busy || s.LastActive.IsZero() {
			continue
		}
		if c.settle[s.SessionID] {
			// The compact turn itself moved LastActive; that is not
			// the user coming back.
			c.seen[s.SessionID] = s.LastActive
			delete(c.settle, s.SessionID)
			continue
		}
		if at, ok := c.seen[s.SessionID]; ok && !s.LastActive.After(at) {
			continue
		}
		idle := now.Sub(s.LastActive)
		if idle > providerIdleCompactLookback {
			continue
		}
		key, used, window, ok := c.Usage(s.SessionID)
		if !ok || key == "" {
			continue
		}
		pol := c.Policy(key)
		if !pol.Enabled {
			continue
		}
		if pol.Skips(s.SessionID, s.Title, s.Project) {
			// Excluded for good: weigh it again only on new activity.
			c.seen[s.SessionID] = s.LastActive
			continue
		}
		if idle < pol.Idle {
			// Not idle long enough yet: look again next pass.
			continue
		}
		// Decided for this stretch, whichever way it goes.
		c.seen[s.SessionID] = s.LastActive
		if !pol.Due(used, window, idle) {
			continue
		}
		if err := c.Compact(ctx, s.SessionID); err != nil {
			delete(c.seen, s.SessionID)
			continue
		}
		c.settle[s.SessionID] = true
		out = append(out, s.SessionID)
	}
	return out
}

// Run ticks every interval until ctx ends.
func (c *providerIdleCompactor) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Tick(ctx)
		}
	}
}

var startProviderIdleCompact sync.Once

// startProviderIdleCompactor starts the provider idle compactor once the
// pool, manager and layout are set.
func startProviderIdleCompactor() {
	startProviderIdleCompact.Do(func() {
		c := &providerIdleCompactor{
			Sessions: func() []idleCandidate {
				if globalMgr == nil || globalPool == nil {
					return nil
				}
				lcs := map[string]string{}
				for _, e := range globalPool.ActiveSnapshot() {
					lcs[e.SessionID] = e.Lifecycle
				}
				names := map[string]string{}
				var out []idleCandidate
				for id, s := range globalMgr.Registry().Sessions() {
					lc := lcs[id]
					pid := s.Meta.ProjectID
					if _, ok := names[pid]; !ok && pid != "" {
						if p, err := project.Load(globalLayout, pid); err == nil {
							names[pid] = p.Meta.Name
						} else {
							names[pid] = ""
						}
					}
					out = append(out, idleCandidate{
						SessionID:  id,
						Title:      s.Meta.Label,
						Project:    names[pid],
						LastActive: s.Meta.LastActive,
						Busy:       lc != "" && lc != "idle",
					})
				}
				return out
			},
			Usage: func(id string) (string, int, int, bool) {
				su, err := store.LoadSessionUsage(globalLayout, id)
				if err != nil {
					return "", 0, 0, false
				}
				key := activeContextProvider(su.Providers)
				p := su.Providers[key]
				if p == nil {
					return "", 0, 0, false
				}
				return key, p.ContextUsed, p.ContextWindow, true
			},
			Policy: func(key string) provider.IdleCompactPolicy {
				all, err := provider.Load()
				if err != nil {
					return provider.IdleCompactPolicy{}
				}
				typ, name := provider.SplitInstanceKey(key)
				for _, ins := range all {
					if string(ins.Type) == typ && ins.Name == name {
						return provider.IdleCompactPolicyOf(ins)
					}
				}
				return provider.IdleCompactPolicy{}
			},
			Compact: func(ctx context.Context, sid string) error {
				s, ok := globalMgr.Registry().Session(sid)
				if !ok {
					return errors.New("session gone")
				}
				return compactSession(ctx, s)
			},
		}
		go c.Run(context.Background(), providerIdleCompactInterval)
	})
}
