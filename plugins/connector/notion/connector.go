package main

import (
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

const Key = "notion"

// notionVersion is the REST API version this connector is built against. Kept
// as 2022-06-28 (stable, one database = one schema) so property/block shapes
// match what repo.go's normalizers expect. Do not mix versions.
const notionVersion = "2022-06-28"

// Config is the connector's settings. Only a bot token is needed — no OAuth
// dance. The operator creates an Internal Integration at
// notion.so/my-integrations, copies the secret (ntn_…), and shares each target
// page/database to the integration via the page's Connections menu (REST returns
// 404, not 403, for anything not shared — that's the permission model).
type Config struct {
	Token string `wick:"secret;required;group=Authentication;desc=Notion Internal Integration Secret (starts with ntn_). Create one at notion.so/my-integrations and share your pages/databases to it via each page's Connections menu."`
	// Status is a read-only widget: it calls GET /v1/users/me live and shows the
	// bot name + workspace so the operator can confirm the token works. Not a
	// stored value — the config-only op renders it.
	Status string `wick:"html=connection_status;group=Authentication;desc=Live connection status: probes the token against Notion and shows the bot + workspace. Fill the token first."`
}

// --- per-operation input structs (one per op, self-documenting) ---

type searchInput struct {
	Query      string `wick:"desc=Text to match against page/database titles. Empty returns everything shared to the integration."`
	ObjectType string `wick:"dropdown=any|page|database;default=any;desc=Restrict results to pages, databases, or both."`
	PageSize   int    `wick:"desc=Max results to return. Default 25, max 100."`
}

type fetchInput struct {
	ID          string `wick:"required;desc=Page or database ID (dashed UUID or the 32-char id from a Notion URL)."`
	WithContent bool   `wick:"default=true;desc=For a page: return the body as clean markdown (content_md). Turn off to fetch properties only (1 API call, lightest)."`
	WithBlocks  bool   `wick:"default=false;desc=For a page: also return blocks[] = {id, type, text} so you can target a specific block for a comment or edit. Off by default to keep the response light — turn on only when you need block IDs."`
}

type queryDatabaseInput struct {
	DatabaseID string `wick:"required;desc=Database ID to query (dashed UUID or 32-char id)."`
	Filter     string `wick:"desc=Optional Notion filter object as raw JSON. Example: {\"property\":\"Status\",\"select\":{\"equals\":\"Doing\"}}"`
	Sorts      string `wick:"desc=Optional Notion sorts array as raw JSON. Example: [{\"property\":\"Priority\",\"direction\":\"descending\"}]"`
	PageSize   int    `wick:"desc=Rows per page. Default 100 (also the max). Pagination is followed automatically up to Limit."`
	Limit      int    `wick:"desc=Max rows to return across all pages. Default 100, max 1000."`
}

type createPageInput struct {
	ParentType string `wick:"required;dropdown=database|page;desc=Where to create the page. database = a row in a database; page = a subpage under a page."`
	ParentID   string `wick:"required;desc=ID of the parent database (for a row) or parent page (for a subpage)."`
	Title      string `wick:"required;desc=Page title. For a database row this fills the title property."`
	Properties string `wick:"desc=Optional extra properties as raw JSON, keyed by property NAME, in Notion property-object form. Example: {\"Status\":{\"select\":{\"name\":\"Doing\"}},\"Priority\":{\"number\":2}}"`
	Content    string `wick:"desc=Optional page body in markdown. Converted to Notion blocks (headings, lists, code, quotes, paragraphs)."`
}

type updatePageInput struct {
	PageID     string `wick:"required;desc=ID of the page to update."`
	Properties string `wick:"desc=Properties to change as raw JSON, keyed by property NAME. Unlisted properties are left untouched. Example: {\"Status\":{\"select\":{\"name\":\"Done\"}}}"`
	AppendMd   string `wick:"desc=Optional markdown appended to the end of the page body as new blocks."`
	Archive    bool   `wick:"desc=Set true to move the page to trash (archive). Ignores the other fields when set."`
}

type createDatabaseInput struct {
	ParentPageID string `wick:"required;desc=ID of the page the database is created under."`
	Title        string `wick:"required;desc=Database title."`
	Schema       string `wick:"required;desc=Property schema as raw JSON keyed by property name, Notion form. Must include exactly one title property. Example: {\"Name\":{\"title\":{}},\"Status\":{\"select\":{\"options\":[{\"name\":\"Todo\"},{\"name\":\"Done\"}]}},\"Priority\":{\"number\":{}}}"`
}

type updateDatabaseInput struct {
	DatabaseID string `wick:"required;desc=ID of the database whose schema/title to change."`
	Title      string `wick:"desc=New database title. Optional."`
	Properties string `wick:"desc=Schema changes as raw JSON keyed by property name. Add = new key; remove = set the key to null; rename = {\"name\":\"New\"}. Example: {\"Due\":{\"date\":{}},\"Old\":null}"`
}

// createCommentInput targets one of three destinations, in priority order:
// discussion_id (reply to a thread) → block_id (comment on a specific block/
// text) → page_id (page-level). Exactly one is used.
type createCommentInput struct {
	Text         string `wick:"required;desc=Comment text (plain text; sent as a single rich-text run)."`
	PageID       string `wick:"desc=Page to comment on (page-level comment). Used if block_id and discussion_id are empty."`
	BlockID      string `wick:"desc=Block ID to attach the comment to a specific block/text on a page. Takes priority over page_id."`
	DiscussionID string `wick:"desc=Existing discussion/thread ID to reply into. Takes priority over block_id and page_id."`
}

type getCommentsInput struct {
	BlockID string `wick:"required;desc=Page or block ID to read comments from."`
}

type getUsersInput struct {
	Query string `wick:"desc=Optional case-insensitive substring to filter users by name or email. Empty returns all workspace users."`
}

// pickerInput drives the html config-only op (connection_status). The manager's
// html widget always calls the backing op with the currently-selected value in
// an arg named "browser"; the status card ignores it.
type pickerInput struct {
	Browser string `wick:"desc=Unused; present because the html widget always sends it."`
}

// Module is the connector definition.
func Module() connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:         Key,
			Name:        "Notion",
			Description: "Read and write Notion pages, databases, and comments via the official REST API with a bot token. Fetch returns MCP-style markdown.",
			Icon:        "📝",
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
			"Search, fetch, and query Notion content. Fetch flattens properties and renders the page body as markdown, like the Notion MCP.",
			connector.Op(
				"search",
				"Search",
				"Search pages and databases shared to the integration by title. Returns a flat list of {id, title, type, url, last_edited}. Empty query lists everything shared.",
				searchInput{},
				search,
				wickdocs.Docs{},
			),
			connector.Op(
				"fetch",
				"Fetch Page or Database",
				"Fetch one page or database by ID. For a page: returns flattened properties plus (by default) the full body rendered as markdown by walking the block tree. For a database: returns the title and normalized property schema. This is the MCP-style single-call read.",
				fetchInput{},
				fetch,
				wickdocs.Docs{},
			),
			connector.Op(
				"query_database",
				"Query Database",
				"Query a database's rows with an optional Notion filter/sort (raw JSON — REST has no SQL). Returns normalized rows: each row is {id, url, title, properties:{name:value}}. Pagination is followed automatically up to the limit.",
				queryDatabaseInput{},
				queryDatabase,
				wickdocs.Docs{},
			),
			connector.Op(
				"get_comments",
				"Get Comments",
				"List comments on a page or block. Returns a flat list of {id, text, created_time, discussion_id}.",
				getCommentsInput{},
				getComments,
				wickdocs.Docs{},
			),
			connector.Op(
				"get_users",
				"Get Users",
				"List workspace users (people + bots), optionally filtered by a name/email substring. Returns {id, name, type, email}.",
				getUsersInput{},
				getUsers,
				wickdocs.Docs{},
			),
		),
		connector.Cat(
			"Write",
			"Create and modify Notion pages, databases, and comments.",
			connector.Op(
				"create_page",
				"Create Page",
				"Create a page — either a row in a database or a subpage under a page. Extra properties and a markdown body are optional. Returns {id, url}.",
				createPageInput{},
				createPage,
				wickdocs.Docs{},
			),
			connector.Op(
				"update_page",
				"Update Page",
				"Update a page's properties and/or append markdown to its body. Set archive=true to move the page to trash. Returns {id, url}.",
				updatePageInput{},
				updatePage,
				wickdocs.Docs{},
			),
			connector.Op(
				"create_comment",
				"Create Comment",
				"Add a comment. Target precedence: discussion_id (reply into a thread) > block_id (comment on a specific block/text) > page_id (page-level). Returns {id, discussion_id}.",
				createCommentInput{},
				createComment,
				wickdocs.Docs{},
			),
			connector.Op(
				"create_database",
				"Create Database",
				"Create a database under a page from a JSON property schema (must include one title property). Returns {id, url}.",
				createDatabaseInput{},
				createDatabase,
				wickdocs.Docs{},
			),
			connector.Op(
				"update_data_source",
				"Update Database Schema",
				"Change a database's schema and/or title: add a property (new key), remove one (null), or rename one ({\"name\":\"New\"}). Returns {id}.",
				updateDatabaseInput{},
				updateDatabase,
				wickdocs.Docs{},
			),
		),
		connector.Cat(
			"Maintenance",
			"Backs the manager's connection-status widget; not meant for agent use.",
			connector.OpConfigOnly(
				"connection_status",
				"Connection Status",
				"Probe GET /v1/users/me and report the bot name + workspace. Read-only; used by the manager UI's status widget.",
				pickerInput{},
				connectionStatus,
				wickdocs.Docs{},
			),
		),
	}
}
