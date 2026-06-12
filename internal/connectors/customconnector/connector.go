// Package customconnector is the management connector for custom
// connector definitions: the same lifecycle the admin UI offers
// (create / inspect / update / re-sync / disable / delete definitions,
// plus instance rows), exposed as LLM-callable operations so an agent
// can build a connector without anyone opening the dashboard.
//
// There is deliberately NO cURL-parse operation — the LLM converts a
// cURL (or any API doc) into the manual draft shape itself and calls
// def_create. The OAuth scheme is the one thing that cannot ride this
// surface: it needs a browser login, so mcp_register rejects it and
// points at the UI.
//
// Authorization is scoped, not admin-gated: admins manage every
// definition, other callers only the ones they created (level 1 of the
// ownership contract). Whether a user can reach these operations at
// all is the usual tag story — the row ships with the System tag
// (admin-only) by default; grant access tags to open it up.
package customconnector

import (
	"github.com/yogasw/wick/internal/connectors"
	custom "github.com/yogasw/wick/internal/connectors/custom"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// Key is the connector definition slug. Single instance is auto-seeded
// on first boot (Meta.Fixed=true).
const Key = "custom-connector"

// Deps wires the management plane the operations act on.
type Deps struct {
	Custom     *custom.Service
	Connectors *connectors.Service
}

// Meta returns the static metadata block. Fixed=true so wick
// auto-seeds exactly one row and the manager UI hides "+ New row".
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Custom Connectors",
		Description: "Create and manage wick custom connectors (definitions + instances) without the dashboard. Use when the user asks to add, change, or remove a connector wick doesn't ship.",
		Icon:        "🧩",
		Fixed:       true,
	}
}

// Module returns the fully-wired module. DefaultTags carry tags.System
// so the row is hidden from non-admin users — this is wick's own
// management plane — plus tags.Connector for the home-page group.
func Module(deps Deps) connector.Module {
	m := Meta()
	m.DefaultTags = []tool.DefaultTag{tags.Connector, tags.System}
	return connector.Module{
		Meta:       m,
		Operations: Operations(deps),
	}
}

// ── per-op inputs ────────────────────────────────────────────────────

type emptyInput struct{}

type defKeyInput struct {
	Key string `wick:"required;desc=Custom connector definition key (slug). Example: context7"`
}

type defCreateInput struct {
	Draft string `wick:"textarea;required;desc=Full definition draft as JSON: {key, name, description, icon, category, single, configs: [{key,label,widget,secret,required,default,desc}], ops: [{key,name,description,destructive,inputs:[...],request:{method,url_template,headers,body_template,content_type}}]}. Templates use Go text/template over {{.cfg.<key>}} and {{.in.<key>}}. Convert cURL or API docs into this shape yourself."`
}

type defUpdateInput struct {
	Key   string `wick:"required;desc=Definition key to update (immutable, must match the existing def)."`
	Draft string `wick:"textarea;required;desc=Full replacement draft as JSON — same shape as def_create. Fetch the current shape with def_get first; the stored definition is replaced wholesale and the live module reloads immediately."`
}

type defSetDisabledInput struct {
	Key      string `wick:"required;desc=Definition key."`
	Disabled bool   `wick:"desc=true disables (zero operations served; instances and pages stay), false re-enables."`
}

type defResyncInput struct {
	Key        string `wick:"required;desc=Definition key."`
	InstanceID string `wick:"desc=Optional instance whose account authenticates the probe (oauth-scheme MCP servers may expose different tools per account)."`
}

type mcpRegisterInput struct {
	Label       string `wick:"required;desc=Display name — becomes the connector's name and its key (slugified)."`
	Icon        string `wick:"desc=Optional icon: an emoji, an inline <svg>, or a data:image/...;base64 payload (max 32KB)."`
	Description string `wick:"textarea;desc=Optional connector description. Empty = adopt the server's own initialize instructions and keep them fresh on every re-sync; non-empty = locked, never auto-replaced."`
	URL         string `wick:"url;required;desc=Streamable-HTTP MCP endpoint. Example: https://mcp.example.com/v1"`
	AuthScheme  string `wick:"dropdown=none|bearer|custom_header|sso;desc=Auth scheme. Default none. The oauth scheme needs a browser login and is NOT available here — register from the dashboard instead."`
	AuthSecret  string `wick:"secret;desc=Bearer token (bearer scheme only)."`
	Headers     string `wick:"textarea;desc=Optional extra header rows as JSON: [{\"key\":\"X-Tenant\",\"value\":\"prod\",\"secret\":false}]. Applied on every call."`
	Excluded    string `wick:"textarea;desc=Optional JSON array of tool names to exclude. Everything else the server lists becomes an operation."`
}

type mcpSetExcludedInput struct {
	Key      string `wick:"required;desc=Definition key of the MCP connector."`
	Excluded string `wick:"textarea;required;desc=JSON array of tool names to exclude, replacing the current list. [] exposes everything."`
}

type instanceCreateInput struct {
	Key   string `wick:"required;desc=Definition key to add an instance row to."`
	Label string `wick:"desc=Optional row label. Defaults to '<name> (new)'."`
}

type instanceIDInput struct {
	InstanceID string `wick:"required;desc=Instance row ID (from instance_list or wick_list)."`
}

type instanceSetDisabledInput struct {
	InstanceID string `wick:"required;desc=Instance row ID."`
	Disabled   bool   `wick:"desc=true disables the row (hidden from wick_list, calls rejected), false re-enables."`
}

// Operations builds the closure-bound op list. ACCESS: handlers scope
// by caller (admin = every definition, others = only their own);
// descriptions say so for the LLM's benefit.
func Operations(deps Deps) []connector.Operation {
	h := handlers{deps}
	return []connector.Operation{
		// ── definitions ──────────────────────────────────────────
		connector.Op("def_list", "List Definitions",
			"List all custom connector definitions. Returns array of {key, name, source (curl|mcp|manual), disabled, single_instance, operations, instances}. Access: scoped — admins manage every definition, other callers only the ones they created. UI: <app_url>/manager/connectors.",
			emptyInput{}, h.defList, wickdocs.Docs{}),
		connector.Op("def_get", "Get Definition",
			"Get one definition's full editable shape: meta, config field schema, ops (curl/manual) or live tool catalog + excluded list + server info (mcp). Returns the same draft JSON def_create/def_update consume. Access: scoped — admins manage every definition, other callers only the ones they created.",
			defKeyInput{}, h.defGet, wickdocs.Docs{}),
		connector.Op("def_create", "Create Definition",
			"Create a custom connector from a manual draft (convert cURL or API docs into the draft yourself). Validates, persists, registers the live module, and creates the access tag custom:<key> (admin-only until granted). No instance row is created — call instance_create next. Returns {key, name}. Access: scoped — admins manage every definition, other callers only the ones they created.",
			defCreateInput{}, h.defCreate, wickdocs.Docs{}),
		connector.OpDestructive("def_update", "Update Definition",
			"Replace a definition's stored draft wholesale and reload the live module. Key is immutable. Fetch def_get first, edit, send back. Returns {ok, key}. Access: scoped — admins manage every definition, other callers only the ones they created.",
			defUpdateInput{}, h.defUpdate, wickdocs.Docs{}),
		connector.OpDestructive("def_set_disabled", "Disable/Enable Definition",
			"Toggle a definition: disabled serves zero operations (instances and pages stay) until re-enabled; enabling an MCP def re-probes its tools. Returns {ok, key, disabled}. Access: scoped — admins manage every definition, other callers only the ones they created.",
			defSetDisabledInput{}, h.defSetDisabled, wickdocs.Docs{}),
		connector.OpDestructive("def_delete", "Delete Definition",
			"Delete a definition, all its instance rows, and (for MCP defs) the server registration. Run history is kept for audit; the access tag survives so re-creating the key restores grants. Irreversible. Returns {ok, key}. Access: scoped — admins manage every definition, other callers only the ones they created.",
			defKeyInput{}, h.defDelete, wickdocs.Docs{}),
		connector.Op("def_resync", "Re-sync MCP Tools",
			"Re-fetch an MCP definition's live tools/list and swap the fresh operation set in (also the reconnect: refreshes the Connected status). No-op value for curl/manual defs. Returns {ok, key, operations}. Access: scoped — admins manage every definition, other callers only the ones they created.",
			defResyncInput{}, h.defResync, wickdocs.Docs{}),

		// ── MCP servers ──────────────────────────────────────────
		connector.Op("mcp_register", "Register MCP Server",
			"Register an external MCP server as a connector: tests initialize + tools/list with the given auth (save is rejected when the test fails), then creates the connector — every listed tool becomes an operation minus the excluded names; tools added server-side appear after a re-sync. One server = one connector named after the label. The oauth scheme is not available here (browser login) — use the dashboard for that. Returns {key, name, tools}. Access: scoped — admins manage every definition, other callers only the ones they created.",
			mcpRegisterInput{}, h.mcpRegister, wickdocs.Docs{}),
		connector.OpDestructive("mcp_set_excluded", "Set Excluded Tools",
			"Replace an MCP connector's excluded-tool list and re-sync the catalog. Returns {ok, key, excluded, operations}. Access: scoped — admins manage every definition, other callers only the ones they created.",
			mcpSetExcludedInput{}, h.mcpSetExcluded, wickdocs.Docs{}),

		// ── instances ────────────────────────────────────────────
		connector.Op("instance_list", "List Instances",
			"List a definition's instance rows. Returns array of {id, label, disabled, status (ready|needs_setup)}. Access: scoped — admins manage every definition, other callers only the ones they created. UI: <app_url>/manager/connectors/<key>.",
			defKeyInput{}, h.instanceList, wickdocs.Docs{}),
		connector.Op("instance_create", "Create Instance",
			"Add an instance row to a definition (the '+ New row' of the UI) and link its access tags. Each row carries its own credential values — set them afterwards via the dashboard or the wickmanager connector. Rejected for single-instance defs that already have their row. Returns {id, label}. Access: scoped — admins manage every definition, other callers only the ones they created.",
			instanceCreateInput{}, h.instanceCreate, wickdocs.Docs{}),
		connector.OpDestructive("instance_delete", "Delete Instance",
			"Delete one instance row (credentials and per-op state go with it; run history is kept). The definition stays — instance_create brings a new row. Returns {ok, id}. Access: scoped — admins manage every definition, other callers only the ones they created.",
			instanceIDInput{}, h.instanceDelete, wickdocs.Docs{}),
		connector.OpDestructive("instance_set_disabled", "Disable/Enable Instance",
			"Toggle one instance row: disabled rejects every call and hides it from wick_list until re-enabled. Per-op enable state is preserved. Returns {ok, id, disabled}. Access: scoped — admins manage every definition, other callers only the ones they created.",
			instanceSetDisabledInput{}, h.instanceSetDisabled, wickdocs.Docs{}),
	}
}
