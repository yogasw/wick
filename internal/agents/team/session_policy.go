package team

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// Compact policies for an agent's main chat.
const (
	// CompactAuto leaves compaction to the provider (its own threshold).
	CompactAuto = "auto"
	// CompactIdle additionally sends /compact once the chat has been idle
	// for IdleHours, so the next conversation starts on a short context.
	CompactIdle = "idle"
)

// Idle-hours bounds: an hour is the shortest gap that still reads as
// "done for now", a week the longest worth waiting for.
const (
	MinIdleHours     = 1
	MaxIdleHours     = 168
	DefaultIdleHours = 8
)

// SessionPolicy is entity.AgentPersona.SessionPolicy decoded.
type SessionPolicy struct {
	Compact   string `json:"compact"`
	IdleHours int    `json:"idle_hours"`
	// SummariseThreads asks for Slack/A2A threads to be folded into the
	// agent's memory when they go idle. Stored only for now; nothing acts
	// on it yet.
	SummariseThreads bool `json:"summarise_threads"`
}

// NormalizeSessionPolicy clamps p to valid values: an unknown policy is
// auto, and the idle hours fall within [MinIdleHours, MaxIdleHours].
func NormalizeSessionPolicy(p SessionPolicy) SessionPolicy {
	if p.Compact != CompactIdle {
		p.Compact = CompactAuto
	}
	switch {
	case p.IdleHours == 0:
		p.IdleHours = DefaultIdleHours
	case p.IdleHours < MinIdleHours:
		p.IdleHours = MinIdleHours
	case p.IdleHours > MaxIdleHours:
		p.IdleHours = MaxIdleHours
	}
	return p
}

// DecodeSessionPolicy parses the stored JSON; anything unreadable is the
// default policy.
func DecodeSessionPolicy(raw string) SessionPolicy {
	var p SessionPolicy
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &p)
	}
	return NormalizeSessionPolicy(p)
}

// EncodeSessionPolicy is the inverse of DecodeSessionPolicy.
func EncodeSessionPolicy(p SessionPolicy) string {
	b, _ := json.Marshal(NormalizeSessionPolicy(p))
	return string(b)
}

// MainChat is what the idle compactor needs to know about an agent's main
// conversation.
type MainChat struct {
	SessionID  string
	LastActive time.Time
	// Busy is true while a turn is running; compacting under it would
	// interrupt the user.
	Busy bool
}

// IdleCompactor sends /compact to an agent's main chat once it has been
// idle past the agent's policy — once per idle stretch: the LastActive it
// compacted for is remembered, and only new activity (a later LastActive)
// arms it again. The compact turn itself bumps LastActive; that bump is
// absorbed by recording the post-compact value on the next pass.
//
// The memory is in-process, so a restart can compact an already-compacted
// idle chat once more. That costs one cheap no-op compact, which is
// cheaper than persisting a mark per chat.
type IdleCompactor struct {
	// Agents lists the agents to consider (ListIdleCompact).
	Agents func(ctx context.Context) ([]entity.AgentPersona, error)
	// Main resolves an agent's main chat; ok=false when it has none.
	Main func(p entity.AgentPersona) (MainChat, bool)
	// Compact sends /compact into the session.
	Compact func(ctx context.Context, sessionID string) error
	// Now is the clock; time.Now when nil.
	Now func() time.Time

	mu sync.Mutex
	// done maps a session to the LastActive it was compacted at; after a
	// compact, settle holds the session until its post-compact
	// LastActive has been seen once.
	done   map[string]time.Time
	settle map[string]bool
}

// Tick runs one pass and returns the sessions it compacted.
func (c *IdleCompactor) Tick(ctx context.Context) []string {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	rows, err := c.Agents(ctx)
	if err != nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done == nil {
		c.done, c.settle = map[string]time.Time{}, map[string]bool{}
	}
	var out []string
	for _, p := range rows {
		pol := DecodeSessionPolicy(p.SessionPolicy)
		if p.Disabled || pol.Compact != CompactIdle {
			continue
		}
		mc, ok := c.Main(p)
		if !ok || mc.SessionID == "" || mc.Busy || mc.LastActive.IsZero() {
			continue
		}
		if c.settle[mc.SessionID] {
			// The first look after a compact: its own turn moved
			// LastActive, which is not the user coming back.
			c.done[mc.SessionID] = mc.LastActive
			delete(c.settle, mc.SessionID)
			continue
		}
		if at, seen := c.done[mc.SessionID]; seen && !mc.LastActive.After(at) {
			continue
		}
		if now.Sub(mc.LastActive) < time.Duration(pol.IdleHours)*time.Hour {
			continue
		}
		if err := c.Compact(ctx, mc.SessionID); err != nil {
			continue
		}
		c.done[mc.SessionID] = mc.LastActive
		c.settle[mc.SessionID] = true
		out = append(out, mc.SessionID)
	}
	return out
}

// Run ticks every interval until ctx ends.
func (c *IdleCompactor) Run(ctx context.Context, interval time.Duration) {
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
