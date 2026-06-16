package systemprompt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/connectors"
)

// connectorCatalogHeader is the section preamble appended above the
// runtime list of connector keys + descriptions.
//
// The "MUST route via wick" rule lives in immutable.md
// where it can't be undone by presets or operator edits. This header
// stays minimal — it only seeds the discovery hint so the agent jumps
// straight to wick_get for whichever key it wants instead of burning
// a turn on wick_list / wick_search.
const connectorCatalogHeader = `## Available wick connectors

Routing rules for these are in the immutable section above. To get
the full operation list and input schemas for any key below, call
wick_get "<key>" — wick_list / wick_search are unnecessary for a
cold-start discovery pass.

`

// ConnectorCatalog renders the registered connectors as a markdown
// bullet list. Empty registry (or filtered-empty result) returns ""
// so callers can drop the whole section cleanly when nothing matches.
//
// readyKeys filters the output:
//   - nil          → show every registered connector definition.
//   - non-nil      → show only the ones whose Meta.Key is present in
//     the map (typical use: pass the keys of connector instances with
//     status="ready" so a model never sees a connector the operator
//     hasn't finished configuring).
//
// The list is built at call time so a connector registered later in
// boot (e.g. wickmanager, added after runtime services are wired)
// still shows up without forcing a static rebuild.
func ConnectorCatalog(readyKeys map[string]bool) string {
	mods := connectors.All()
	if len(mods) == 0 {
		return ""
	}
	type row struct {
		key  string
		desc string
	}
	rows := make([]row, 0, len(mods))
	maxKeyLen := 0
	for _, m := range mods {
		key := strings.TrimSpace(m.Meta.Key)
		if key == "" {
			continue
		}
		if readyKeys != nil && !readyKeys[key] {
			continue
		}
		if len(key) > maxKeyLen {
			maxKeyLen = len(key)
		}
		rows = append(rows, row{key: key, desc: strings.TrimSpace(m.Meta.Description)})
	}
	if len(rows) == 0 {
		return ""
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].key < rows[j].key })

	var b strings.Builder
	b.WriteString(connectorCatalogHeader)
	for _, r := range rows {
		if r.desc == "" {
			b.WriteString(fmt.Sprintf("- %-*s\n", maxKeyLen, r.key))
			continue
		}
		b.WriteString(fmt.Sprintf("- %-*s — %s\n", maxKeyLen, r.key, r.desc))
	}
	return b.String()
}
