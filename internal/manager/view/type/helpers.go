package fieldtype

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// valueFor returns the value to pre-fill in the edit input. Secret
// values are never disclosed — the Secret widget uses its own empty
// default and does not call this.
func valueFor(v entity.Config) string {
	if v.IsSecret {
		return ""
	}
	return v.Value
}

func placeholderFor(v entity.Config) string {
	if v.IsSecret {
		return "Enter new value (current value is hidden)"
	}
	return ""
}

// dropdownOptions splits the pipe-separated Options column into a
// slice. Empty Options returns nil so the template renders an empty
// <select> without crashing.
func dropdownOptions(v entity.Config) []string {
	if v.Options == "" {
		return nil
	}
	return strings.Split(v.Options, "|")
}

// kvlistColumns returns the column names encoded in Options for a
// kvlist config. Falls back to ["value"] when Options is empty.
func kvlistColumns(v entity.Config) []string {
	if v.Options == "" {
		return []string{"value"}
	}
	return strings.Split(v.Options, "|")
}

// kvlistRows parses the JSON-array Value of a kvlist config into a
// slice of string maps. Returns nil on empty or malformed input.
func kvlistRows(v entity.Config) []map[string]string {
	if v.Value == "" {
		return nil
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(v.Value), &rows); err != nil {
		return nil
	}
	return rows
}

// KVListSummary returns a compact human-readable summary of a kvlist
// value: "N entries: col1:col2, ..." (first 3 rows, then "+N more").
func KVListSummary(v entity.Config) string {
	rows := kvlistRows(v)
	if len(rows) == 0 {
		return ""
	}
	cols := kvlistColumns(v)
	limit := len(rows)
	if limit > 3 {
		limit = 3
	}
	parts := make([]string, 0, limit)
	for _, row := range rows[:limit] {
		vals := make([]string, 0, len(cols))
		for _, col := range cols {
			vals = append(vals, row[col])
		}
		parts = append(parts, strings.Join(vals, ":"))
	}
	summary := strings.Join(parts, " · ")
	if len(rows) > 3 {
		summary += fmt.Sprintf(" +%s more", strconv.Itoa(len(rows)-3))
	}
	return fmt.Sprintf("%d entries: %s", len(rows), summary)
}
