package delegation

import (
	"fmt"
	"strconv"
	"strings"
)

// Token budget (Phase 6).
//
// None of the systems surveyed before building this had a token cap:
// they measured usage for billing, but nothing stopped a loop from
// spending without limit. Turn counting bounds how MANY times an agent
// runs, not how much each run costs — a single turn that reads a huge
// file can cost more than ten small ones. So spend is capped too.
//
// Everything here degrades safely: a provider that reports no usage
// yields 0, which reads as "unknown", never as "free". A cap can only be
// enforced against numbers a provider actually reports.

// UsageReport is one provider's token accounting for a turn.
type UsageReport struct {
	InputTokens  int
	OutputTokens int
}

// Total is the combined token count.
func (u UsageReport) Total() int { return u.InputTokens + u.OutputTokens }

// Empty reports whether the provider told us nothing.
func (u UsageReport) Empty() bool { return u.InputTokens == 0 && u.OutputTokens == 0 }

// approxCostPerMillionUSD is a deliberately rough blended rate used only
// for an at-a-glance "this tree is expensive" signal in the monitor.
//
// It is NOT billing. Real per-model pricing changes often and varies by
// provider, and baking a pricing table into the binary would go stale
// silently — worse than an obvious approximation.
const approxCostPerMillionUSD = 6.0

// estimateCostUSD converts a token count into a rough dollar figure.
func estimateCostUSD(tokens int) float64 {
	if tokens <= 0 {
		return 0
	}
	return (float64(tokens) / 1_000_000.0) * approxCostPerMillionUSD
}

// formatCostUSD renders an estimate for display. Sub-cent totals show as
// "<$0.01" rather than "$0.00", which would read as free.
func formatCostUSD(v float64) string {
	if v <= 0 {
		return ""
	}
	if v < 0.01 {
		return "<$0.01"
	}
	return "$" + strconv.FormatFloat(v, 'f', 2, 64)
}

// EstimateCostUSD is the exported estimate used by the monitor.
func EstimateCostUSD(tokens int) float64 { return estimateCostUSD(tokens) }

// FormatCostUSD is the exported formatter used by the monitor.
func FormatCostUSD(v float64) string { return formatCostUSD(v) }

// ParseUsage pulls token counts out of a raw provider stream line.
//
// Providers disagree on shape, so this matches the common key names
// rather than binding to one vendor's schema:
//
//	input_tokens / prompt_tokens / inputTokens
//	output_tokens / completion_tokens / outputTokens
//
// Returns an empty report when the line carries no usage, which is the
// normal case for most lines.
func ParseUsage(raw string) UsageReport {
	// Case-insensitive fast reject: camelCase providers spell it
	// "inputTokens", and a case-sensitive check here would silently
	// report zero tokens for them — which reads as "free" and would let
	// them slip past every spend cap.
	if raw == "" || !strings.Contains(strings.ToLower(raw), "tokens") {
		return UsageReport{}
	}
	return UsageReport{
		InputTokens:  firstIntField(raw, "input_tokens", "prompt_tokens", "inputTokens", "promptTokens"),
		OutputTokens: firstIntField(raw, "output_tokens", "completion_tokens", "outputTokens", "completionTokens"),
	}
}

// firstIntField scans raw JSON text for the first of keys that carries an
// integer value. A tolerant scan rather than a full unmarshal: the raw
// line shape differs per provider and a strict decode would drop usage
// we could otherwise read.
func firstIntField(raw string, keys ...string) int {
	for _, k := range keys {
		needle := `"` + k + `"`
		i := strings.Index(raw, needle)
		if i < 0 {
			continue
		}
		rest := raw[i+len(needle):]
		// Skip past the colon and any whitespace.
		j := 0
		for j < len(rest) && (rest[j] == ':' || rest[j] == ' ') {
			j++
		}
		start := j
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if j > start {
			if n, err := strconv.Atoi(rest[start:j]); err == nil {
				return n
			}
		}
	}
	return 0
}

// TokenBudgetNote renders the message a leader sees when a delegation is
// cut short on spend.
func TokenBudgetNote(used, cap int) string {
	return fmt.Sprintf(
		"Stopped after using %d tokens (cap %d). The result above is partial.",
		used, cap)
}
