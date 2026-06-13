---
outline: deep
---

# Google Workspace

`google_workspace` wraps the Google Drive, Sheets, Docs, and Slides REST APIs behind one connector. One instance = one Google account, authenticated via the **Connect Account** OAuth2 button — a single token grants access to all four products.

This replaces the older code-only `google_drive` connector. The eight Drive operations carry over unchanged; twelve new ops add native file creation (Docs / Sheets / Slides) plus read/write access to Sheets, Docs, and Slides content. Anything not yet covered is one [`httprest`](./httprest) call away.

| | |
|---|---|
| **Source** | [`internal/connectors/googleworkspace/`](https://github.com/yogasw/wick/tree/master/internal/connectors/googleworkspace) |
| **Key** | `google_workspace` |
| **Icon** | 🗂️ |
| **Tier** | builtin (every wick app) |
| **Health check** | ✅ — probes the token's granted scopes via Google `tokeninfo` and reports per-op availability |
| **OAuth** | ✅ — per-instance Google OAuth app credentials live on this row |

## Configs

The admin sets `ClientID` / `ClientSecret` (from a Google Cloud Console OAuth client); `UserToken` / `RefreshToken` are auto-filled when the operator clicks **Connect Account** and completes the Google consent screen. The connector requests offline access (`access_type=offline`, `prompt=consent`) so it obtains a refresh token and auto-renews the 1-hour access token.

| Field | Type | Required | Notes |
|---|---|---|---|
| `ClientID` | string | ✅ | OAuth Client ID from Google Cloud Console. Required to activate the **Connect Account** button. |
| `ClientSecret` | secret | ✅ | OAuth Client Secret. Used in the token-exchange step. |
| `UserToken` | secret | | Access token — auto-filled via Connect Account. |
| `RefreshToken` | secret | | Refresh token for auto-renewal — auto-filled via Connect Account. |

### OAuth scopes requested

The Connect Account flow asks for these scopes; the health check verifies which were actually granted and enables ops accordingly:

| Scope | Enables |
|---|---|
| `https://www.googleapis.com/auth/drive` | All Drive ops + Workspace file creation (`create_doc` / `create_sheet` / `create_slides`). |
| `https://www.googleapis.com/auth/spreadsheets` | All `sheets_*` ops. |
| `https://www.googleapis.com/auth/documents` | All `docs_*` ops. |
| `https://www.googleapis.com/auth/presentations` | All `slides_*` ops. |
| `https://www.googleapis.com/auth/userinfo.email` | Identifies the connected account (per-user token mapping). |

The health check treats a write scope as a superset of its read-only variant (granting `drive` satisfies `drive.readonly`, `spreadsheets` satisfies `spreadsheets.readonly`, `presentations` satisfies `presentations.readonly`). Read-only ops (`list_files`, `search_files`, `get_file_info`, `get_file_content`, `sheets_read_range`, `slides_get_content`) are satisfied by either the read-only or full scope.

## Operations (read)

Non-destructive — enabled by default.

| Op | Product | Input | What it does |
|---|---|---|---|
| `list_files` | Drive | `folder_id`, `page_size`, `order_by` | List files/folders. Returns ID, name, MIME type, modified time, size, web view link. Empty `folder_id` lists My Drive root. |
| `search_files` | Drive | `query`, `page_size` | Search using Drive query syntax (e.g. `name contains 'report'`). First page only. |
| `get_file_info` | Drive | `file_id` | Metadata for one file/folder: name, MIME type, size, owner email, sharing state, parent IDs, web view link. |
| `get_file_content` | Drive | `file_id` | Read text content. Docs → plain text, Sheets → CSV, Slides → plain text, other files → raw bytes (first 100 KB). |
| `sheets_read_range` | Sheets | `file_id`, `range` | Read cell values from an A1 range (e.g. `Sheet1!A1:C10`). Returns rows as a JSON array + row count. |
| `slides_get_content` | Slides | `file_id` | Get every slide's index, title, and body text, plus the deck title and slide count. |

## Operations (write — destructive, opt-in per row)

Every op below is destructive. The MCP layer appends a destructive warning to the description so the LLM confirms before calling. Disable individual ops per (row, op) at `/manager/connectors/google_workspace/{id}`.

### Drive — files & Workspace file creation

| Op | Input | What it does |
|---|---|---|
| `upload_file` | `name`, `content`, `folder_id`, `mime_type` | Upload a new file from plain-text content. Returns ID + web view link. Default MIME type `text/plain`. |
| `create_folder` | `name`, `parent_folder_id` | Create a folder. Returns ID + web view link. |
| `delete_file` | `file_id` | Move a file/folder to trash (reversible within 30 days via the Drive UI — not a permanent delete). |
| `share_file` | `file_id`, `email`, `role` | Grant access by email. Role: `reader`, `writer`, or `commenter`. Returns the permission ID. |
| `create_doc` | `name`, `folder_id`, `content` | Create a Google Doc, optionally with initial plain-text body. Returns ID + web view link. |
| `create_sheet` | `name`, `folder_id`, `csv_data` | Create a Google Sheet, optionally pre-populated from a CSV string. Returns ID + web view link. |
| `create_slides` | `name`, `folder_id`, `first_slide_text` | Create a Google Slides deck, optionally setting the first slide's title. Returns ID + web view link. |

### Sheets (Sheets API v4)

| Op | Input | What it does |
|---|---|---|
| `sheets_append_rows` | `file_id`, `range`, `csv_data` | Append CSV rows after the last existing row in the table. Does not overwrite. |
| `sheets_update_range` | `file_id`, `range`, `csv_data` | Overwrite an A1 range with CSV data. Existing values in the range are replaced. |
| `sheets_clear_range` | `file_id`, `range` | Clear values from an A1 range. Formatting is preserved; only values are removed. |

### Docs (Docs API v1)

| Op | Input | What it does |
|---|---|---|
| `docs_append_text` | `file_id`, `text` | Append plain text at the end of the document. |
| `docs_replace_text` | `file_id`, `find`, `replace`, `match_case` | Find-and-replace all occurrences throughout the doc. Case-insensitive by default. Returns the number of replacements. |

### Slides (Slides API v1)

| Op | Input | What it does |
|---|---|---|
| `slides_add_slide` | `file_id`, `title`, `body`, `layout`, `insert_at_index` | Add a slide with optional title/body. Layout: `TITLE_AND_BODY` (default), `BLANK`, or `TITLE_ONLY`. `insert_at_index` 0-based; default appends to the end. Returns the new slide ID + index. |
| `slides_duplicate_slide` | `file_id`, `slide_index` | Duplicate a slide by 0-based index. The copy is inserted immediately after the original. |

## Quirks worth knowing

- **One token, four APIs.** The health check (**Test Integration**) reflects which APIs are usable based on the scopes actually granted on the consent screen — if a user only approved Drive, the `sheets_*` / `docs_*` / `slides_*` ops report `needs scope: …` and won't run. Re-run Connect Account to widen scopes.
- `create_sheet` / `sheets_append_rows` / `sheets_update_range` take **CSV strings** (one row per line; quoted fields and embedded newlines are handled). They don't accept JSON arrays.
- `list_files` and `search_files` return the **first page only**. For deeper history, loop in your workflow or call `httprest` against the Drive API directly.
- `delete_file` trashes, it does not permanently delete — the owner can restore within 30 days.
- IDs are Google **file IDs** (the long string in a Drive/Docs/Sheets/Slides URL), not file names. Resolve names to IDs with `search_files` first.

## See also

- [Connector Module](/guide/connector-module) — module contract, file layout, `wick:"..."` tag grammar.
- [MCP for LLMs](/guide/mcp) — `wick_list` / `wick_get` / `wick_execute` flow.
- [HTTP / REST](./httprest) — fallback for any Google API call wick hasn't typed yet.
- [Encrypted Fields](/reference/encrypted-fields) — how the `secret`-tagged token fields are stored and round-tripped.
