package main

import (
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

const Key = "notion_unofficial"

// Config holds the browser-session credentials the private API needs. token_v2
// is the value of the token_v2 cookie on notion.so (DevTools → Application →
// Cookies). ActiveUserID fills the x-notion-active-user-header some endpoints
// require on multi-account sessions; leave blank for a single-account login.
type Config struct {
	// Import is the easy path: an html widget (import_form op) renders a textarea
	// where the operator pastes a "Copy as cURL" of any api/v3 request from
	// DevTools, plus an Extract button. Extract parses the curl and writes the
	// individual fields below (token_v2, user_agent, …) via the core's
	// multi-field mechanism, then shows a feedback line. This field itself stores
	// nothing — it's just the widget mount point.
	Import string `wick:"html=import_form;group=Authentication;desc=Paste a Copy-as-cURL of any notion.so/api/v3 request from DevTools, then Extract — it fills the fields below."`

	TokenV2      string `wick:"secret;group=Authentication;desc=Value of the token_v2 cookie from a logged-in notion.so browser session (DevTools → Application → Cookies → token_v2). Filled by Extract, or paste manually. Expires when the session ends."`
	ActiveUserID string `wick:"group=Authentication;desc=Optional. Notion user ID for the x-notion-active-user-header, needed only on sessions with multiple accounts."`
	// Status is a read-only widget: it calls LoadUserContent live and shows the
	// logged-in user + workspace so the operator can confirm the cookie works.
	Status string `wick:"html=connection_status;group=Authentication;desc=Live connection status: probes the cookie and shows the logged-in user + workspace. Paste a cURL or fill token_v2 first."`

	// The private API is the browser's API — requests present as a browser to
	// avoid being flagged. Sensible defaults are baked in; override only if a
	// request gets blocked or you want to match a specific browser/app version.
	// NOTE: no default= in the User-Agent tag — the value contains ';' which is
	// the wick tag delimiter and would corrupt parsing. The default is applied
	// in code (newClient falls back to defaultUserAgent when the config is
	// empty), so a blank field still sends a modern-Chrome UA.
	UserAgent           string `wick:"group=Advanced;desc=Browser User-Agent sent with every request. Leave blank for a modern Chrome default; change only if requests get blocked."`
	NotionClientVersion string `wick:"group=Advanced;default=23.13.0.0;desc=Notion-Client-Version header the web app sends. Leave blank for a sensible default."`
}

// --- per-operation input structs ---

type fetchInput struct {
	PageID string `wick:"required;desc=Page ID (dashed UUID or the 32-char id from a Notion URL)."`
}

type queryDatabaseInput struct {
	PageID string `wick:"required;desc=ID of a database (collection) page. Its rows are returned as records."`
	Limit  int    `wick:"desc=Max rows to return. Default 100, max 1000."`
}

type setTitleInput struct {
	PageID string `wick:"required;desc=ID of the page whose title to change."`
	Title  string `wick:"required;desc=New page title (plain text)."`
}

type appendContentInput struct {
	PageID       string `wick:"required;desc=ID of the page to add blocks to (dashed UUID or 32-char id from a Notion URL)."`
	Markdown     string `wick:"required;desc=Markdown turned into new blocks. Supports headings (#/##/###), bullet/numbered/to-do lists, quotes, fenced code, and dividers. Inline bold/code/links are stored as plain text."`
	AfterBlockID string `wick:"desc=Optional. Insert the new blocks right AFTER this block id (get it from list_blocks) — use this to add in the MIDDLE of a page. Leave blank to append at the end. Existing content is never touched either way."`
}

type listBlocksInput struct {
	PageID string `wick:"required;desc=ID of the page whose top-level blocks to list. Returns each block's {id, type, text} — use a returned id with update_block or delete_block to edit ONE block without touching the rest of the page."`
}

type updateBlockInput struct {
	BlockID string `wick:"required;desc=ID of the block to rewrite (get it from list_blocks). Only this block changes."`
	Text    string `wick:"required;desc=New plain-text content for the block. Replaces the block's current text; the block type is kept unless 'type' is set."`
	Type    string `wick:"desc=Optional new block type to switch to, e.g. text, header, sub_header, sub_sub_header, bulleted_list, numbered_list, to_do, quote, code. Leave blank to keep the current type."`
}

type deleteBlockInput struct {
	PageID  string `wick:"required;desc=ID of the page the block sits on."`
	BlockID string `wick:"required;desc=ID of the block to delete (get it from list_blocks). Only this block is removed; the rest of the page stays."`
}

type describeDatabaseInput struct {
	PageID string `wick:"required;desc=ID of a database page OR a page that embeds a database. Returns the schema so you know what to set on a new row."`
}

type createPageInput struct {
	ParentType string `wick:"required;dropdown=page|database;default=page;desc=page = subpage under a page; database = a new row in a database (collection)."`
	ParentID   string `wick:"required;desc=ID of the parent page (for a subpage) or database (for a row)."`
	Title      string `wick:"required;desc=Page/row title (plain text). Fills the Name/title column."`
	Properties string `wick:"desc=Database rows only. JSON object keyed by property NAME → string value. Call describe_database first for names/types/options. Formats: select=exact option; multi_select=comma-separated; checkbox=true/false; date=\"YYYY-MM-DD\" or \"YYYY-MM-DD HH:MM\" (range with \" → \"); relation/person=comma-separated ids. Example: {\"Activity\":\"Debug\",\"Start time\":\"2026-07-17 06:00\",\"End time\":\"2026-07-17 07:00\",\"Ticket\":\"<page-id>\"}"`
}

type updatePagePropertiesInput struct {
	PageID     string `wick:"required;desc=ID of the database ROW (a row is a page) whose properties to update. Must be a row inside a database — a plain page has no properties. For its title use set_title; for body content use update_block."`
	Properties string `wick:"required;desc=JSON object keyed by property NAME → string value; only the listed properties change, the rest of the row is untouched. Call describe_database first for names/types/options. Formats: select=exact option; multi_select=comma-separated; checkbox=true/false; date=\"YYYY-MM-DD\" or \"YYYY-MM-DD HH:MM\" (range with \" → \"); relation/person=comma-separated ids. To clear a cell pass an empty string. Example: {\"Status\":\"Done\",\"End time\":\"2026-07-21 07:00\"}"`
}

type createCommentInput struct {
	PageID string `wick:"required;desc=ID of the page (or database row — a row is a page) to comment on."`
	Text   string `wick:"required;desc=Comment text (plain text)."`
}

type recordsInput struct {
	IDs string `wick:"required;desc=Comma-separated block/record IDs to fetch raw. Example: id1,id2,id3"`
}

// pickerInput drives the connection_status html widget (arg always sent as
// "browser"; ignored here).
type pickerInput struct {
	Browser string `wick:"desc=Unused; present because the html widget always sends it."`
}

// importFormInput drives the import_form html widget. On Extract, the widget
// sends the textarea's value under the name "raw"; the initial render sends
// nothing meaningful.
type importFormInput struct {
	Browser string `wick:"desc=Unused; the html widget always sends it."`
	Raw     string `wick:"desc=The pasted cURL text (sent as the textarea's named value on Extract)."`
}

// Module is the connector definition.
func Module() connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:         Key,
			Name:        "Notion (Unofficial)",
			Description: "Read and edit Notion pages and databases via the private web API using a token_v2 cookie. Fetch returns rich markdown; write ops append content and edit or delete individual blocks in place (list_blocks → update_block/delete_block) without rewriting the whole page.",
			Icon:        "📓",
			DefaultTags: []entity.DefaultTag{tags.Connector, tags.Productivity},
		},
		Configs:    entity.StructToConfigs(Config{}),
		Operations: Operations(),
	}
}

func Operations() []connector.Category {
	return []connector.Category{
		connector.Cat(
			"Read",
			"Read pages and databases through Notion's private API. Fetch renders the full page as markdown, the closest match to the Notion MCP's enhanced markdown.",
			connector.Op(
				"fetch",
				"Fetch Page",
				"Download a page and render its whole body as markdown, plus its title. Recurses the block tree. This is the MCP-style single-call read.",
				fetchInput{},
				fetch,
				wickdocs.Docs{},
			),
			connector.Op(
				"query_database",
				"Query Database",
				"Read a database (collection) page and return its rows: each row is {id, title, cells:{column:value}}. Applies the view's filter + sort, so results match what the view shows. Dates, people, and relations are resolved to readable values.",
				queryDatabaseInput{},
				queryDatabase,
				wickdocs.Docs{},
			),
			connector.Op(
				"describe_database",
				"Describe Database",
				"Return a database's schema: every property's {name, type, writable, options}. For an embedded/linked view it also returns view_filter + a hint. Call this BEFORE create_page on a database so you know the exact property names, types, select options, and which property a new row must set to appear in the view.",
				describeDatabaseInput{},
				describeDatabase,
				wickdocs.Docs{},
			),
			connector.Op(
				"get_records",
				"Get Raw Records",
				"Fetch raw block records by ID (comma-separated). Returns the private API's block objects as-is — an escape hatch when fetch/query don't expose a field.",
				recordsInput{},
				getRecords,
				wickdocs.Docs{},
			),
			connector.Op(
				"list_blocks",
				"List Page Blocks",
				"List a page's top-level content blocks in order, each as {id, type, text, editable}. Call this BEFORE update_block/delete_block to get the id of the exact block to change — so you edit one block in place instead of rewriting the whole page. editable=false marks blocks whose text can't be set this way (images, embeds, tables, etc.).",
				listBlocksInput{},
				listBlocks,
				wickdocs.Docs{},
			),
		),
		connector.Cat(
			"Write",
			"Write via the private API's saveTransactions endpoint. To EDIT existing page content precisely, never rewrite the whole page: call list_blocks to get each block's id + type, then update_block (change one block's text/type) or delete_block (drop one block) on the id you want — everything else is left untouched. append_content adds new blocks at the end. To edit a database row's PROPERTIES (status, date, select, relation, …) use update_page_properties on the row id with a name→value JSON object (describe_database lists the exact names/types/options); only the cells you pass change. Comments are append-only: create_comment adds one; there is no edit/delete-comment op.",
			connector.Op(
				"create_page",
				"Create Page / Add Row",
				"Create a subpage under a page, OR add a row to a database (parent_type=database). For a database row, pass properties (JSON name→value) to fill columns — dates, selects, relations, etc. Call describe_database first to get the property names/types and the view filter. Returns {id, url} (+ skipped_properties for any unknown/read-only names).",
				createPageInput{},
				createPage,
				wickdocs.Docs{},
			),
			connector.Op(
				"create_comment",
				"Create Comment",
				"Add a page-level comment to a page or a database row (a row is a page). Comments are append-only — there is no way to edit or delete a comment through this connector, so post a correcting comment instead. Returns {id, discussion_id}.",
				createCommentInput{},
				createComment,
				wickdocs.Docs{},
			),
			connector.Op(
				"set_title",
				"Set Page Title",
				"Change a page's title (the H1/name at the top). For a database row this sets the row's Name/title cell. Does not touch the body — to edit body text use update_block. Returns {id, title}.",
				setTitleInput{},
				setTitle,
				wickdocs.Docs{},
			),
			connector.Op(
				"append_content",
				"Append / Insert Content",
				"ADD new blocks to a page's body from markdown. By default they go at the END; set after_block_id (from list_blocks) to insert right AFTER that block instead — that's how you add content in the MIDDLE of a page. Existing content is never touched — use this to add, not to rewrite; to change something already there use update_block/delete_block. Supports #/##/### headings, - / 1. / - [ ] lists, > quotes, fenced code, and --- dividers. Returns {page_id, added, block_ids} — the block_ids let you immediately edit or delete what you just added.",
				appendContentInput{},
				appendContent,
				wickdocs.Docs{},
			),
			connector.Op(
				"update_block",
				"Update Block",
				"EDIT one block in place, addressed by its own id from list_blocks — only that block changes, every other block on the page is left exactly as-is (this is how you fix a typo or rewrite a paragraph without replacing the whole page). Set text to the block's new content; optionally set type to convert it (e.g. text→sub_header). Refuses non-text blocks (image, embed, table, divider, …): run list_blocks first and target an editable=true block. Returns {id}.",
				updateBlockInput{},
				updateBlock,
				wickdocs.Docs{},
			),
			connector.Op(
				"delete_block",
				"Delete Block",
				"REMOVE one block from a page by its id (from list_blocks). Only that block is removed; the rest of the page stays. Combine with append_content to insert a corrected version, or with update_block to fix in place. Returns {id, deleted}.",
				deleteBlockInput{},
				deleteBlock,
				wickdocs.Docs{},
			),
			connector.Op(
				"update_page_properties",
				"Update Page Properties",
				"EDIT the PROPERTY cells of an existing database row in place (status, date, select, relation, checkbox, …) — only the properties you list change, every other cell and the row's body content is left exactly as-is. Pass properties as a JSON object of name→value (same shapes as create_page); call describe_database first for the exact names/types/options. Refuses a plain page (no property schema) — for a page title use set_title, for body content use update_block. Returns {id, updated, skipped_properties}.",
				updatePagePropertiesInput{},
				updatePageProperties,
				wickdocs.Docs{},
			),
		),
		connector.Cat(
			"Maintenance",
			"Backs the manager's config widgets (import form + connection status); not meant for agent use.",
			connector.OpConfigOnly(
				"connection_status",
				"Connection Status",
				"Probe LoadUserContent and report the logged-in user + workspace. Read-only; used by the manager UI's status widget.",
				pickerInput{},
				connectionStatus,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"import_form",
				"Import cURL Form",
				"Render the paste-a-cURL textarea + Extract button. Read-only; used by the manager UI's import widget.",
				importFormInput{},
				importForm,
				wickdocs.Docs{},
			),
			connector.OpConfigOnly(
				"import_curl_extract",
				"Extract from cURL",
				"Parse a pasted cURL and return the extracted config fields (token_v2, user_agent, …) plus a feedback line. Read-only; used by the manager UI's import widget Extract button.",
				importFormInput{},
				importExtract,
				wickdocs.Docs{},
			),
		),
	}
}
