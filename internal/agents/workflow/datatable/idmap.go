// Package datatable — name↔id translation between caller-facing column
// names and storage-layer column ids.
//
// Storage uses immutable `cN` ids as JSONB keys so renames touch only
// the schema row, never the data. Every read translates ids back to
// names before returning to the caller, and every write translates
// names to ids before persistence. Both happen through a single
// IDMap built once per request from the live Schema.
package datatable

import (
	"fmt"
)

// IDMap is the materialised translation layer for one Schema.
//
// Construct once per request with BuildIDMap and reuse for every row
// Encode/Decode in that request. Cheap to build (single pass over
// Schema.Columns), so callers should not bother caching across
// requests — the cache invalidation on schema change is trickier than
// the rebuild itself.
type IDMap struct {
	NameToID map[string]string  // "priority" → "c2"
	IDToName map[string]string  // "c2" → "priority"
	Cols     map[string]Column  // "c2" → {ID:"c2", Name:"priority", Type:"int", ...}
}

// BuildIDMap walks the schema once and produces the bidirectional
// translation map. System columns are skipped — they live outside
// Data (id from row PK, timestamps from row columns).
func BuildIDMap(sc Schema) IDMap {
	m := IDMap{
		NameToID: make(map[string]string, len(sc.Columns)),
		IDToName: make(map[string]string, len(sc.Columns)),
		Cols:     make(map[string]Column, len(sc.Columns)),
	}
	for _, c := range sc.Columns {
		if c.System || c.ID == "" {
			continue
		}
		m.NameToID[c.Name] = c.ID
		m.IDToName[c.ID] = c.Name
		m.Cols[c.ID] = c
	}
	return m
}

// Encode translates a caller-supplied row (keyed by user-facing name)
// into the storage shape (keyed by column id).
//
// Strict mode rejects unknown names with ErrSchemaMismatch.
// Lax mode passes them through under their original name so ad-hoc
// data survives until the user formalises a schema for it. System
// columns (id/created_at/updated_at) are dropped — the storage layer
// owns them.
func (m IDMap) Encode(row map[string]any, mode string) (map[string]any, error) {
	out := make(map[string]any, len(row))
	for name, v := range row {
		if IsSystemColumn(name) {
			continue
		}
		if cid, ok := m.NameToID[name]; ok {
			out[cid] = v
			continue
		}
		if mode == ModeLax {
			out[name] = v
			continue
		}
		return nil, fmt.Errorf("%w: unknown column %q", ErrSchemaMismatch, name)
	}
	return out, nil
}

// Decode translates a storage row (keyed by column id) back into the
// caller-facing shape (keyed by user-facing name). Orphan keys — ids
// from columns that have since been dropped — are skipped silently;
// any non-cN keys (lax-mode extras) are passed through.
func (m IDMap) Decode(stored map[string]any) map[string]any {
	out := make(map[string]any, len(stored))
	for k, v := range stored {
		if name, ok := m.IDToName[k]; ok {
			out[name] = v
			continue
		}
		// Not a known id. Keep the key verbatim if it doesn't look
		// like a column id (so lax-mode user keys survive); silently
		// drop orphan cN ids from dropped columns.
		if !looksLikeColumnID(k) {
			out[k] = v
		}
	}
	return out
}

// NextColumnID bumps the schema's monotonic counter and returns the
// freshly-allocated id. Caller wraps the surrounding read-modify-write
// in a TX so concurrent column adds never collide.
func NextColumnID(sc *Schema) string {
	sc.NextColID++
	return fmt.Sprintf("c%d", sc.NextColID)
}

// looksLikeColumnID reports whether a key matches the cN format used
// for storage ids. Used by Decode to silently drop orphan ids while
// preserving lax-mode user keys.
func looksLikeColumnID(k string) bool {
	if len(k) < 2 || k[0] != 'c' {
		return false
	}
	for i := 1; i < len(k); i++ {
		if k[i] < '0' || k[i] > '9' {
			return false
		}
	}
	return true
}
