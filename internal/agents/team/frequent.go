package team

import (
	"context"
	"slices"
)

// FrequentFunc renders the "Frequent ops" lines for one agent: the ops it
// (or everyone) calls most on the connectors it may use, with tool_id and
// signature, so it can call wick_execute without wick_list / wick_get.
// allow maps each usable connector id to an op filter (nil = every op).
type FrequentFunc func(ctx context.Context, agentID string, allow map[string]func(opKey string) bool) string

// SetFrequent wires the renderer (it needs the connectors service, which
// lives outside this package). Set at boot; nil leaves the block out.
func (s *Service) SetFrequent(f FrequentFunc) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.frequent = f
	s.mu.Unlock()
}

// frequentBlock is the block for agent agentID under sc, "" when not wired
// or empty. Read-only grants keep only non-destructive ops, which the
// renderer already requires; picked grants keep only their picked ops.
func (s *Service) frequentBlock(ctx context.Context, agentID string, sc *Scope, reach Reach) string {
	s.mu.Lock()
	f := s.frequent
	s.mu.Unlock()
	if f == nil || sc == nil || reach == nil {
		return ""
	}
	allow := map[string]func(string) bool{}
	for id, it := range reach {
		if isToolGrant(id) || !sc.AllowKey(it.Key) {
			continue
		}
		lv, g := sc.resolve(id)
		switch lv {
		case LevelAll, LevelRead:
			allow[id] = nil
		case LevelPick:
			if g != nil {
				ops := append([]string(nil), g.Ops...)
				allow[id] = func(op string) bool { return slices.Contains(ops, op) }
			}
		}
	}
	if len(allow) == 0 {
		return ""
	}
	return f(ctx, agentID, allow)
}
