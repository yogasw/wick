package teamlink

import "strings"

// Who may hand an agent a turn over the Team link (Peer.MentionFrom). A
// person's @mention is never filtered by it: the policy is about agents
// pulling each other in, not about the owner talking to their own agent.
const (
	// MentionAll takes a turn from every agent of the same owner.
	MentionAll = "all"
	// MentionCaptain takes a turn from the owner's Captain only.
	MentionCaptain = "captain"
	// MentionList takes a turn from the agents in Peer.MentionAllow.
	MentionList = "list"
	// MentionOff takes no turn from another agent.
	MentionOff = "off"
)

// Per-agent hop caps (Peer.MaxHops).
const (
	// DefaultMaxHops is an agent's cap when it never set one.
	DefaultMaxHops = MaxContextTurns
	// MaxHopsCeiling is the largest cap an agent may set.
	MaxHopsCeiling = 10
)

// NormalizeMentionFrom maps a stored value to a policy. Empty (a row saved
// before the column existed) and anything unknown read as MentionAll, the
// behaviour every agent had before the setting existed.
func NormalizeMentionFrom(v string) string {
	switch v = strings.ToLower(strings.TrimSpace(v)); v {
	case MentionCaptain, MentionList, MentionOff:
		return v
	}
	return MentionAll
}

// ValidMentionFrom reports whether v names a policy.
func ValidMentionFrom(v string) bool {
	switch v {
	case MentionAll, MentionCaptain, MentionList, MentionOff:
		return true
	}
	return false
}

// EffectiveHops is an agent's cap: DefaultMaxHops when unset, clamped to
// 1..MaxHopsCeiling.
func EffectiveHops(n int) int {
	switch {
	case n <= 0:
		return DefaultMaxHops
	case n > MaxHopsCeiling:
		return MaxHopsCeiling
	}
	return n
}

// MinHops is the cap of an exchange between peers: the smallest of theirs.
func MinHops(peers ...Peer) int {
	out := MaxHopsCeiling
	for _, p := range peers {
		if h := EffectiveHops(p.MaxHops); h < out {
			out = h
		}
	}
	return out
}

// AcceptsFrom reports whether p takes a turn handed over by caller (an
// agent, never a person).
func (p Peer) AcceptsFrom(caller Peer) bool {
	if p.Remote && p.RemoteOwnerOnly {
		return false
	}
	switch NormalizeMentionFrom(p.MentionFrom) {
	case MentionOff:
		return false
	case MentionCaptain:
		return caller.IsCaptain
	case MentionList:
		for _, id := range p.MentionAllow {
			if id == caller.ID {
				return true
			}
		}
		return false
	}
	return true
}
