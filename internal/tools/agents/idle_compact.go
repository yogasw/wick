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
	// providerIdleCompactInterval is how often sessions are checked: the
	// idle setting is in seconds, so a session is compacted at most this
	// long after it is due.
	providerIdleCompactInterval = 15 * time.Second
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
	// Project is the session's project name, ProjectID its id; both ""
	// when it has none.
	Project    string
	ProjectID  string
	LastActive time.Time
	// SubAgent is true for a sub-agent's own session: it reports back and
	// is done, so compacting it only costs a turn.
	SubAgent bool
	// Busy is true while a turn is running (spawning or working); Idle is
	// true while the CLI sits alive after a turn that ended normally.
	// Neither: no process, or it was killed.
	Busy bool
	Idle bool
}

// settleFor bounds how long a just-compacted session is held while its
// compact turn runs.
const settleFor = 10 * time.Minute

// providerIdleCompactor sends /compact to sessions whose provider instance
// asks for it. Only a session seen going from a turn into idle while this
// process watches is armed: one that was stopped mid-turn, or that went
// quiet before wick started, is left alone, so a restart never sweeps
// through old conversations. An armed session is weighed once, when it
// has sat idle long enough.
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
	// last is the LastActive each session was last seen with; wasBusy
	// marks a turn seen running; armed marks a turn seen ending normally;
	// settle holds a just-compacted session (with when) until its compact
	// turn is over.
	last    map[string]time.Time
	wasBusy map[string]bool
	armed   map[string]bool
	settle  map[string]time.Time
}

// Tick runs one pass and returns the sessions it compacted.
func (c *providerIdleCompactor) Tick(ctx context.Context) []string {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		c.last, c.wasBusy, c.armed, c.settle = map[string]time.Time{}, map[string]bool{}, map[string]bool{}, map[string]time.Time{}
	}
	var out []string
	for _, s := range c.Sessions() {
		id := s.SessionID
		if id == "" || s.SubAgent {
			continue
		}
		prev, known := c.last[id]
		c.last[id] = s.LastActive
		if at, ok := c.settle[id]; ok {
			// The compact turn itself is not the user coming back.
			if !s.Busy && (s.LastActive.After(at) || now.Sub(at) > settleFor) {
				delete(c.settle, id)
			}
			continue
		}
		switch {
		case s.Busy:
			c.wasBusy[id] = true
			continue
		case s.Idle && (c.wasBusy[id] || (known && s.LastActive.After(prev))):
			// A turn ended normally: armed for this idle stretch.
			c.armed[id] = true
			delete(c.wasBusy, id)
		case !s.Idle && c.wasBusy[id]:
			// The turn ended without going idle: stopped or killed.
			delete(c.wasBusy, id)
		}
		if !c.armed[id] || len(out) >= providerIdleCompactPerTick {
			continue
		}
		idle := now.Sub(s.LastActive)
		if s.LastActive.IsZero() || idle > providerIdleCompactLookback {
			delete(c.armed, id)
			continue
		}
		key, used, window, ok := c.Usage(id)
		if !ok || key == "" {
			continue
		}
		pol := c.Policy(key)
		if !pol.Enabled || pol.Skips(provider.SessionRef{ID: id, Title: s.Title, Project: s.Project, ProjectID: s.ProjectID}) {
			delete(c.armed, id)
			continue
		}
		if idle < pol.Idle {
			// Not idle long enough yet: look again next pass.
			continue
		}
		// Decided for this stretch, whichever way it goes.
		delete(c.armed, id)
		if !pol.Due(used, window, idle) {
			continue
		}
		if err := c.Compact(ctx, id); err != nil {
			continue
		}
		c.settle[id] = now
		out = append(out, id)
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
						ProjectID:  pid,
						LastActive: s.Meta.LastActive,
						SubAgent:   s.Meta.ParentSessionID != "",
						Busy:       lc == "spawning" || lc == "working",
						Idle:       lc == "idle",
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
