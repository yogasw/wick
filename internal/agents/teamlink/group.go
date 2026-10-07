package teamlink

import (
	"errors"
	"fmt"
	"strings"
)

// Group chat rules. A group thread is one conversation; each member
// answers in its own session with its own access, so the only shared
// state is the thread and the turn budget worked out here. Pure: the
// wiring loads the peers and runs the turns.

// Default responders of a group (session.AgentGroup.DefaultResponder).
const (
	ResponderCaptain = "captain"
	ResponderFirst   = "first"
)

// MinGroupMembers is the fewest agents a group has.
const MinGroupMembers = 2

// GroupLimit is a group's agent-to-agent turn cap: the smallest MaxHops of
// its members, lowered further by override when override is smaller.
// override can never raise it.
func GroupLimit(members []Peer, override int) int {
	limit := MinHops(members...)
	if override > 0 && override < limit {
		limit = override
	}
	return limit
}

// ValidGroupOverride reports whether override may be stored for members:
// 0 (none) or 1..GroupLimit(members, 0).
func ValidGroupOverride(members []Peer, override int) bool {
	return override == 0 || (override >= 1 && override <= GroupLimit(members, 0))
}

// NormalizeResponder maps a stored value; anything unknown is captain.
func NormalizeResponder(v string) string {
	if v == ResponderFirst {
		return ResponderFirst
	}
	return ResponderCaptain
}

// GroupMembers checks a requested member list against the owner's agents
// (own) and returns it deduplicated, in order. Every member must be one
// of own; a group needs MinGroupMembers.
func GroupMembers(ids []string, own []Peer) ([]string, error) {
	known := make(map[string]bool, len(own))
	for _, p := range own {
		known[p.ID] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if !known[id] {
			return nil, fmt.Errorf("%q is not one of your agents", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) < MinGroupMembers {
		return nil, fmt.Errorf("a group needs at least %d agents", MinGroupMembers)
	}
	return out, nil
}

// ErrNoResponder means no enabled member can answer.
var ErrNoResponder = errors.New("no enabled agent in this group")

// DefaultResponderOf picks who answers a message with no @: the Captain
// when mode is captain and the Captain is an enabled member, else the
// first enabled member.
func DefaultResponderOf(members []Peer, mode string) (Peer, error) {
	if NormalizeResponder(mode) == ResponderCaptain {
		for _, p := range members {
			if p.IsCaptain && !p.Disabled {
				return p, nil
			}
		}
	}
	for _, p := range members {
		if !p.Disabled {
			return p, nil
		}
	}
	return Peer{}, ErrNoResponder
}

// LeadMentions lists, in line order and once each, the handles of members
// named at the start of a line ("@anton check the logs"), outside fenced
// code. requireBody skips a bare "@anton" line: a person's message may
// name an agent alone, an agent's reply may not (the same strictness as
// delegation.ParseMentions). Unknown handles are returned apart, for a
// mention_refused event.
func LeadMentions(text string, members []Peer, requireBody bool) (hit []Peer, unknown []string) {
	byHandle := make(map[string]Peer, len(members))
	for _, p := range members {
		byHandle[p.Handle] = p
	}
	seen := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(t, "@") {
			continue
		}
		rest := t[1:]
		handle, body := rest, ""
		if cut := strings.IndexAny(rest, " \t,:"); cut >= 0 {
			handle, body = rest[:cut], strings.TrimSpace(strings.TrimLeft(rest[cut:], ",:"))
		}
		handle = NormalizeHandle(handle)
		if handle == "" || seen[handle] || (requireBody && body == "") {
			continue
		}
		seen[handle] = true
		if p, ok := byHandle[handle]; ok {
			hit = append(hit, p)
		} else if validHandle(handle) {
			unknown = append(unknown, handle)
		}
	}
	return hit, unknown
}

// validHandle is the handle shape team.NormalizeHandle produces; anything
// else after an @ (an email, a decorator) is not a mention at all.
func validHandle(h string) bool {
	for i, r := range h {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || (r == '-' && i > 0)
		if !ok {
			return false
		}
	}
	return h != ""
}

// HumanTargets is who answers a person's message in a group: the members
// named at line starts (only those — keputusan 35), else the default
// responder. Disabled members are skipped and reported in refused.
func HumanTargets(text string, members []Peer, mode string) (targets []Peer, refused []string, err error) {
	hit, unknown := LeadMentions(text, members, false)
	refused = unknown
	for _, p := range hit {
		if p.Disabled {
			refused = append(refused, p.Handle)
			continue
		}
		targets = append(targets, p)
	}
	if len(hit) > 0 {
		return targets, refused, nil
	}
	p, err := DefaultResponderOf(members, mode)
	if err != nil {
		return nil, refused, err
	}
	return []Peer{p}, refused, nil
}
