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

## Importing credentials from a cURL

The connector authenticates with your browser's `token_v2` cookie. The easiest way to hand it over is to copy any live `notion.so/api/v3` request as a cURL and let the **Import** widget parse it — one paste fills `TokenV2`, `UserAgent`, `NotionClientVersion`, and `ActiveUserID` for you.

1. Open Notion in your browser (`app.notion.com` / `notion.so`) while logged in.
2. Open **DevTools** (`F12` or `Ctrl/Cmd+Shift+I`) → **Network** tab.
3. In the filter box type `v3` so only private-API calls show. Click around Notion (open a page) so a request appears — e.g. `getTeamsV2`, `syncRecordValues`, `loadPageChunk`. Any `notion.so/api/v3` request works.
4. **Right-click** the request → **Copy** → **Copy as cURL (bash)**.

   ![DevTools Network tab filtered to v3, right-click a request, Copy → Copy as cURL (bash)](/screenshots/notion-unofficial-copy-curl.png)

   ::: tip Which cURL format?
   On Windows both **Copy as cURL (bash)** and **Copy as cURL (cmd)** are accepted — the widget parses both single-quote (bash) and double-quote (cmd) styles. Prefer **bash** when offered.
   :::
5. In the connector's config, paste the copied cURL into the **Import** textarea and click **Extract**. The fields below fill in automatically.
6. Confirm the **Status** widget shows **Connected** as _your user_ + workspace.

::: warning The cookie expires
`token_v2` is a session cookie: it dies when you log out, change your password, or the session rotates. When requests start returning `401 not authenticated`, repeat the steps above with a fresh cURL. There is no refresh — the private API has no token-refresh flow.
:::

You can also fill `TokenV2` by hand: **DevTools → Application → Cookies → `https://www.notion.so` → `token_v2`**. Import-from-cURL is preferred because it also captures the matching `User-Agent` and client-version headers, which makes requests blend in with your real session.

## Configs

| Field | Required | Notes |
|---|---|---|
| `Import` (widget) | | Paste a **Copy-as-cURL** of any `notion.so/api/v3` request from browser DevTools, click **Extract** — it parses the curl and fills the fields below automatically. See [Importing credentials from a cURL](#importing-credentials-from-a-curl). The easiest way to set this connector up. |
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
| `list_blocks` | `page_id` | List a page's top-level content blocks in order, each as `{id, type, text, editable}`. **Call this before `update_block`/`delete_block`** to get the ID of the exact block to change. `editable: false` marks blocks whose text can't be rewritten in place (images, embeds, tables, dividers, …). |

### Write

| Op | Input | What it does |
|---|---|---|
| `create_page` | `parent_type`, `parent_id`, `title`, `properties` | Create a subpage under a page, or add a row to a database (`parent_type=database`). For a row, `properties` is JSON `name → value`; call `describe_database` first for the exact names/types. Returns `{id, url}` (+ `skipped_properties` for any unknown/read-only names). |
| `create_comment` | `page_id`, `text` | Add a page-level comment to a page or a database row (a row is a page). Comments are **append-only** — no edit/delete-comment op. |
| `set_title` | `page_id`, `title` | Rename a page (the H1 / row Name). Does not touch the body. |
| `append_content` | `page_id`, `markdown`, `after_block_id` | Add new blocks from markdown. By default they go at the **end**; set `after_block_id` (from `list_blocks`) to insert right **after** that block — i.e. in the **middle** of the page. Existing content is never touched. Returns `{page_id, added, block_ids}`. |
| `update_block` | `block_id`, `text`, `type` | Rewrite **one** block's text in place, addressed by its ID from `list_blocks` — every other block stays exactly as-is. Optionally set `type` to convert the block (e.g. `text → sub_header`). Refuses non-text blocks. Returns `{id}`. |
| `delete_block` | `page_id`, `block_id` | Remove **one** block by its ID (from `list_blocks`). Only that block goes; the rest of the page stays. Returns `{id, deleted}`. |

Read ops never mutate; only the write ops above call the private API's `saveTransactions` endpoint.

### Editing page content in place

To change something already on a page **without rewriting the whole page**, always work per-block:

1. `list_blocks` → find the block you want by its `text`/`type`, note its `id`, and check `editable`.
2. Then one of:
   - **Fix / rewrite** that block → `update_block` with its `id` (only that block changes).
   - **Remove** it → `delete_block` with its `id`.
   - **Insert near it** → `append_content` with `after_block_id` = its `id`.

`append_content`'s markdown supports `#`/`##`/`###` headings, `-` / `1.` / `- [ ]` lists, `>` quotes, fenced code, and `---` dividers. Inline `**bold**` / `` `code` `` / links are stored as plain text.

Two guards keep an edit from breaking the page:

- **`update_block` refuses non-text blocks.** Images, embeds, tables, dividers, and page links have no editable title — `list_blocks` marks them `editable: false` and `update_block` returns an error naming the editable types. Pick an `editable: true` block.
- **`append_content` validates `after_block_id`.** An anchor that isn't a top-level block of the target page is rejected (instead of silently mis-placing the new blocks). Always pass an ID that `list_blocks` returned for that page.

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
- **Block editing is top-level only.** `list_blocks`, `update_block`, and `delete_block` operate on a page's direct children. Blocks nested inside a toggle or a column are not enumerated, so they can't be targeted by ID yet.
- **`append_content` stores plain text.** Inline markdown (`**bold**`, `` `code` ``, links) inside a line is not parsed into rich text — it's kept as literal characters within the block.

## See also

- [Notion](./notion) — the official REST API connector. Prefer it when the content only needs a bot-shared page/database and durability matters more than reach.
- [Connector Module](/guide/connector-module) — module contract, per-instance AI description.
- [Connector Plugins](/guide/connector-plugins) — install / update / uninstall flow.
