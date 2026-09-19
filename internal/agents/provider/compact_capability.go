package provider

// compact_capability.go — which providers can act on /compact.
//
// "Compaction happens" and "wick can ask for it" are two different
// facts, and only the second one belongs on a button.
//
//   - claude: `/compact` is a real slash command in print mode, and the
//     CLI also compacts on its own unless DISABLE_AUTO_COMPACT is set.
//     Asking works.
//   - wick's own engine: compacts in-process, automatically past
//     compactionTriggerRatio and on demand. Asking works.
//   - codex: `codex exec` has no slash commands, so its spawner intercepts
//     the bare command and calls app-server's thread/compact/start RPC.
//
// Anything unknown is treated as capable: a provider added later is
// more likely to behave like claude than like codex, and a button that
// does nothing is a smaller failure than one that is missing.

// CanCompact reports whether a /compact sent to this provider type
// actually compacts anything. False means the request must be answered
// by wick instead of forwarded — see Pool.send.
func CanCompact(t Type) bool { return true }

// CompactUnsupportedNote is retained for providers that may opt out later.
const CompactUnsupportedNote = "manual compaction is unavailable for this provider"
