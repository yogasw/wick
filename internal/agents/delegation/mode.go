package delegation

import "strings"

// Delegation modes.
//
// BACKGROUND is the DEFAULT: the leader gets a handle back, ends its turn,
// and is woken when the result lands. FOREGROUND — the leader blocking
// inside the tool call until the child answers — is the opt-in.
//
// The default is that way round because of what a blocked leader costs.
// Its provider process stays resident with the whole conversation loaded
// while it does nothing, and a room full of leaders waiting on children is
// a room full of idle processes holding memory. Worse, a blocked leader
// cannot be talked to: the human sees a spinner, and the run is only
// cancellable by killing it.
//
// Foreground is still right for a short lookup whose answer the very next
// sentence depends on — under a handful of seconds, one child, nothing
// else to get on with. Anything longer, or more than one child, wants the
// background and the queue.
//
// Two vocabularies, deliberately. The stored values stay "sync"/"async"
// because rows in agent_delegations and agent_profiles already hold them
// and renaming a column's contents buys nothing. Everything a person or a
// model reads says background/foreground, which is what the mode actually
// does — nobody has to remember which of two Greek-derived adjectives
// means "I have to wait". ParseMode reads both spellings; ModeLabel is
// what goes back out.
const (
	ModeSync  = "sync"
	ModeAsync = "async"

	// ModeForeground / ModeBackground are the spoken names for the two
	// stored values above.
	ModeForeground = "foreground"
	ModeBackground = "background"
)

// Delivery sinks decide where an async result goes.
const (
	// SinkNone: nothing is delivered; the result lives in the monitor and
	// the rail panel only.
	SinkNone = "none"
	// SinkChannel: post the result back to the originating chat thread.
	SinkChannel = "channel"
	// SinkSession: re-prompt the leader with the result when it lands
	// (async collect).
	SinkSession = "session"
)

// ParseMode canonicalizes an incoming mode to a stored value.
//
// Accepts the spoken names and the stored ones, so a caller that learned
// "async" from an older tool description is not punished for it. Empty
// stays empty — "no opinion" is a real answer that NormalizeMode resolves
// against the role.
func ParseMode(m string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(m)) {
	case "":
		return "", true
	case ModeBackground, ModeAsync:
		return ModeAsync, true
	case ModeForeground, ModeSync:
		return ModeSync, true
	}
	return "", false
}

// ModeLabel renders a stored mode as the name people and models use.
func ModeLabel(m string) string {
	if m == ModeSync {
		return ModeForeground
	}
	return ModeBackground
}

// ValidMode reports whether m is a supported mode. Empty means background.
func ValidMode(m string) bool {
	_, ok := ParseMode(m)
	return ok
}

// ValidSink reports whether s is a supported delivery sink. Empty is
// allowed and means "no delivery".
func ValidSink(s string) bool {
	switch s {
	case "", SinkNone, SinkChannel, SinkSession:
		return true
	}
	return false
}

// NormalizeMode resolves the mode for one call: an explicit request
// wins, then the profile default, then background.
//
// Only an explicit foreground — asked for by the caller or set on the role
// — blocks. Anything else, including a role whose default_mode was never
// filled in, runs in the background. Both arguments may arrive in either
// spelling.
func NormalizeMode(requested, profileDefault string) string {
	if m, ok := ParseMode(requested); ok && m != "" {
		return m
	}
	if m, ok := ParseMode(profileDefault); ok && m == ModeSync {
		return ModeSync
	}
	return ModeAsync
}

// Workspace modes.
const (
	// WorkspaceShared: the sub-agent works in the same directory as its
	// parent. Simple, and correct whenever tasks do not write.
	WorkspaceShared = "shared"
	// WorkspaceWorktree: the sub-agent gets its own git worktree, so
	// parallel coding tasks cannot overwrite each other's edits.
	WorkspaceWorktree = "worktree"
)

// ValidWorkspace reports whether w is supported. Empty means shared.
func ValidWorkspace(w string) bool {
	return w == "" || w == WorkspaceShared || w == WorkspaceWorktree
}

// NormalizeWorkspace resolves the workspace mode for one call.
func NormalizeWorkspace(requested, profileDefault string) string {
	if requested == WorkspaceWorktree || requested == WorkspaceShared {
		return requested
	}
	if profileDefault == WorkspaceWorktree {
		return WorkspaceWorktree
	}
	return WorkspaceShared
}
