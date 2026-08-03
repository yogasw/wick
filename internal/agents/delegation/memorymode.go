package delegation

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// Memory modes.
//
// A sub-agent starts with a clean context, so what it knows is exactly
// what it is told. That used to be whatever the leader happened to paste
// into `context` — which meant the choice was made accidentally, once per
// call, by a model with no vocabulary for it. These four modes make the
// choice explicit and name its cost.
const (
	// MemoryNone: the task and nothing else. For work whose answer does
	// not depend on what anyone else found.
	MemoryNone = "no_history"
	// MemorySummary: one line per finished sibling. The default, because
	// it is cheap and it is what stops iteration 3 re-deriving what
	// iterations 1 and 2 already established.
	MemorySummary = "state_summary"
	// MemoryChunks: the caller's context verbatim. The leader curates;
	// wick does not guess.
	MemoryChunks = "relevant_chunks"
	// MemoryFull: every sibling's full result. Audit and debugging only —
	// expensive, noisy, and it biases a fresh agent toward the
	// conclusions of the agents before it.
	MemoryFull = "full_history"
)

// memorySiblingSummaryRunes caps a sibling line so a state summary stays
// a summary. A sibling that wrote three paragraphs contributes one line
// like every other.
const memorySiblingSummaryRunes = 200

// ValidMemoryMode reports whether m is supported. Empty is allowed and
// resolves to the default.
func ValidMemoryMode(m string) bool {
	switch m {
	case "", MemoryNone, MemorySummary, MemoryChunks, MemoryFull:
		return true
	}
	return false
}

// MemoryModes lists the valid values, for error messages that tell a
// caller what it may say instead.
func MemoryModes() []string {
	return []string{MemoryNone, MemorySummary, MemoryChunks, MemoryFull}
}

// NormalizeMemoryMode resolves the mode for one call: an explicit request
// wins, then the role default, then state_summary.
func NormalizeMemoryMode(requested, profileDefault string) string {
	if requested != "" && ValidMemoryMode(requested) {
		return requested
	}
	if profileDefault != "" && ValidMemoryMode(profileDefault) {
		return profileDefault
	}
	return MemorySummary
}

// MemoryPayload renders what WICK adds to a sub-agent's task for a mode.
//
// The caller's own `context` field is appended separately and in every
// mode — a leader always gets to say something. This governs only the
// history wick contributes on top.
//
// Siblings are the tree's other delegations. A snapshot, like the roster:
// read once at spawn, and labelled as such rather than implying it will
// stay current.
func MemoryPayload(mode string, siblings []entity.AgentDelegation, inc *entity.AgentIncident) string {
	switch mode {
	case MemoryNone, "":
		return ""
	case MemoryChunks:
		// The context field IS the payload here; wick adds nothing.
		return ""
	case MemoryFull:
		return joinBlocks(renderFullHistory(siblings), renderIncidentState(inc))
	default:
		return joinBlocks(renderStateSummary(siblings), renderIncidentState(inc))
	}
}

func joinBlocks(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "")
}

// renderIncidentState is the whole point of the store: a sub-agent
// spawned in iteration 3 should not re-derive what iterations 1 and 2
// already established, nor go looking for evidence somebody has already
// recorded as missing.
//
// Malformed JSON in a column renders nothing rather than erroring — this
// is best-effort context, and a bad column should cost a line of prompt,
// not a spawn.
func renderIncidentState(inc *entity.AgentIncident) string {
	if inc == nil {
		return ""
	}
	var sb strings.Builder
	if h := decodeJSONList(inc.Hypotheses); len(h) > 0 {
		fmt.Fprintf(&sb, "current hypotheses: %s\n", strings.Join(h, " · "))
	}
	if m := decodeJSONList(inc.MissingEvidence); len(m) > 0 {
		fmt.Fprintf(&sb, "missing evidence: %s\n", strings.Join(m, " · "))
	}
	return sb.String()
}

func renderStateSummary(siblings []entity.AgentDelegation) string {
	lines := make([]string, 0, len(siblings))
	for i := range siblings {
		d := &siblings[i]
		if !entity.IsTerminalDelegationStatus(d.Status) {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s (%s): %s",
			d.ProfileKey, d.Status, truncateRunes(siblingSummary(d), memorySiblingSummaryRunes)))
	}
	if len(lines) == 0 {
		return ""
	}
	return "What the other agents in this conversation have established (snapshot at spawn):\n" +
		strings.Join(lines, "\n") + "\n"
}

func renderFullHistory(siblings []entity.AgentDelegation) string {
	var sb strings.Builder
	for i := range siblings {
		d := &siblings[i]
		if !entity.IsTerminalDelegationStatus(d.Status) {
			continue
		}
		body := strings.TrimSpace(d.Result)
		if body == "" {
			continue
		}
		fmt.Fprintf(&sb, "── %s (%s) ──\n%s\n\n", d.ProfileKey, d.Status, body)
	}
	if sb.Len() == 0 {
		return ""
	}
	return "Everything the other agents in this conversation produced (snapshot at spawn):\n" + sb.String()
}

// siblingSummary prefers the reported summary and falls back to the first
// line of the prose, so a sibling that never called report_result still
// contributes something rather than a blank.
func siblingSummary(d *entity.AgentDelegation) string {
	if env := EnvelopeOf(d); env != nil && strings.TrimSpace(env.Summary) != "" {
		return strings.TrimSpace(env.Summary)
	}
	for _, line := range strings.Split(d.Result, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return "(no output)"
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
