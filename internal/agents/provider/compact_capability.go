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
//   - codex: `codex exec` has no slash commands at all. The text is
//     handed to the MODEL, which plays along — measured on codex-cli
//     0.149.1, sending "/compact" produced the reply "Context
//     compacted." while the context level went UP, 24,477 → 29,901
//     tokens. Codex does compact itself once its own
//     auto_compact_token_limit is passed, but nothing in exec mode
//     triggers it on request (`-c model_auto_compact_token_limit=5000`
//     against a 29.9k context changed nothing), so wick must not
//     pretend otherwise.
//
// Anything unknown is treated as capable: a provider added later is
// more likely to behave like claude than like codex, and a button that
// does nothing is a smaller failure than one that is missing.

// CanCompact reports whether a /compact sent to this provider type
// actually compacts anything. False means the request must be answered
// by wick instead of forwarded — see Pool.send.
func CanCompact(t Type) bool { return t != TypeCodex }

// CompactUnsupportedNote explains, in the conversation, why nothing
// happened. It names the alternative that does exist so the reader is
// not left with only a refusal.
const CompactUnsupportedNote = "codex has no /compact: `codex exec` takes no slash commands, so this would have gone to the model as a plain message and been answered with a summary rather than an actual compaction. Codex compacts on its own once it reaches its auto-compact limit. To reclaim context now, start a new session."
