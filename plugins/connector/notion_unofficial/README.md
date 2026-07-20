# Notion (Unofficial) connector

Reads and writes Notion through its **private web API** (`https://www.notion.so/api/v3`)
using a browser session cookie (`token_v2`) — no OAuth, no integration token.
Where the official `notion` connector is limited to what a bot integration is
shared, this one sees everything the logged-in user can see, and returns
MCP-style markdown.

> ⚠️ **Best-effort / undocumented.** The private API is not a public contract:
> it can change without notice, the cookie expires when the browser session
> ends, and it is a ToS grey area. Treat this connector as a convenience, not a
> guarantee. For anything durable, prefer the official `notion` connector.

## Known limitations / not yet handled

- **Formula & rollup columns are blank.** Notion computes them server-side and
  does not store them on the row, so they can't be read from the record. (They'd
  need the query's aggregation results — not wired.)
- **Write date/relation glyph is load-bearing.** Date/user/page cells use the
  `‣` (U+2023) mention glyph. The Go code writes it correctly; if you ever craft
  a raw value by hand (e.g. in a shell), a mangled `?` will be *accepted by the
  API but render blank in the Notion UI*.
- **Relation writes are one-directional here.** Setting a relation property makes
  the row match a `relation_contains` filter after Notion re-indexes (a few
  seconds); the dual back-reference is left to Notion.
- **No comment on a sub-range of text** — the private API anchors comments to a
  page/block, same as the official one.

## Auth (config)

| Field | Required | Notes |
|---|---|---|
| `import` (widget) | — | Paste a **Copy-as-cURL** of any `notion.so/api/v3` request from DevTools, click **Extract** → fills the fields below automatically. Easiest path. |
| `token_v2` | ✅ (secret) | The `token_v2` cookie from a logged-in session. Fetched by Extract or pasted manually. Expires with the session. |
| `active_user_id` | — | Sent as `x-notion-active-user-header`; only needed on multi-account sessions. |
| `user_agent` | — | Advanced. Browser UA sent with every request; defaults to a modern Chrome. |
| `notion_client_version` | — | Advanced. `Notion-Client-Version` header; sensible default baked in. |

## Operations

### Read
- **`fetch`** — download a page and render its whole body as **markdown** (block
  tree recursed). Embedded databases (inline **and** linked views) are expanded
  into markdown tables that respect the view's **filter + sort + visible
  columns**. Dates, people, and relations are resolved to readable values; each
  table gets a footer `_(db <id> · view filter: <Prop> <op> <value>)_`.
- **`query_database`** — return a database's rows as `{id, title, cells}`,
  applying the view's filter/sort.
- **`describe_database`** — return the schema: every property `{name, type,
  writable, options}`, plus `view_filter` + a hint for an embedded view. **Call
  this before `create_page` on a database** so you know exact property names,
  types, select options, and which property a new row must set to appear in the
  view.
- **`get_records`** — raw block records by id (escape hatch).

### Write (`saveTransactions`)
- **`create_page`** — a subpage under a page, or a **row in a database**
  (`parent_type=database`). For a row, pass `properties` (JSON `name → value`) to
  fill columns.
- **`create_comment`** — page-level comment on a page or a database row.
- **`set_title`** — rename a page.

### Maintenance (UI only, hidden from the agent)
- `import_form`, `import_curl_extract` — back the paste-a-cURL widget.
- `connection_status` — live connection card.

## Adding a database row (the intended flow)

1. `describe_database` on the database (or the page that embeds it) → get
   property names, types, options, and `view_filter`.
2. `create_page` with `parent_type=database`, `parent_id=<collection id>`,
   `title`, and `properties`:

   ```json
   {
     "Activity": "Debug",
     "Start time": "2026-07-17 06:00",
     "End time": "2026-07-17 07:00",
     "Ticket": "<host-page-id>"
   }
   ```

   Value formats: `select` = exact option; `multi_select` = comma-separated;
   `checkbox` = `true`/`false`; `date` = `YYYY-MM-DD` or `YYYY-MM-DD HH:MM`
   (range with ` → `); `relation`/`person` = comma-separated ids.
3. To make the row show up in a **filtered view**, set the property named in
   `view_filter` (e.g. a relation → the host page id). Notion re-indexes within
   a few seconds.

`create_page` returns `{id, url}` and, if any names were unknown or read-only,
`skipped_properties`.

## Read vs write

Read ops (`fetch`, `query_database`, `describe_database`, `get_records`) never
mutate — `queryCollection`/`loadPageChunk` are reads. Only the Write ops call
`saveTransactions`.
