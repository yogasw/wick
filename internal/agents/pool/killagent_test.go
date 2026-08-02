package pool

import (
	"errors"
	"testing"
)

// newKillPool builds a bare Pool with only the map KillAgent reads.
func newKillPool() *Pool {
	return &Pool{
		active:       map[string]*runEntry{},
		spawningKeys: map[string]struct{}{},
		stopCh:       make(chan struct{}),
		providerMax:  func(_, _ string) int { return 0 },
	}
}

// KillAgent must report ErrAgentNotActive rather than silently
// succeeding, so callers can tell "no process to kill" (a queued
// sub-agent, or one that already exited) from "stopped it".
func TestKillAgentUnknownKeyReportsNotActive(t *testing.T) {
	p := newKillPool()
	p.active["sess-1::claude"] = &runEntry{sessID: "sess-1", agentNm: "claude"}

	if err := p.KillAgent("sess-1", "codex"); !errors.Is(err, ErrAgentNotActive) {
		t.Fatalf("unknown agent in a live session: err = %v, want ErrAgentNotActive", err)
	}
	if err := p.KillAgent("sess-nope", "claude"); !errors.Is(err, ErrAgentNotActive) {
		t.Fatalf("unknown session: err = %v, want ErrAgentNotActive", err)
	}
}

// The bug D1 fixes: Kill accepted an agentName and ignored it, matching
// every entry under the session prefix. KillAgent must address exactly
// one composite key, or stopping one sub-agent would take its siblings
// down with it.
func TestKillAgentSelectsExactlyOneEntry(t *testing.T) {
	p := newKillPool()
	p.active["sess-1::claude"] = &runEntry{sessID: "sess-1", agentNm: "claude"}
	p.active["sess-1::codex"] = &runEntry{sessID: "sess-1", agentNm: "codex"}

	// A sibling-clobbering implementation would resolve the prefix
	// "sess-1::" and match both; the exact key must miss.
	if err := p.KillAgent("sess-1", "gemini"); !errors.Is(err, ErrAgentNotActive) {
		t.Fatalf("err = %v, want ErrAgentNotActive — lookup must be exact, not prefix-based", err)
	}
	if len(p.active) != 2 {
		t.Fatalf("active = %d entries, want both siblings untouched", len(p.active))
	}
}
