package view

import (
	"encoding/json"
	"net/url"
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

// strings_Join is a templ-friendly alias for strings.Join. Templ does
// not import packages used only inside `{ ... }` expressions, so going
// through a same-package helper keeps the template clean.
func strings_Join(parts []string, sep string) string { return strings.Join(parts, sep) }

// HealthBanner is the data the row detail page renders right above the
// Operations section after a health-check round-trip. Kind picks the
// styling; the three slices show op transitions and granular errors.
type HealthBanner struct {
	Kind         string // "ok" | "err" — empty means no banner
	ErrorMessage string
	NewlyLocked  []string
	NewlyCleared []string
}

// HealthBannerFromQuery decodes the redirect query params runConnectorHealthCheck
// stamps onto the detail-page URL. Returns a zero-value banner (no
// render) when the page was opened without health-check params.
func HealthBannerFromQuery(q url.Values) HealthBanner {
	if msg := q.Get("health_err"); msg != "" {
		return HealthBanner{Kind: "err", ErrorMessage: msg}
	}
	if q.Get("health_ok") == "" {
		return HealthBanner{}
	}
	b := HealthBanner{Kind: "ok"}
	if v := q.Get("health_locked"); v != "" {
		b.NewlyLocked = strings.Split(v, ",")
	}
	if v := q.Get("health_cleared"); v != "" {
		b.NewlyCleared = strings.Split(v, ",")
	}
	return b
}

// cfgInputClass is the shared Tailwind class string for all block-form
// text-like inputs (text, email, url, number, date, etc.).
const cfgInputClass = "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-sm font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"

// splitSimple returns configs whose Type is neither "kvlist" nor "picker".
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
