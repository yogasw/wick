package strutil

const DefaultLimit = 10_000

// LimitText truncates s to max bytes, appending a marker when cut.
func LimitText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + " ...[truncated]"
}
