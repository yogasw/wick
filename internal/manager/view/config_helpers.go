package view

import (
	"encoding/json"
	"strings"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/connector"
)

// HasHealthCheck reports whether a connector module registered a
// HealthCheck hook. The detail page renders the "Check Permissions"
// button only when this is true so connectors without a hook do not
// expose a no-op action.
func HasHealthCheck(mod connector.Module) bool {
	return mod.HealthCheck != nil
}

// descText turns a config Description into display text with real line
// breaks. Go struct tags can't carry literal newlines, so a wick:"desc=…"
// uses the two-character escape `\n` to mark a break; this expands it to
// a real newline. The desc <p> uses `whitespace-pre-line` so those
// newlines render as separate lines. Authors split a long description
// into "what it is" + "per-option meaning" so the dropdown help reads as
// a short list, not one run-on paragraph.
func descText(s string) string {
	return strings.ReplaceAll(s, `\n`, "\n")
}

// cfgInputClass is the shared Tailwind class string for all block-form
// text-like inputs (text, email, url, number, date, etc.).
const cfgInputClass = "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"

// splitSimple returns configs whose Type is neither "kvlist" nor "picker".
// Callers are responsible for filtering hidden rows before passing — this
// function renders whatever it receives.
func splitSimple(rows []entity.Config) []entity.Config {
	out := make([]entity.Config, 0, len(rows))
	for _, r := range rows {
		if r.Type != "kvlist" && r.Type != "picker" {
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

// splitPicker returns only configs with Type == "picker".
func splitPicker(rows []entity.Config) []entity.Config {
	out := make([]entity.Config, 0)
	for _, r := range rows {
		if r.Type == "picker" {
			out = append(out, r)
		}
	}
	return out
}

// pickerRows parses the JSON-array Value of a picker config into
// [{id,name},...] entries. Returns nil on empty or malformed input.
func pickerRows(row entity.Config) []map[string]string {
	if row.Value == "" {
		return nil
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(row.Value), &rows); err != nil {
		return nil
	}
	return rows
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
