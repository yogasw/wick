package event

import (
	"fmt"
	"strconv"
)

// Summary is the one-line, human-readable rendering of a compaction
// boundary, e.g. "Context compacted (manual) — 31.3k → 4.1k tokens".
//
// It lives on the event rather than in one consumer because two of them
// need the exact same sentence: the store writes it as the system turn's
// Text (the plain-text fallback for readers that show no structure), and
// the chat channels post it into Slack/Telegram, where the conversation
// has no system row to render the numbers in. A compaction that reads one
// way in the transcript and another in the thread is a needless puzzle for
// whoever is comparing the two.
//
// An empty Trigger reads as "auto": the CLI names the trigger only for a
// manual /compact, and a boundary with no trigger at all is one the window
// limit forced.
func (c *CompactionInfo) Summary() string {
	if c == nil {
		return ""
	}
	trigger := c.Trigger
	if trigger == "" {
		trigger = "auto"
	}
	if c.PreTokens == 0 && c.PostTokens == 0 {
		// The CLI reported no counts (opencode's summarize answers true).
		return fmt.Sprintf("Context compacted (%s)", trigger)
	}
	if c.PostTokens == 0 {
		// Only the "before" side is known (omp's RPC compact answers with
		// tokensBefore alone when the provider compacts remotely). "→ 0"
		// would read as an emptied context, which it is not.
		return fmt.Sprintf("Context compacted (%s) — was %s tokens", trigger, ShortTokens(c.PreTokens))
	}
	return fmt.Sprintf("Context compacted (%s) — %s → %s tokens",
		trigger, ShortTokens(c.PreTokens), ShortTokens(c.PostTokens))
}

// ShortTokens renders a count the way a person reads it: 31261 -> 31.3k.
func ShortTokens(n int) string {
	switch {
	case n < 1000:
		return strconv.Itoa(n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	}
}
