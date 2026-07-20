package main

import (
	"encoding/json"
	"strings"
	"time"
)

// This file turns the private API's raw records into clean values: the "notion
// inline" title format → markdown, the block tree → markdown, and the collection
// schema → row cells. All deterministic — no AI. Plus the setTitle transaction.

// --- notion inline text ("properties.title" and rich cells) ---
//
// Notion stores rich text as an array of segments: [[text, [[fmt,...],...]], ...]
// e.g. [["hello ", []], ["bold", [["b"]]], [" x", [["a","https://…"]]]].
// segment[0] = text, segment[1] = optional array of format ops where op[0] is
// the type ("b" bold, "i" italic, "c" code, "s" strike, "a" link w/ op[1]=url).

func inlineToMarkdown(raw json.RawMessage) string {
	segs := parseInline(raw)
	var b strings.Builder
	for _, s := range segs {
		t := s.text
		if t == "" {
			continue
		}
		for _, f := range s.fmts {
			switch f.kind {
			case "c":
				t = "`" + t + "`"
			case "b":
				t = "**" + t + "**"
			case "i":
				t = "*" + t + "*"
			case "s":
				t = "~~" + t + "~~"
			case "a":
				if f.arg != "" {
					t = "[" + t + "](" + f.arg + ")"
				}
			}
		}
		b.WriteString(t)
	}
	return b.String()
}

func inlineToPlain(raw json.RawMessage) string {
	var b strings.Builder
	for _, s := range parseInline(raw) {
		b.WriteString(s.text)
	}
	return b.String()
}

type inlineSeg struct {
	text string
	fmts []inlineFmt
}
type inlineFmt struct {
	kind string
	arg  string
}

func parseInline(raw json.RawMessage) []inlineSeg {
	if len(raw) == 0 {
		return nil
	}
	var arr [][]json.RawMessage
	if json.Unmarshal(raw, &arr) != nil {
		return nil
	}
	out := make([]inlineSeg, 0, len(arr))
	for _, seg := range arr {
		if len(seg) == 0 {
			continue
		}
		var text string
		_ = json.Unmarshal(seg[0], &text)
		s := inlineSeg{text: text}
		if len(seg) > 1 {
			var fmts [][]json.RawMessage
			if json.Unmarshal(seg[1], &fmts) == nil {
				for _, f := range fmts {
					if len(f) == 0 {
						continue
					}
					var kind string
					_ = json.Unmarshal(f[0], &kind)
					fm := inlineFmt{kind: kind}
					if len(f) > 1 {
						_ = json.Unmarshal(f[1], &fm.arg)
					}
					s.fmts = append(s.fmts, fm)
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// blockTitle returns a block's title as plain text (from properties.title).
func blockTitle(rec map[string]json.RawMessage) string {
	props := recordProps(rec)
	if props == nil {
		return ""
	}
	return inlineToPlain(props["title"])
}

func recordProps(rec map[string]json.RawMessage) map[string]json.RawMessage {
	if rec == nil {
		return nil
	}
	raw, ok := rec["properties"]
	if !ok {
		return nil
	}
	var props map[string]json.RawMessage
	if json.Unmarshal(raw, &props) != nil {
		return nil
	}
	return props
}

// --- block tree → markdown ---

// blocksToMarkdown renders a page/block's children (from its "content" order)
// as markdown, recursing one level via the recordMap already loaded. The private
// loadPageChunk returns the whole chunk's blocks in one recordMap, so nested
// children are resolved from the same map without extra calls.
func blocksToMarkdown(cl *v3Client, rm *recordMap, parent map[string]json.RawMessage, depth int) string {
	ids := contentIDs(parent)
	var b strings.Builder
	for _, id := range ids {
		rec := recordValue(rm.Block[id])
		if rec == nil {
			continue
		}
		b.WriteString(blockToMarkdown(cl, rm, rec, depth))
	}
	return b.String()
}

func contentIDs(rec map[string]json.RawMessage) []string {
	if rec == nil {
		return nil
	}
	raw, ok := rec["content"]
	if !ok {
		return nil
	}
	var ids []string
	_ = json.Unmarshal(raw, &ids)
	return ids
}

func blockToMarkdown(cl *v3Client, rm *recordMap, rec map[string]json.RawMessage, depth int) string {
	typ := strField(rec, "type")
	indent := strings.Repeat("  ", depth)
	props := recordProps(rec)
	title := ""
	if props != nil {
		title = inlineToMarkdown(props["title"])
	}

	var line string
	switch typ {
	case "header":
		line = indent + "# " + title + "\n\n"
	case "sub_header":
		line = indent + "## " + title + "\n\n"
	case "sub_sub_header":
		line = indent + "### " + title + "\n\n"
	case "text":
		if title != "" {
			line = indent + title + "\n\n"
		}
	case "bulleted_list":
		line = indent + "- " + title + "\n"
	case "numbered_list":
		line = indent + "1. " + title + "\n"
	case "to_do":
		box := "[ ]"
		if strings.EqualFold(propPlain(props, "checked"), "yes") {
			box = "[x]"
		}
		line = indent + "- " + box + " " + title + "\n"
	case "quote":
		line = indent + "> " + title + "\n\n"
	case "callout":
		line = indent + "> " + title + "\n\n"
	case "code":
		lang := strings.ToLower(propPlain(props, "language"))
		line = indent + "```" + lang + "\n" + inlineToPlain(props["title"]) + "\n" + indent + "```\n\n"
	case "divider":
		line = indent + "---\n\n"
	case "page":
		line = indent + "- 📄 " + title + "\n"
	case "collection_view", "collection_view_page":
		line = indent + embeddedCollectionMarkdown(cl, rm, rec, indent)
	default:
		if title != "" {
			line = indent + title + "\n\n"
		}
	}

	// Recurse into children (toggles, nested lists, columns).
	if kids := contentIDs(rec); len(kids) > 0 {
		line += blocksToMarkdown(cl, rm, rec, depth+1)
	}
	return line
}

// embeddedCollectionMarkdown expands an inline/linked database (a
// collection_view / collection_view_page block) into a markdown table of its
// rows, instead of a bare placeholder. It reads the block's collection_id +
// first view_id, pulls the schema from the recordMap (already loaded), then
// runs one queryCollection to fetch the rows. Best-effort: on any missing id /
// query error it falls back to a labelled placeholder so fetch never fails
// because of an embedded DB.
func embeddedCollectionMarkdown(cl *v3Client, rm *recordMap, rec map[string]json.RawMessage, indent string) string {
	collID := strField(rec, "collection_id")
	if collID == "" {
		// Newer blocks carry the collection under format.collection_pointer.
		if raw, ok := rec["format"]; ok {
			var f struct {
				CollectionPointer struct {
					ID string `json:"id"`
				} `json:"collection_pointer"`
			}
			if json.Unmarshal(raw, &f) == nil {
				collID = f.CollectionPointer.ID
			}
		}
	}
	viewID := ""
	if raw, ok := rec["view_ids"]; ok {
		var ids []string
		if json.Unmarshal(raw, &ids) == nil && len(ids) > 0 {
			viewID = ids[0]
		}
	}
	// Linked database: the block has no collection_id — the pointer lives on the
	// VIEW record instead (view.format.collection_pointer.id). Resolve it so a
	// linked/embedded view still expands instead of falling back to a placeholder.
	if collID == "" && viewID != "" {
		collID = collectionPointerOfView(rm, viewID)
	}

	// Collection record (schema + name) comes from the same chunk when present.
	coll := recordValue(rm.Collection[collID])
	name := ""
	if coll != nil {
		name = inlineToPlain(coll["name"])
	}
	header := indent + "**🗃️ " + orDefault(name, "Database") + "**\n\n"

	if cl == nil || collID == "" || viewID == "" {
		return header + indent + "_(embedded database — open in Notion)_\n\n"
	}

	// Apply the view's filter + sort so the table matches what the view shows
	// (e.g. a linked inline database filtered by a relation to the host page).
	view := recordValue(rm.CollectionView[viewID])
	rowsRM, rowIDs, err := cl.queryCollection(collID, viewID, 50, viewQueryFromRecord(view))
	if err != nil {
		return header + indent + "_(embedded database — couldn't load rows)_\n\n"
	}
	cl.resolveUsers(rowsRM, rowIDs) // fill created_by / last_edited_by names
	schemaRec := coll
	if len(collectionSchema(schemaRec)) == 0 {
		// queryCollection's own recordMap may carry the collection.
		schemaRec = recordValue(rowsRM.Collection[collID])
	}
	schema := collectionSchema(schemaRec)
	types := collectionSchemaTypes(schemaRec)
	// Restrict + order columns to the view's visible ones when it declares them.
	cols := visibleColumns(view, schema)
	table := collectionTable(rowsRM, rowIDs, schema, types, cols, indent)
	// Footer: DB id + active filter, so an agent knows where these rows live and
	// what a new row must satisfy to appear in this view.
	return header + table + embeddedDBMeta(view, schema, collID, indent)
}

// embeddedDBMeta renders a one-line footer describing the embedded database: its
// collection id and the view's active filter (property name + operator + value).
// This gives an agent enough to add a row that appears here — e.g. "set relation
// <Prop> to <this page>". Empty when there's no filter (still prints the db id).
func embeddedDBMeta(view map[string]json.RawMessage, schema map[string]string, collID, indent string) string {
	parts := []string{"db `" + collID + "`"}
	if f := viewFilterSummary(view, schema); f != "" {
		parts = append(parts, "view filter: "+f)
	}
	return indent + "_(" + strings.Join(parts, " · ") + ")_\n\n"
}

// viewFilterSummary renders a view's property_filters as human/agent-readable
// clauses: "<PropName> <operator> <value>", joined by " AND ". Returns "" when
// the view has no filter.
func viewFilterSummary(view map[string]json.RawMessage, schema map[string]string) string {
	if view == nil {
		return ""
	}
	raw, ok := view["format"]
	if !ok {
		return ""
	}
	var f struct {
		PropertyFilters []struct {
			Filter struct {
				Property string `json:"property"`
				Filter   struct {
					Operator string `json:"operator"`
					Value    struct {
						Value string `json:"value"`
					} `json:"value"`
				} `json:"filter"`
			} `json:"filter"`
		} `json:"property_filters"`
	}
	if json.Unmarshal(raw, &f) != nil || len(f.PropertyFilters) == 0 {
		return ""
	}
	clauses := make([]string, 0, len(f.PropertyFilters))
	for _, pf := range f.PropertyFilters {
		name := schema[pf.Filter.Property]
		if name == "" {
			name = pf.Filter.Property
		}
		val := pf.Filter.Filter.Value.Value
		if len(val) > 12 { // shorten long ids/uuids
			val = shortID(val)
		}
		clauses = append(clauses, name+" "+pf.Filter.Filter.Operator+" "+val)
	}
	return strings.Join(clauses, " AND ")
}

// viewQueryFromRecord builds the filter+sort a collection view applies. The
// filter for a linked/inline view lives in format.property_filters (each entry
// wraps a {property, filter} clause); sort lives in query2.sort. Returns nil
// when the view carries neither, so the caller queries unfiltered.
func viewQueryFromRecord(view map[string]json.RawMessage) *viewQuery {
	if view == nil {
		return nil
	}
	vq := &viewQuery{}

	// property_filters → {operator:"and", filters:[<clause>, ...]}
	if raw, ok := view["format"]; ok {
		var f struct {
			PropertyFilters []struct {
				Filter json.RawMessage `json:"filter"`
			} `json:"property_filters"`
		}
		if json.Unmarshal(raw, &f) == nil && len(f.PropertyFilters) > 0 {
			clauses := make([]json.RawMessage, 0, len(f.PropertyFilters))
			for _, pf := range f.PropertyFilters {
				if len(pf.Filter) > 0 {
					clauses = append(clauses, pf.Filter)
				}
			}
			if len(clauses) > 0 {
				// Assemble {"operator":"and","filters":[...]} without re-modeling
				// each clause (they are already in api/v3 shape).
				var b strings.Builder
				b.WriteString(`{"operator":"and","filters":[`)
				for i, c := range clauses {
					if i > 0 {
						b.WriteByte(',')
					}
					b.Write(c)
				}
				b.WriteString(`]}`)
				vq.Filter = json.RawMessage(b.String())
			}
		}
	}

	// query2.sort passes through as-is.
	if raw, ok := view["query2"]; ok {
		var q struct {
			Sort json.RawMessage `json:"sort"`
		}
		if json.Unmarshal(raw, &q) == nil && len(q.Sort) > 0 {
			vq.Sort = q.Sort
		}
	}

	if len(vq.Filter) == 0 && len(vq.Sort) == 0 {
		return nil
	}
	return vq
}

// visibleColumns returns the column ids a view shows, in the view's order,
// from format.table_properties (visible==true). Falls back to all schema ids
// (title first) when the view declares no table_properties.
func visibleColumns(view map[string]json.RawMessage, schema map[string]string) []string {
	if view != nil {
		if raw, ok := view["format"]; ok {
			var f struct {
				TableProperties []struct {
					Property string `json:"property"`
					Visible  bool   `json:"visible"`
				} `json:"table_properties"`
			}
			if json.Unmarshal(raw, &f) == nil && len(f.TableProperties) > 0 {
				cols := make([]string, 0, len(f.TableProperties))
				for _, p := range f.TableProperties {
					if p.Visible {
						cols = append(cols, p.Property)
					}
				}
				if len(cols) > 0 {
					return cols
				}
			}
		}
	}
	// Fallback: all schema columns, title first.
	return allColumns(schema)
}

// allColumns returns every schema column id, title/name first.
func allColumns(schema map[string]string) []string {
	titleID := ""
	for id, name := range schema {
		if id == "title" || strings.EqualFold(name, "name") {
			titleID = id
			break
		}
	}
	cols := make([]string, 0, len(schema))
	if titleID != "" {
		cols = append(cols, titleID)
	}
	for id := range schema {
		if id != titleID {
			cols = append(cols, id)
		}
	}
	return cols
}

// collectionPointerOfView reads a collection_view record's
// format.collection_pointer.id — the collection a linked/embedded view points
// at when the block itself carries no collection_id. Returns "" if absent.
func collectionPointerOfView(rm *recordMap, viewID string) string {
	view := recordValue(rm.CollectionView[viewID])
	if view == nil {
		return ""
	}
	raw, ok := view["format"]
	if !ok {
		return ""
	}
	var f struct {
		CollectionPointer struct {
			ID string `json:"id"`
		} `json:"collection_pointer"`
	}
	if json.Unmarshal(raw, &f) != nil {
		return ""
	}
	return f.CollectionPointer.ID
}

// collectionTable renders queried rows as a markdown table. colIDs is the
// column order to render (already restricted to the view's visible columns by
// the caller); rows are the block records, bounded by the query limit.
func collectionTable(rm *recordMap, rowIDs []string, schema, types map[string]string, colIDs []string, indent string) string {
	// Drop columns with no schema name (stale/hidden ids) to keep the header clean.
	filtered := colIDs[:0:0]
	for _, id := range colIDs {
		if _, ok := schema[id]; ok || id == "title" {
			filtered = append(filtered, id)
		}
	}
	colIDs = filtered
	if len(colIDs) == 0 {
		return indent + "_(empty database)_\n\n"
	}

	var b strings.Builder
	// header row
	b.WriteString(indent + "| ")
	for _, id := range colIDs {
		b.WriteString(cellText(schema[id]) + " | ")
	}
	b.WriteString("\n" + indent + "|")
	for range colIDs {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	// rows
	for _, rid := range rowIDs {
		row := recordValue(rm.Block[rid])
		if row == nil {
			continue
		}
		props := recordProps(row)
		b.WriteString(indent + "| ")
		for _, id := range colIDs {
			b.WriteString(cellText(cellValue(rm, row, props, id, types[id])) + " | ")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}

// cellText keeps a table cell single-line (markdown tables can't hold raw
// newlines or unescaped pipes).
func cellText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	return strings.TrimSpace(s)
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func propPlain(props map[string]json.RawMessage, key string) string {
	if props == nil {
		return ""
	}
	return inlineToPlain(props[key])
}

// --- collection schema + row cells ---

// collectionSchema maps a column's internal id → its display name. The row
// property keys use these internal ids, so we translate to friendly names.
func collectionSchema(coll map[string]json.RawMessage) map[string]string {
	out := map[string]string{}
	if coll == nil {
		return out
	}
	raw, ok := coll["schema"]
	if !ok {
		return out
	}
	var schema map[string]struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &schema) != nil {
		return out
	}
	for id, col := range schema {
		name := col.Name
		if name == "" {
			name = id
		}
		out[id] = name
	}
	return out
}

// collectionSchemaTypes maps a column id → its Notion property type (title,
// date, created_by, formula, …). Used to render metadata/computed columns that
// aren't stored in a row's properties.
func collectionSchemaTypes(coll map[string]json.RawMessage) map[string]string {
	out := map[string]string{}
	if coll == nil {
		return out
	}
	raw, ok := coll["schema"]
	if !ok {
		return out
	}
	var schema map[string]struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &schema) != nil {
		return out
	}
	for id, col := range schema {
		out[id] = col.Type
	}
	return out
}

// selectOptions returns the option values for a select/multi_select column, so
// describe_database can tell the agent the allowed values. Empty for other types.
func selectOptions(coll map[string]json.RawMessage, colID string) []string {
	if coll == nil {
		return nil
	}
	raw, ok := coll["schema"]
	if !ok {
		return nil
	}
	var schema map[string]struct {
		Options []struct {
			Value string `json:"value"`
		} `json:"options"`
	}
	if json.Unmarshal(raw, &schema) != nil {
		return nil
	}
	col, ok := schema[colID]
	if !ok || len(col.Options) == 0 {
		return nil
	}
	out := make([]string, 0, len(col.Options))
	for _, o := range col.Options {
		out = append(out, o.Value)
	}
	return out
}

// cellValue renders one row cell to text, resolving the value by column type.
// Metadata columns (created_by/created_time/last_edited_*) come from the block's
// own fields, not properties; formula/rollup are computed by Notion and not
// stored, so they render empty. Everything else goes through richCell, which
// resolves date/user/relation mentions embedded in the inline value.
func cellValue(rm *recordMap, block, props map[string]json.RawMessage, colID, colType string) string {
	switch colType {
	case "created_by":
		return userName(rm, strField(block, "created_by_id"))
	case "last_edited_by":
		return userName(rm, strField(block, "last_edited_by_id"))
	case "created_time":
		return msToDate(numField(block, "created_time"))
	case "last_edited_time":
		return msToDate(numField(block, "last_edited_time"))
	case "formula", "rollup":
		// Computed by Notion, not stored on the row. (Would need the query's
		// aggregation results to surface — out of scope here.)
		return ""
	default:
		if props == nil {
			return ""
		}
		return richCell(rm, props[colID])
	}
}

// richCell renders an inline value, resolving the special mention attributes a
// plain-text pass drops: `d` (date) → formatted date, `u` (user) → name,
// `p`/`‣` (page relation) → the linked page's title. Plain segments pass
// through; a lone placeholder glyph with no attribute is skipped.
func richCell(rm *recordMap, raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var arr [][]json.RawMessage
	if json.Unmarshal(raw, &arr) != nil {
		return ""
	}
	var b strings.Builder
	for _, seg := range arr {
		if len(seg) == 0 {
			continue
		}
		var text string
		_ = json.Unmarshal(seg[0], &text)
		if len(seg) > 1 {
			if v := renderMentionAttrs(rm, seg[1]); v != "" {
				b.WriteString(v)
				continue
			}
		}
		if text != "‣" { // skip lone ‣ placeholder
			b.WriteString(text)
		}
	}
	return b.String()
}

// renderMentionAttrs inspects an inline segment's attribute array for a
// date/user/page mention and returns its resolved text, or "" if none.
func renderMentionAttrs(rm *recordMap, rawAttrs json.RawMessage) string {
	var attrs [][]json.RawMessage
	if json.Unmarshal(rawAttrs, &attrs) != nil {
		return ""
	}
	for _, a := range attrs {
		if len(a) == 0 {
			continue
		}
		var kind string
		_ = json.Unmarshal(a[0], &kind)
		switch kind {
		case "d": // date
			if len(a) > 1 {
				return formatDateAttr(a[1])
			}
		case "u": // user mention
			if len(a) > 1 {
				var uid string
				_ = json.Unmarshal(a[1], &uid)
				return userName(rm, uid)
			}
		case "p": // page mention / relation
			if len(a) > 1 {
				var pid string
				_ = json.Unmarshal(a[1], &pid)
				if title := blockTitle(recordValue(rm.Block[pid])); title != "" {
					return title
				}
				return "↗ " + shortID(pid)
			}
		}
	}
	return ""
}

// formatDateAttr renders a Notion date attribute: "2026-07-16 13:23" (start),
// with " → end" when it's a range.
func formatDateAttr(raw json.RawMessage) string {
	var d struct {
		StartDate string `json:"start_date"`
		StartTime string `json:"start_time"`
		EndDate   string `json:"end_date"`
		EndTime   string `json:"end_time"`
	}
	if json.Unmarshal(raw, &d) != nil || d.StartDate == "" {
		return ""
	}
	one := func(date, tm string) string {
		if tm != "" {
			return date + " " + tm
		}
		return date
	}
	out := one(d.StartDate, d.StartTime)
	if d.EndDate != "" {
		out += " → " + one(d.EndDate, d.EndTime)
	}
	return out
}

// userName resolves a notion_user id to a display name (or a short id).
func userName(rm *recordMap, uid string) string {
	if uid == "" {
		return ""
	}
	u := recordValue(rm.NotionUser[uid])
	if u == nil {
		return shortID(uid)
	}
	if n := strField(u, "name"); n != "" {
		return n
	}
	full := strings.TrimSpace(strField(u, "given_name") + " " + strField(u, "family_name"))
	if full != "" {
		return full
	}
	if e := strField(u, "email"); e != "" {
		return e
	}
	return shortID(uid)
}

// numField reads a numeric block field (ms timestamps) as int64.
func numField(rec map[string]json.RawMessage, key string) int64 {
	if rec == nil {
		return 0
	}
	raw, ok := rec[key]
	if !ok {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	return 0
}

// msToDate formats a Unix-ms timestamp as "2006-01-02 15:04" (UTC). 0 → "".
func msToDate(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04")
}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// rowCells maps each schema column → its rendered value, keyed by the column's
// friendly name. Resolves dates/people/relations and metadata columns via
// cellValue (rm needed to resolve user/page mentions).
func rowCells(rm *recordMap, rec map[string]json.RawMessage, schema, types map[string]string) map[string]any {
	cells := map[string]any{}
	props := recordProps(rec)
	for colID, name := range schema {
		if name == "" {
			name = colID
		}
		v := cellValue(rm, rec, props, colID, types[colID])
		if v != "" {
			cells[name] = v
		}
	}
	return cells
}

// --- writing property values ---

// mentionGlyph is the placeholder char Notion uses for a date/user/page mention
// inside an inline value. It MUST be U+2023 (‣) — Notion keys the cell's display
// off this glyph; any other char (e.g. "?") stores the attribute but renders
// blank in the app. (This exact bug cost real debugging: a shell-mangled "?"
// wrote dates the API accepted but the UI never showed.)
const mentionGlyph = "‣"

// formatProperty converts a plain string value into the api/v3 inline value for
// a property of the given Notion type. Returns settable=false for computed /
// read-only types (formula, rollup, created_*, last_edited_*, button) that a row
// write must skip. Value conventions (all strings, since inputs are strings):
//   - select:        the exact option value
//   - multi_select:  comma-separated option values
//   - checkbox:      "true"/"yes"/"1" → Yes, else No
//   - date:          "YYYY-MM-DD" or "YYYY-MM-DD HH:MM" (optionally " → end")
//   - relation:      comma-separated page ids
//   - person:        comma-separated user ids
//   - title/text/…:  the text as-is
func formatProperty(typ, value string) (json.RawMessage, bool) {
	switch typ {
	case "formula", "rollup", "created_time", "created_by",
		"last_edited_time", "last_edited_by", "button":
		return nil, false // computed / read-only

	case "checkbox":
		on := false
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "yes", "1", "on", "checked":
			on = true
		}
		if on {
			return jsonInline("Yes"), true
		}
		return jsonInline("No"), true

	case "date":
		if d := notionDateValue(value); d != nil {
			return d, true
		}
		return jsonInline(""), true

	case "relation":
		return mentionList("p", splitCSV(value)), true

	case "person":
		return mentionList("u", splitCSV(value)), true

	case "multi_select":
		// stored as one segment of comma-joined option values
		parts := splitCSV(value)
		return jsonInline(strings.Join(parts, ",")), true

	default: // title, text, select, number, url, email, phone_number, status, …
		return jsonInline(value), true
	}
}

// jsonInline builds a plain single-segment inline value [["<text>"]].
func jsonInline(text string) json.RawMessage {
	b, _ := json.Marshal([][]any{{text}})
	return b
}

// mentionList builds an inline value of one-or-more mentions of a kind ("p"
// relation / page, "u" user), comma-separated: [[‣,[[kind,id]]],[","],[‣,...]].
func mentionList(kind string, ids []string) json.RawMessage {
	segs := make([]any, 0, len(ids)*2)
	for i, id := range ids {
		if i > 0 {
			segs = append(segs, []any{","})
		}
		segs = append(segs, []any{mentionGlyph, []any{[]any{kind, id}}})
	}
	if len(segs) == 0 {
		segs = append(segs, []any{""})
	}
	b, _ := json.Marshal(segs)
	return b
}

// notionDateValue parses "YYYY-MM-DD", "YYYY-MM-DD HH:MM", or a range
// "start → end" into a Notion date inline value. Returns nil on parse failure.
func notionDateValue(value string) json.RawMessage {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	start, end := value, ""
	for _, sep := range []string{" → ", " - ", " — "} {
		if i := strings.Index(value, sep); i >= 0 {
			start, end = strings.TrimSpace(value[:i]), strings.TrimSpace(value[i+len(sep):])
			break
		}
	}
	sd, st := splitDateTime(start)
	if sd == "" {
		return nil
	}
	d := map[string]any{"time_zone": "Asia/Jakarta", "start_date": sd}
	if st != "" {
		d["type"] = "datetime"
		d["start_date"] = sd
		d["start_time"] = st
	} else {
		d["type"] = "date"
	}
	if end != "" {
		ed, et := splitDateTime(end)
		if ed != "" {
			d["end_date"] = ed
			if et != "" {
				d["end_time"] = et
				d["type"] = "datetimerange"
			} else {
				d["type"] = "daterange"
			}
		}
	}
	b, _ := json.Marshal([]any{[]any{mentionGlyph, []any{[]any{"d", d}}}})
	return b
}

// splitDateTime splits "2026-07-17 06:00" → ("2026-07-17","06:00"); a bare date
// returns ("2026-07-17",""). Accepts "T" separator too.
func splitDateTime(s string) (date, tm string) {
	s = strings.TrimSpace(s)
	for _, sep := range []string{" ", "T"} {
		if i := strings.Index(s, sep); i > 0 {
			return strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+1:])
		}
	}
	return s, ""
}

// --- write ops (saveTransactions) ---

// setTitle sets a page's title (one set op on properties.title). Title is stored
// in the "notion inline" shape [[text]].
func (cl *v3Client) setTitle(pageID, title string) error {
	spaceID, _, err := cl.identity()
	if err != nil {
		return err
	}
	ops := []map[string]any{
		op("block", pageID, spaceID, []any{"properties", "title"}, "set", [][]any{{title}}),
	}
	return cl.saveTransactions(spaceID, ops)
}

// propSet is a resolved property write: the schema column id + its formatted
// api/v3 inline value.
type propSet struct {
	ID    string
	Value json.RawMessage
}

// createPage creates a page as a child of parentID (a page block or a
// collection). extraProps are additional property writes (resolved + formatted
// by the caller, e.g. a database row's date/select/relation cells). Returns the
// new page id. Sequence (verified live):
//  1. set the new block as type=page with parent + audit fields,
//  2. set its title + any extra properties,
//  3. list it after the parent's content (page parent) — a collection parent
//     needs no listAfter (rows are found via query).
func (cl *v3Client) createPage(parentID, parentTable, title string, extraProps []propSet) (string, error) {
	spaceID, userID, err := cl.identity()
	if err != nil {
		return "", err
	}
	newID := newUUID()
	now := nowMillis()

	ops := []map[string]any{
		op("block", newID, spaceID, []any{}, "set", map[string]any{
			"type":             "page",
			"id":               newID,
			"version":          1,
			"parent_id":        parentID,
			"parent_table":     parentTable,
			"alive":            true,
			"space_id":         spaceID,
			"created_by_id":    userID,
			"created_by_table": "notion_user",
			"created_time":     now,
		}),
		op("block", newID, spaceID, []any{"properties", "title"}, "set", [][]any{{title}}),
	}
	for _, p := range extraProps {
		ops = append(ops, op("block", newID, spaceID, []any{"properties", p.ID}, "set", p.Value))
	}
	if parentTable == "block" {
		ops = append(ops, op("block", parentID, spaceID, []any{"content"}, "listAfter", map[string]any{"id": newID}))
	}
	if err := cl.saveTransactions(spaceID, ops); err != nil {
		return "", err
	}
	return newID, nil
}

// fetchCollectionSchema loads a collection and returns its column name→id,
// id→type maps. Used to resolve property names an agent supplies to the internal
// ids + types a write needs.
func (cl *v3Client) fetchCollectionSchema(collID string) (nameToID, idToType map[string]string, err error) {
	rm, err := cl.syncRecords("collection", []string{collID})
	if err != nil {
		return nil, nil, err
	}
	coll := recordValue(rm.Collection[collID])
	idToName := collectionSchema(coll)
	idToType = collectionSchemaTypes(coll)
	nameToID = make(map[string]string, len(idToName))
	for id, name := range idToName {
		nameToID[name] = id
	}
	return nameToID, idToType, nil
}

// resolveProps maps an agent-supplied {property name → string value} to the
// resolved+formatted property writes. Unknown names and computed/read-only
// types are skipped (returned in `skipped` for feedback). "Name"/"title" is
// handled by createPage's title op, so it's ignored here.
func resolveProps(props map[string]string, nameToID, idToType map[string]string) (sets []propSet, skipped []string) {
	for name, value := range props {
		if strings.EqualFold(name, "name") || strings.EqualFold(name, "title") {
			continue
		}
		id, ok := nameToID[name]
		if !ok {
			skipped = append(skipped, name+" (no such property)")
			continue
		}
		v, settable := formatProperty(idToType[id], value)
		if !settable {
			skipped = append(skipped, name+" ("+idToType[id]+" is read-only)")
			continue
		}
		sets = append(sets, propSet{ID: id, Value: v})
	}
	return sets, skipped
}

// createComment adds a page-level comment: it creates a discussion + a comment
// and lists the discussion on the target block (a page or a row-page).
// Returns {commentID, discussionID}. Sequence verified live (comment needs the
// alive field or Postgres rejects it).
func (cl *v3Client) createComment(pageID, text string) (commentID, discussionID string, err error) {
	spaceID, userID, err := cl.identity()
	if err != nil {
		return "", "", err
	}
	discID := newUUID()
	comID := newUUID()
	now := nowMillis()

	ops := []map[string]any{
		op("discussion", discID, spaceID, []any{}, "set", map[string]any{
			"id":           discID,
			"version":      1,
			"parent_id":    pageID,
			"parent_table": "block",
			"resolved":     false,
			"space_id":     spaceID,
			"comments":     []string{comID},
		}),
		op("comment", comID, spaceID, []any{}, "set", map[string]any{
			"id":               comID,
			"version":          1,
			"parent_id":        discID,
			"parent_table":     "discussion",
			"text":             [][]any{{text}},
			"alive":            true,
			"space_id":         spaceID,
			"created_by_id":    userID,
			"created_by_table": "notion_user",
			"created_time":     now,
		}),
		op("block", pageID, spaceID, []any{"discussions"}, "listAfter", map[string]any{"id": discID}),
	}
	if err := cl.saveTransactions(spaceID, ops); err != nil {
		return "", "", err
	}
	return comID, discID, nil
}

// --- id helpers ---

// normalizeID accepts a dashed UUID, a bare 32-char id, or a Notion URL and
// returns the dashed UUID the private API expects.
func normalizeID(in string) string {
	s := strings.TrimSpace(in)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "notion.") || strings.HasPrefix(s, "http") {
		if i := strings.LastIndexAny(s, "/-"); i >= 0 {
			s = s[i+1:]
		}
	}
	s = strings.ReplaceAll(s, "-", "")
	if q := strings.IndexAny(s, "?#"); q >= 0 {
		s = s[:q]
	}
	if len(s) != 32 || !isHex(s) {
		return strings.TrimSpace(in)
	}
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

func isHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
