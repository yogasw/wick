package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// --- op handlers ---

func fetch(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("page_id"))
	if id == "" {
		return nil, errors.New("page_id is required")
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}
	rm, err := cl.loadPageChunk(id)
	if err != nil {
		return nil, err
	}

	root := recordValue(rm.Block[id])
	if root == nil {
		return nil, errors.New("page not found or not accessible with this token")
	}

	// content: render the root page's child blocks (order in "content") as markdown.
	md := blocksToMarkdown(cl, rm, root, 0)
	return map[string]any{
		"id":         id,
		"title":      blockTitle(root),
		"content_md": strings.TrimSpace(md),
	}, nil
}

func queryDatabase(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("page_id"))
	if id == "" {
		return nil, errors.New("page_id is required")
	}
	limit := c.InputInt("limit")
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}

	// Step 1: load the DB page to find its collection + a collection view.
	pageRM, err := cl.loadPageChunk(id)
	if err != nil {
		return nil, err
	}
	collID := firstKey(pageRM.Collection)
	viewID := firstKey(pageRM.CollectionView)
	if collID == "" || viewID == "" {
		return nil, errors.New("no database (collection) found on this page — is the id a database page?")
	}

	// Step 2: read the schema (column id → name + type) from the collection record.
	collRec := recordValue(pageRM.Collection[collID])
	schema := collectionSchema(collRec)
	types := collectionSchemaTypes(collRec)

	// Step 3: query the collection for row ids, then resolve each row's block.
	// Apply the view's filter/sort so the result matches what the view shows.
	rowsRM, rowIDs, err := cl.queryCollection(collID, viewID, limit, viewQueryFromRecord(recordValue(pageRM.CollectionView[viewID])))
	if err != nil {
		return nil, err
	}
	cl.resolveUsers(rowsRM, rowIDs) // fill created_by / last_edited_by names
	rows := make([]map[string]any, 0, len(rowIDs))
	for _, rid := range rowIDs {
		if len(rows) >= limit {
			break
		}
		rec := recordValue(rowsRM.Block[rid])
		if rec == nil {
			continue
		}
		cells := rowCells(rowsRM, rec, schema, types)
		rows = append(rows, map[string]any{
			"id":    rid,
			"title": blockTitle(rec),
			"cells": cells,
		})
	}
	return map[string]any{"count": len(rows), "rows": rows}, nil
}

func getRecords(c *connector.Ctx) (any, error) {
	raw := strings.TrimSpace(c.Input("ids"))
	if raw == "" {
		return nil, errors.New("ids is required")
	}
	ids := splitCSV(raw)
	if len(ids) == 0 {
		return nil, errors.New("no valid ids provided")
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}
	rm, err := cl.syncRecordValues(ids)
	if err != nil {
		return nil, err
	}
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		rec := recordValue(rm.Block[normalizeID(id)])
		if rec == nil {
			rec = recordValue(rm.Block[id])
		}
		if rec == nil {
			continue
		}
		out = append(out, map[string]any{
			"id":    id,
			"type":  strField(rec, "type"),
			"title": blockTitle(rec),
		})
	}
	return map[string]any{"count": len(out), "records": out}, nil
}

func createPage(c *connector.Ctx) (any, error) {
	parentID := normalizeID(c.Input("parent_id"))
	title := strings.TrimSpace(c.Input("title"))
	if parentID == "" || title == "" {
		return nil, errors.New("parent_id and title are required")
	}
	parentTable := "block"
	if strings.TrimSpace(c.Input("parent_type")) == "database" {
		parentTable = "collection"
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}

	// Optional properties (database rows). Supplied as a JSON object keyed by
	// property NAME → string value; resolved to ids + formatted by type against
	// the collection schema. Ignored for a plain page parent.
	var sets []propSet
	var skipped []string
	if raw := strings.TrimSpace(c.Input("properties")); raw != "" && parentTable == "collection" {
		var props map[string]string
		if err := json.Unmarshal([]byte(raw), &props); err != nil {
			return nil, fmt.Errorf("properties is not valid JSON object of name→value: %w", err)
		}
		nameToID, idToType, ferr := cl.fetchCollectionSchema(parentID)
		if ferr != nil {
			return nil, fmt.Errorf("load database schema: %w", ferr)
		}
		sets, skipped = resolveProps(props, nameToID, idToType)
	}

	id, err := cl.createPage(parentID, parentTable, title, sets)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "url": "https://www.notion.so/" + strings.ReplaceAll(id, "-", "")}
	if len(skipped) > 0 {
		out["skipped_properties"] = skipped
	}
	return out, nil
}

// updatePageProperties edits the property cells of an existing database row in
// place. The row is addressed by its page id; properties is a JSON object of
// name→value (same shapes create_page accepts). Only the listed properties
// change — the rest of the row and its body content are untouched.
func updatePageProperties(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("page_id"))
	raw := strings.TrimSpace(c.Input("properties"))
	if id == "" || raw == "" {
		return nil, errors.New("page_id and properties are required")
	}
	var props map[string]string
	if err := json.Unmarshal([]byte(raw), &props); err != nil {
		return nil, fmt.Errorf("properties is not valid JSON object of name→value: %w", err)
	}
	if len(props) == 0 {
		return nil, errors.New("properties is empty — nothing to update")
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}
	updated, skipped, err := cl.updatePageProps(id, props)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "updated": updated}
	if len(skipped) > 0 {
		out["skipped_properties"] = skipped
	}
	return out, nil
}

// describeDatabase returns a database's schema so the agent knows exactly what
// it can set on a new row: each property's name, type, whether it's writable,
// and select options. For an embedded/linked view it also reports the active
// filter — so the agent knows which property a new row must set (and to what)
// to appear in that view.
func describeDatabase(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("page_id"))
	if id == "" {
		return nil, errors.New("page_id is required")
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}
	rm, err := cl.loadPageChunk(id)
	if err != nil {
		return nil, err
	}

	// Resolve the collection: the page IS a collection, or it embeds one.
	collID := firstKey(rm.Collection)
	viewID := firstKey(rm.CollectionView)
	if collID == "" {
		// A content page that embeds a database view — find the first
		// collection_view block and resolve its collection.
		if root := recordValue(rm.Block[id]); root != nil {
			for _, cid := range contentIDs(root) {
				b := recordValue(rm.Block[cid])
				if b == nil {
					continue
				}
				if t := strField(b, "type"); t == "collection_view" || t == "collection_view_page" {
					if raw, ok := b["view_ids"]; ok {
						var ids []string
						if json.Unmarshal(raw, &ids) == nil && len(ids) > 0 {
							viewID = ids[0]
						}
					}
					collID = strField(b, "collection_id")
					if collID == "" && viewID != "" {
						collID = collectionPointerOfView(rm, viewID)
					}
					break
				}
			}
		}
	}
	if collID == "" {
		return nil, errors.New("no database found on this page")
	}

	coll := recordValue(rm.Collection[collID])
	if coll == nil {
		if rm2, e := cl.syncRecords("collection", []string{collID}); e == nil {
			coll = recordValue(rm2.Collection[collID])
		}
	}
	schema := collectionSchema(coll)
	types := collectionSchemaTypes(coll)

	props := make([]map[string]any, 0, len(schema))
	for pid, name := range schema {
		t := types[pid]
		_, settable := formatProperty(t, "")
		entry := map[string]any{"name": name, "type": t, "writable": settable}
		if opts := selectOptions(coll, pid); len(opts) > 0 {
			entry["options"] = opts
		}
		props = append(props, entry)
	}

	out := map[string]any{
		"database_id": collID,
		"title":       inlineToPlain(coll["name"]),
		"properties":  props,
	}
	if f := viewFilterSummary(recordValue(rm.CollectionView[viewID]), schema); f != "" {
		out["view_filter"] = f
		out["hint"] = "To make a new row appear in this view, set the property named in view_filter accordingly (e.g. a relation property → this page id)."
	}
	return out, nil
}

func createComment(c *connector.Ctx) (any, error) {
	pageID := normalizeID(c.Input("page_id"))
	text := strings.TrimSpace(c.Input("text"))
	if pageID == "" || text == "" {
		return nil, errors.New("page_id and text are required")
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}
	comID, discID, err := cl.createComment(pageID, text)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": comID, "discussion_id": discID}, nil
}

func setTitle(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("page_id"))
	title := strings.TrimSpace(c.Input("title"))
	if id == "" || title == "" {
		return nil, errors.New("page_id and title are required")
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}
	if err := cl.setTitle(id, title); err != nil {
		return nil, err
	}
	return map[string]any{"id": id, "title": title}, nil
}

func appendContent(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("page_id"))
	md := c.Input("markdown")
	if id == "" || strings.TrimSpace(md) == "" {
		return nil, errors.New("page_id and markdown are required")
	}
	blocks := markdownToBlocks(md)
	if len(blocks) == 0 {
		return nil, errors.New("markdown produced no blocks")
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}
	// Optional anchor: insert right AFTER this block instead of at the page end.
	afterID := normalizeID(c.Input("after_block_id"))
	ids, err := cl.appendContent(id, blocks, afterID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"page_id": id, "added": len(ids), "block_ids": ids}, nil
}

func listBlocks(c *connector.Ctx) (any, error) {
	id := normalizeID(c.Input("page_id"))
	if id == "" {
		return nil, errors.New("page_id is required")
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}
	blocks, err := cl.listBlocks(id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"page_id": id, "count": len(blocks), "blocks": blocks}, nil
}

func updateBlock(c *connector.Ctx) (any, error) {
	blockID := normalizeID(c.Input("block_id"))
	text := c.Input("text")
	if blockID == "" || strings.TrimSpace(text) == "" {
		return nil, errors.New("block_id and text are required")
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}
	if err := cl.updateBlock(blockID, text, strings.TrimSpace(c.Input("type"))); err != nil {
		return nil, err
	}
	return map[string]any{"id": blockID}, nil
}

func deleteBlock(c *connector.Ctx) (any, error) {
	blockID := normalizeID(c.Input("block_id"))
	pageID := normalizeID(c.Input("page_id"))
	if blockID == "" || pageID == "" {
		return nil, errors.New("page_id and block_id are required")
	}
	cl, err := newClient(c)
	if err != nil {
		return nil, err
	}
	if err := cl.deleteBlock(pageID, blockID); err != nil {
		return nil, err
	}
	return map[string]any{"id": blockID, "deleted": true}, nil
}

// --- config-only widget ---

func connectionStatus(c *connector.Ctx) (any, error) {
	// Ungated: the status card must work during setup, before the usage_note is
	// filled — that's how the operator confirms the token before enabling ops.
	cl, err := newClientUngated(c)
	if err != nil {
		return map[string]any{"html": statusCard(false, "Fill token_v2 first.")}, nil
	}
	rm, err := cl.loadUserContent()
	if err != nil {
		return map[string]any{"html": statusCard(false, html.EscapeString(shorten(err.Error(), 160)))}, nil
	}
	parts := []string{"Connected"}
	if user := firstRecord(rm.NotionUser); user != nil {
		name := strings.TrimSpace(strField(user, "name"))
		if name == "" {
			name = strings.TrimSpace(strField(user, "given_name") + " " + strField(user, "family_name"))
		}
		if name != "" {
			parts = append(parts, "as "+html.EscapeString(name))
		} else if email := strField(user, "email"); email != "" {
			parts = append(parts, html.EscapeString(email))
		}
	}
	if space := firstRecord(rm.Space); space != nil {
		if n := strField(space, "name"); n != "" {
			parts = append(parts, "workspace "+html.EscapeString(n))
		}
	}
	return map[string]any{"html": statusCard(true, strings.Join(parts, " · "))}, nil
}

// --- client calls (map onto api/v3 endpoints) ---

func (cl *v3Client) loadUserContent() (*recordMap, error) {
	raw, err := cl.post("/loadUserContent", map[string]any{})
	if err != nil {
		return nil, err
	}
	return decodeRecordMap(raw)
}

func (cl *v3Client) loadPageChunk(pageID string) (*recordMap, error) {
	raw, err := cl.post("/loadPageChunk", map[string]any{
		"pageId":          pageID,
		"limit":           100,
		"cursor":          map[string]any{"stack": []any{}},
		"chunkNumber":     0,
		"verticalColumns": false,
	})
	if err != nil {
		return nil, err
	}
	return decodeRecordMap(raw)
}

// viewQuery carries a view's filter + sort (already in api/v3 shape) so a query
// can reproduce what a specific collection view shows. Nil fields are omitted.
type viewQuery struct {
	Filter json.RawMessage // {operator, filters:[...]}
	Sort   json.RawMessage // [{property, direction}, ...]
}

// queryCollection returns the collection's recordMap plus the ordered row ids.
// An optional viewQuery applies a view's filter/sort so the result matches what
// that view renders (e.g. a linked/filtered inline database).
func (cl *v3Client) queryCollection(collectionID, viewID string, limit int, vq *viewQuery) (*recordMap, []string, error) {
	loader := map[string]any{
		"type":         "reducer",
		"reducers":     map[string]any{"collection_group_results": map[string]any{"type": "results", "limit": limit}},
		"searchQuery":  "",
		"userTimeZone": "UTC",
	}
	if vq != nil {
		if len(vq.Filter) > 0 {
			loader["filter"] = vq.Filter
		}
		if len(vq.Sort) > 0 {
			loader["sort"] = vq.Sort
		}
	}
	raw, err := cl.post("/queryCollection", map[string]any{
		"collection":     map[string]any{"id": collectionID},
		"collectionView": map[string]any{"id": viewID},
		"loader":         loader,
	})
	if err != nil {
		return nil, nil, err
	}
	rm, err := decodeRecordMap(raw)
	if err != nil {
		return nil, nil, err
	}
	// Row ids live under result.reducerResults.collection_group_results.blockIds.
	var env struct {
		Result struct {
			ReducerResults struct {
				CollectionGroupResults struct {
					BlockIDs []string `json:"blockIds"`
				} `json:"collection_group_results"`
			} `json:"reducerResults"`
		} `json:"result"`
	}
	_ = json.Unmarshal(raw, &env)
	return rm, env.Result.ReducerResults.CollectionGroupResults.BlockIDs, nil
}

func (cl *v3Client) syncRecordValues(ids []string) (*recordMap, error) {
	return cl.syncRecords("block", ids)
}

// syncRecords fetches records of one table by id and returns the recordMap.
func (cl *v3Client) syncRecords(table string, ids []string) (*recordMap, error) {
	pointers := make([]any, 0, len(ids))
	for _, id := range ids {
		pointers = append(pointers, map[string]any{
			"pointer": map[string]any{"table": table, "id": normalizeID(id)},
			"version": -1,
		})
	}
	raw, err := cl.post("/syncRecordValues", map[string]any{"requests": pointers})
	if err != nil {
		return nil, err
	}
	return decodeRecordMap(raw)
}

// resolveUsers fetches the notion_user records for any user ids referenced by
// the rows (created_by / last_edited_by) that aren't already in rm, and merges
// them in so cellValue can render display names instead of ids. Best-effort:
// a failed fetch leaves ids to fall back to short form.
func (cl *v3Client) resolveUsers(rm *recordMap, rowIDs []string) {
	if rm == nil {
		return
	}
	want := map[string]bool{}
	for _, rid := range rowIDs {
		row := recordValue(rm.Block[rid])
		if row == nil {
			continue
		}
		for _, k := range []string{"created_by_id", "last_edited_by_id"} {
			if uid := strField(row, k); uid != "" {
				if rm.NotionUser == nil || rm.NotionUser[uid] == nil {
					want[uid] = true
				}
			}
		}
	}
	if len(want) == 0 {
		return
	}
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	got, err := cl.syncRecords("notion_user", ids)
	if err != nil || got == nil || len(got.NotionUser) == 0 {
		return
	}
	if rm.NotionUser == nil {
		rm.NotionUser = map[string]json.RawMessage{}
	}
	for k, v := range got.NotionUser {
		rm.NotionUser[k] = v
	}
}

// decodeRecordMap parses only the recordMap envelope, tolerating the numeric
// __version__ field (it's not one of our typed sub-maps, so it's ignored).
func decodeRecordMap(raw []byte) (*recordMap, error) {
	var env struct {
		RecordMap recordMap `json:"recordMap"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode recordMap: %w", err)
	}
	return &env.RecordMap, nil
}

// --- helpers ---

func firstKey(m map[string]json.RawMessage) string {
	for k := range m {
		return k
	}
	return ""
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// statusCard renders the connection-status widget. Mirrors the other connectors.
func statusCard(ok bool, detail string) string {
	dot := `<span class="h-2 w-2 rounded-full bg-neg-400"></span>`
	ring := "border-neg-300 bg-neg-100 dark:bg-navy-800"
	text := "text-neg-400"
	if ok {
		dot = `<span class="h-2 w-2 rounded-full bg-pos-400"></span>`
		ring = "border-pos-300 bg-pos-100 dark:bg-navy-800"
		text = "text-pos-400"
	}
	return `<div class="flex items-center gap-2 rounded-lg border ` + ring + ` px-4 py-2.5">` +
		dot +
		`<span class="text-xs font-medium ` + text + `">` + detail + `</span>` +
		`</div>`
}
