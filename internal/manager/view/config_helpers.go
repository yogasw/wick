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

// fieldGroup is one rendered card under a section title. A group holds the
// simple fields (rendered as a 2-col grid) plus any picker / kvlist fields
// that share the same group= tag, rendered as sub-blocks inside the same
// card so a dependent picker (e.g. reaction_channels) sits with its toggle
// instead of being orphaned at the bottom of the page.
//
// Desc is an optional one-time blurb shown under the heading so the shared
// context lives on the group instead of being repeated on every field.
type fieldGroup struct {
	Title     string
	Desc      string
	Collapsed bool // card starts collapsed (group=Title|Desc|collapsed)
	Simple    []entity.Config
	Pickers   []entity.Config
	KvLists   []entity.Config
}

// defaultGroupTitle is the heading used for rows that declare no
// `wick:"group=..."`. Matches the legacy single-card title so an
// ungrouped config page looks exactly as it did before grouping.
const defaultGroupTitle = "Configuration"

// parseGroup splits a `wick:"group=..."` value into a section title, an
// optional description, and an optional collapsed flag. Grammar:
//
//	"Title"                     — card, always expanded
//	"Title|Description"         — card with a shared blurb
//	"Title|Description|collapsed" — card that starts collapsed (click to expand)
//	"Title||collapsed"          — collapsed, no description
//
// The description is the place to write the context shared by every field in
// the group, so individual fields need not repeat it. Any 3rd segment other
// than "collapsed" is ignored (treated as expanded). An empty value maps to
// the default heading, expanded, with no description.
func parseGroup(raw string) (title, desc string, collapsed bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultGroupTitle, "", false
	}
	parts := strings.SplitN(raw, "|", 3)
	title = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		desc = strings.TrimSpace(parts[1])
	}
	if len(parts) > 2 {
		collapsed = strings.EqualFold(strings.TrimSpace(parts[2]), "collapsed")
	}
	return title, desc, collapsed
}

// groupRows partitions ALL rows (simple, picker, kvlist) into cards by their
// Group tag, preserving first-seen group order so the page layout is stable
// and authoring-order-driven. Within a group, each field type lands in its
// own slot so the templ can render simple fields as a grid and picker/kvlist
// fields as sub-blocks under them. Rows with an empty Group collapse into
// the default "Configuration" card at its first-seen position. The first
// non-empty description seen for a group wins.
func groupRows(rows []entity.Config) []fieldGroup {
	idx := map[string]int{}
	var out []fieldGroup
	for _, r := range rows {
		title, desc, collapsed := parseGroup(r.Group)
		i, ok := idx[title]
		if !ok {
			i = len(out)
			idx[title] = i
			out = append(out, fieldGroup{Title: title, Desc: desc, Collapsed: collapsed})
		} else if out[i].Desc == "" && desc != "" {
			out[i].Desc = desc
		}
		switch r.Type {
		case "picker":
			out[i].Pickers = append(out[i].Pickers, r)
		case "kvlist":
			out[i].KvLists = append(out[i].KvLists, r)
		default:
			out[i].Simple = append(out[i].Simple, r)
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
