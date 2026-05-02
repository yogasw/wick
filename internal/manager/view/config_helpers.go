package view

import (
	"encoding/json"
	"strings"

	"github.com/yogasw/wick/internal/entity"
)

// cfgInputClass is the shared Tailwind class string for all block-form
// text-like inputs (text, email, url, number, date, etc.).
const cfgInputClass = "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"

// splitSimple returns configs whose Type is not "kvlist".
func splitSimple(rows []entity.Config) []entity.Config {
	out := make([]entity.Config, 0, len(rows))
	for _, r := range rows {
		if r.Type != "kvlist" {
			out = append(out, r)
		}
	}
	return out
}

// splitKVList returns only configs with Type == "kvlist".
func splitKVList(rows []entity.Config) []entity.Config {
	out := make([]entity.Config, 0)
	for _, r := range rows {
		if r.Type == "kvlist" {
			out = append(out, r)
		}
	}
	return out
}

// cfgKVCols returns the column names for a kvlist config (from Options).
func cfgKVCols(row entity.Config) []string {
	if row.Options == "" {
		return []string{"value"}
	}
	return strings.Split(row.Options, "|")
}

// cfgKVRows parses the JSON-array Value of a kvlist config.
func cfgKVRows(row entity.Config) []map[string]string {
	if row.Value == "" {
		return nil
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(row.Value), &rows); err != nil {
		return nil
	}
	return rows
}
