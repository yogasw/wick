package delegation

// Delegation modes.
//
// Sync is the default and the simple case: the leader blocks and the
// answer comes back as the tool result. Async exists because some roles
// produce output for a HUMAN, not for the leader — a research write-up
// posted to a Slack thread does not need the leader sitting idle while
// it is written.
const (
	ModeSync  = "sync"
	ModeAsync = "async"
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

// ValidMode reports whether m is a supported mode. Empty means sync.
func ValidMode(m string) bool {
	return m == "" || m == ModeSync || m == ModeAsync
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
// wins, then the profile default, then sync.
func NormalizeMode(requested, profileDefault string) string {
	if requested == ModeAsync || requested == ModeSync {
		return requested
	}
	if profileDefault == ModeAsync {
		return ModeAsync
	}
	return ModeSync
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
