// Package datatables exposes the workflow engine's data-table store as its
// own fixed single-instance MCP connector. Data tables are an n8n-style
// shared key/value store: workflows read/write them via datatable_* nodes,
// and an AI with MCP access manages their schema and rows through the ops
// registered here.
//
// Split out of the workflow connector so the two surfaces can be tagged,
// enabled, and audited independently — an agent that only needs to read a
// table no longer needs the whole workflow authoring surface.
//
// File layout:
//
//   - connector.go — Meta, Configs, Input structs, Operations, handler struct
//   - ops.go       — handler implementations + the per-caller ACL gate
//
// Wire-up: call datatables.Module(ops) and pass the result to
// connectors.Register(...) before connectors.Service.Bootstrap. The
// mcp.Ops pointer is the same bundle the workflow connector receives,
// obtained from the workflow bootstrap (internal/agents/workflow/).
package datatables

import (
	wfmcp "github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const Key = "datatables"

// Configs is intentionally empty — the connector talks to the in-process
// data-table store, not an external API.
type Configs struct{}

// Meta returns the static metadata block.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Data Tables",
		Description: "Manage wick data tables over MCP: create and drop tables, inspect schemas, and query, insert, upsert, delete, or count rows. Tables are the shared store workflows reach through datatable_* nodes.",
		Icon:        "🗄",
		Fixed:       true,
	}
}

// Module returns the fully-wired connector.Module for the given ops.
// Call connectors.Register(datatables.Module(ops)) at boot before
// connectors.Service.Bootstrap runs.
func Module(ops *wfmcp.Ops) connector.Module {
	m := Meta()
	m.DefaultTags = []tool.DefaultTag{tags.Connector, tags.Platform}
	return connector.Module{
		Meta:       m,
		Operations: Operations(ops),
	}
}

// Operations builds the op list, capturing ops so every handler can reach
// the data-table store.
func Operations(ops *wfmcp.Ops) []connector.Category {
	h := &handlers{ops: ops}
	return []connector.Category{connector.Cat("Data Tables", "Inspect and mutate the n8n-style shared key/value store workflows read through datatable_* nodes.",
		connector.Op("datatable_list", "List Data Tables",
			"List every registered data table with row count + column count. Use to discover what tables exist before referencing one in a workflow.",
			emptyInput{}, h.datatableList, wickdocs.Docs{}),
		connector.Op("datatable_get", "Get Data Table",
			"Return one table's schema + row count. Use before workflow_add_node to see column names/types for datatable_* nodes.",
			slugInput{}, h.datatableGet, wickdocs.Docs{}),
		connector.Op("datatable_create", "Create Data Table",
			"Register a new data table. Slug must be lowercase a-z0-9-. Columns are app-validated against type (string/int/float/bool/timestamp/json/enum). Primary key defaults to the first column when omitted.",
			createInput{}, h.datatableCreate, wickdocs.Docs{}),
		connector.Op("datatable_drop", "Drop Data Table",
			"Remove a table and all its rows. DESTRUCTIVE. Returns {ok}.",
			slugInput{}, h.datatableDrop, wickdocs.Docs{}),
		connector.Op("datatable_query", "Query Data Table",
			"Return rows matching filters. Use 'where' for simple equality, or 'conditions' (n8n parity) for richer ops (equals, not_equals, gt, gte, lt, lte, contains, in, is_empty, is_not_empty). Optional order_by + limit + offset.",
			queryInput{}, h.datatableQuery, wickdocs.Docs{}),
		connector.Op("datatable_insert", "Insert Row",
			"Insert a row. Fails on primary-key conflict. Row is validated against the table schema.",
			rowInput{}, h.datatableInsert, wickdocs.Docs{}),
		connector.Op("datatable_upsert", "Upsert Row",
			"Insert when PK doesn't exist, update otherwise. Idempotent. Returns {action: insert|update}. "+
				"Note it INSERTS at whatever id you pass, so a wrong id creates a new row instead of editing one — "+
				"use datatable_update when the row is supposed to already exist.",
			rowInput{}, h.datatableUpsert, wickdocs.Docs{}),
		connector.Op("datatable_update", "Update Row",
			"Patch an EXISTING row, matched by 'id' in the row payload. Never creates a row: if the id does not exist "+
				"the call fails instead of quietly inserting, so a wrong id is reported rather than silently doubling your data. "+
				"Keys you omit keep their current values. Use this for edits; use datatable_upsert only when creating is also acceptable.",
			rowInput{}, h.datatableUpdate, wickdocs.Docs{}),
		connector.Op("datatable_delete", "Delete Rows",
			"Delete rows matching 'where' equality or 'conditions' list (n8n parity ops). Returns {deleted_count}. DESTRUCTIVE.",
			filterInput{}, h.datatableDelete, wickdocs.Docs{}),
		connector.Op("datatable_count", "Count Rows",
			"Count rows matching 'where' equality or 'conditions'. Cheap statistic for decisions.",
			filterInput{}, h.datatableCount, wickdocs.Docs{}),
	)}
}

// ── Inputs ────────────────────────────────────────────────────────────

type emptyInput struct{}

type slugInput struct {
	Slug string `wick:"required;desc=Data table slug (the alias workflows reference)."`
}

type createInput struct {
	Slug       string `wick:"required;desc=Lowercase a-z0-9- slug. Used as the alias workflows reference."`
	Mode       string `wick:"desc=strict (default) rejects extra keys, lax accepts them."`
	PrimaryKey string `wick:"desc=Comma-separated primary key column names. Defaults to the first column."`
	Columns    string `wick:"required;textarea;desc=One column per line: name:type. Types: string, int, float, bool, timestamp, json, enum. Example:\\nid:string\\nstatus:enum\\ncreated_at:timestamp"`
	Access     string `wick:"desc=Optional access JSON: {\"workflows\":[\"wf-id\"],\"row_filter\":\"by_creator\"}."`
}

type queryInput struct {
	Slug       string `wick:"required;desc=Data table slug."`
	Where      string `wick:"textarea;desc=Optional equality JSON: {\"status\":\"open\"}."`
	Conditions string `wick:"textarea;desc=Optional condition JSON array (n8n parity ops): [{\"column\":\"priority\",\"op\":\"gte\",\"value\":5}]. Wins over Where when both set."`
	OrderBy    string `wick:"desc=Optional order JSON: [{\"column\":\"priority\",\"direction\":\"desc\"}]."`
	Limit      int    `wick:"number;desc=Optional row cap. 0 = no limit."`
	Offset     int    `wick:"number;desc=Optional row skip."`
}

type rowInput struct {
	Slug string `wick:"required;desc=Data table slug."`
	Row  string `wick:"required;textarea;desc=Row JSON: {\"id\":\"E1\",\"status\":\"open\"}."`
}

type filterInput struct {
	Slug       string `wick:"required;desc=Data table slug."`
	Where      string `wick:"textarea;desc=Optional equality JSON."`
	Conditions string `wick:"textarea;desc=Optional condition JSON array (n8n parity ops). Wins over Where when both set."`
}

// ── Handler struct ────────────────────────────────────────────────────

type handlers struct {
	ops *wfmcp.Ops
}
