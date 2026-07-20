---
outline: deep
---

# Notion

`notion` wraps the official [Notion REST API](https://developers.notion.com/) using an **Internal Integration** bot token — no OAuth dance. One instance = one integration; it sees only pages and databases that have been explicitly shared to it.

`fetch` is the MCP-style single-call read: it flattens a page's properties and walks the block tree into clean markdown, matching the shape of Notion's own MCP server.

| | |
|---|---|
| **Source** | [`plugins/connector/notion/`](https://github.com/yogasw/wick/tree/master/plugins/connector/notion) |
| **Key** | `notion` |
| **Icon** | 📝 |
| **Tier** | plugin — install with `<app> plugin install notion` |
| **Default tags** | `Connector`, `Productivity` |

See [Connector Plugins](/guide/connector-plugins) for the install flow.

## Configs

| Field | Type | Required | Notes |
|---|---|---|---|
| `Token` | secret | ✅ | Notion Internal Integration Secret (starts with `ntn_`). Create one at [notion.so/my-integrations](https://notion.so/my-integrations). |
| `Status` | widget | | Live connection status card — probes the token against `GET /v1/users/me` and shows the bot name + workspace. |

**Sharing pages.** The REST API only sees what's explicitly shared to the integration. For any page or database the connector needs, open it in Notion and add the integration via that page's **Connections** menu. A page not shared returns `404`, not `403` — that's Notion's permission model, not a connector bug.

All requests use API version `2022-06-28`.

## Operations

### Read

| Op | Input | What it does |
|---|---|---|
| `search` | `query`, `object_type`, `page_size` | Search pages/databases shared to the integration by title. Empty query lists everything shared. |
| `fetch` | `id`, `with_content`, `with_blocks` | Fetch one page or database by ID. For a page: flattened properties, plus (default on) the body rendered as markdown by walking the block tree. Set `with_blocks` on to also get `blocks[] = {id, type, text}` for targeting a specific block (e.g. to comment on it). For a database: title + normalized property schema. |
| `query_database` | `database_id`, `filter`, `sorts`, `page_size`, `limit` | Query a database's rows with an optional raw-JSON Notion filter/sort. Returns normalized rows `{id, url, title, properties}`. Pagination is followed automatically up to `limit`. |
| `get_comments` | `block_id` | List comments on a page or block. |
| `get_users` | `query` | List workspace users (people + bots), optionally filtered by name/email substring. |

### Write

| Op | Input | What it does |
|---|---|---|
| `create_page` | `parent_type`, `parent_id`, `title`, `properties`, `content` | Create a page — a row in a database or a subpage under a page. `properties` is raw JSON keyed by property name (Notion property-object form); `content` is markdown converted to blocks. Returns `{id, url}`. |
| `update_page` | `page_id`, `properties`, `append_md`, `archive` | Update a page's properties and/or append markdown to its body. `archive=true` moves it to trash and ignores the other fields. Returns `{id, url}`. |
| `create_comment` | `text`, `page_id`, `block_id`, `discussion_id` | Add a comment. Target precedence: `discussion_id` (reply into a thread) > `block_id` (comment on a specific block) > `page_id` (page-level). |
| `create_database` | `parent_page_id`, `title`, `schema` | Create a database under a page from a raw-JSON property schema (must include exactly one title property). |
| `update_data_source` | `database_id`, `title`, `properties` | Change a database's schema and/or title: add a property (new key), remove one (`null`), or rename one (`{"name":"New"}`). |

All write ops are destructive and disabled/enabled per row like any other connector — see [Connector Module ▶ Destructive ops](/guide/connector-module#destructive-ops-optdestructive).

### `fetch` output shape

`fetch` is built to be the one call an agent needs for "read this page":

- `meta` — flattened `{id, url, title, properties, edited_at}` (property values normalized to plain JSON, not Notion's verbose property-object form).
- `content_md` — the page body rendered as markdown (default on; turn off with `with_content=false` to save an API call when only properties are needed).
- `blocks` — flat `{id, type, text}` list, off by default; turn on with `with_blocks=true` when you need a block ID to target with `create_comment` or a future block-level edit.

## See also

- [Notion (Unofficial)](./notion_unofficial) — the private-web-API sibling connector. Use this official one first; reach for the unofficial connector only when you need to read content the bot integration hasn't been shared to.
- [Connector Module](/guide/connector-module) — module contract, per-instance AI description.
- [Connector Plugins](/guide/connector-plugins) — install / update / uninstall flow.
