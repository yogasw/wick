---
outline: deep
---

# Notion (Unofficial)

`notion_unofficial` reads and writes Notion through its **private web API** (`notion.so/api/v3`) using a browser session cookie (`token_v2`) — no OAuth, no integration token. Where the official [`notion`](./notion) connector only sees what's shared to a bot integration, this one sees everything the logged-in user can see.

::: warning Best-effort / undocumented
The private API is not a public contract: it can change without notice, the cookie expires when the browser session ends, and using it is a ToS grey area. Treat this connector as a convenience, not a guarantee. Prefer the official [`notion`](./notion) connector for anything durable.
:::

| | |
|---|---|
| **Source** | [`plugins/connector/notion_unofficial/`](https://github.com/yogasw/wick/tree/master/plugins/connector/notion_unofficial) |
| **Key** | `notion_unofficial` |
| **Icon** | 📓 |
| **Tier** | plugin — install with `<app> plugin install notion_unofficial` |
| **Default tags** | `Connector`, `Productivity` |

See [Connector Plugins](/guide/connector-plugins) for the install flow.

## Configs

| Field | Required | Notes |
|---|---|---|
| `Import` (widget) | | Paste a **Copy-as-cURL** of any `notion.so/api/v3` request from browser DevTools, click **Extract** — it parses the curl and fills the fields below automatically. The easiest way to set this connector up. |
| `TokenV2` | ✅ (secret) | The `token_v2` cookie from a logged-in `notion.so` session (DevTools → Application → Cookies → `token_v2`). Filled by Extract, or paste manually. Expires when the browser session ends. |
| `ActiveUserID` | | Sent as `x-notion-active-user-header`; only needed on sessions with multiple Notion accounts. |
| `UserAgent` | | Advanced. Browser User-Agent sent with every request — leave blank for a modern-Chrome default. |
| `NotionClientVersion` | | Advanced. `Notion-Client-Version` header; a sensible default is baked in. |
| `Status` (widget) | | Live connection status card — probes the cookie and shows the logged-in user + workspace. |

## Operations

### Read

| Op | Input | What it does |
|---|---|---|
| `fetch` | `page_id` | Download a page and render its whole body as markdown (block tree recursed) — the MCP-style single-call read. Embedded databases (inline **and** linked views) are expanded into markdown tables that respect the view's filter, sort, and visible columns; dates, people, and relations resolve to readable values. Each table gets a footer noting the source database ID and view filter. |
| `query_database` | `page_id`, `limit` | Return a database's rows as `{id, title, cells}`, applying the view's filter and sort. |
| `describe_database` | `page_id` | Return the schema: every property's `{name, type, writable, options}`, plus the view's filter. **Call this before `create_page` on a database** to get exact property names, types, select options, and which property a new row must set to appear in a filtered view. |
| `get_records` | `ids` | Fetch raw block records by comma-separated ID — an escape hatch when `fetch`/`query_database` don't expose a field. |

### Write

| Op | Input | What it does |
|---|---|---|
| `create_page` | `parent_type`, `parent_id`, `title`, `properties` | Create a subpage under a page, or add a row to a database (`parent_type=database`). For a row, `properties` is JSON `name → value`; call `describe_database` first for the exact names/types. Returns `{id, url}` (+ `skipped_properties` for any unknown/read-only names). |
| `create_comment` | `page_id`, `text` | Add a page-level comment to a page or a database row (a row is a page). |
| `set_title` | `page_id`, `title` | Rename a page. |

Read ops never mutate; only the write ops above call the private API's `saveTransactions` endpoint.

### Writing database row properties

`properties` values are plain strings in a format-per-type convention:

- `select` — exact option name
- `multi_select` — comma-separated option names
- `checkbox` — `true` / `false`
- `date` — `YYYY-MM-DD` or `YYYY-MM-DD HH:MM`, range with ` → `
- `relation` / `person` — comma-separated IDs

```json
{
  "Activity": "Debug",
  "Start time": "2026-07-17 06:00",
  "End time": "2026-07-17 07:00",
  "Ticket": "<host-page-id>"
}
```

To make a new row show up in a **filtered view**, set the property named in `describe_database`'s `view_filter` (e.g. a relation pointing at the host page). Notion re-indexes within a few seconds.

## Known limitations

- **Formula and rollup columns are blank.** Notion computes them server-side and doesn't store them on the row.
- **Relation writes are one-directional.** Setting a relation property makes the row match a `relation_contains` filter after Notion re-indexes; the dual back-reference is left to Notion itself.
- **No comment on a sub-range of text** — comments anchor to a page/block, same as the official connector.

## See also

- [Notion](./notion) — the official REST API connector. Prefer it when the content only needs a bot-shared page/database and durability matters more than reach.
- [Connector Module](/guide/connector-module) — module contract, per-instance AI description.
- [Connector Plugins](/guide/connector-plugins) — install / update / uninstall flow.
