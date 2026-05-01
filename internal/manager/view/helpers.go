package view

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// prettyOrEmpty re-indents a JSON blob for the history detail panel.
// Falls back to the raw text when the input isn't valid JSON, and to a
// dash when it's empty.
func prettyOrEmpty(s string) string {
	if s == "" {
		return "—"
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}

// shortID renders the first 8 characters of an ID — enough to scan a
// list without showing the full UUID.
func shortID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8] + "…"
}

// truncate clips a string to n runes, appending an ellipsis when it
// had to cut. Used to keep User-Agent strings readable in the history
// metadata row.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// relativeTime renders a coarse "x minutes ago" string. The tooltip on
// the cell carries the precise RFC3339 timestamp for callers who need
// exact times.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
