---
outline: deep
---

# Google Workspace

`google_workspace` wraps the Google Drive, Sheets, Docs, Slides, Gmail, Calendar, and Meet REST APIs behind one connector. One instance = one Google account, authenticated via the **Connect Account** OAuth2 button — a single token grants access to all seven products.

This replaces the older code-only `google_drive` connector. The connector now ships **37 operations across 7 categories**.

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

The Connect Account flow asks for all of the scopes below; the health check verifies which were actually granted and enables ops accordingly:

| Scope | Enables |
|---|---|
| `https://www.googleapis.com/auth/drive` | All Drive ops + Workspace file creation (`create_doc` / `create_sheet` / `create_slides`). |
| `https://www.googleapis.com/auth/spreadsheets` | All `sheets_*` ops. |
| `https://www.googleapis.com/auth/documents` | All `docs_*` ops. |
| `https://www.googleapis.com/auth/presentations` | All `slides_*` ops. |
| `https://www.googleapis.com/auth/gmail.modify` | `gmail_list_messages`, `gmail_get_message`, `gmail_modify_labels`. |
| `https://www.googleapis.com/auth/gmail.send` | `gmail_send`, `gmail_reply`. |
| `https://www.googleapis.com/auth/gmail.compose` | `gmail_create_draft`. |
| `https://www.googleapis.com/auth/calendar` | All `calendar_*` ops (full read/write). |
| `https://www.googleapis.com/auth/meetings.space.readonly` | All `meet_*` ops (read-only). |
| `https://www.googleapis.com/auth/userinfo.email` | Identifies the connected account (per-user token mapping). |

The health check treats a write scope as a superset of its read-only variant. Read-only ops are satisfied by either the read-only or full scope.

> **Existing connected accounts must re-click Connect Account** to grant the new Gmail, Calendar, and Meet scopes added in v0.22.0. Until re-consent, ops requiring those scopes are flagged as `needs scope: …` in the health check and will not execute.

## Operations — Drive

### Read

| Op | Input | What it does |
|---|---|---|
| `list_files` | `folder_id`, `page_size`, `order_by` | List files/folders. Returns ID, name, MIME type, modified time, size, web view link. Empty `folder_id` lists My Drive root. |
| `search_files` | `query`, `page_size` | Search using Drive query syntax (e.g. `name contains 'report'`). First page only. |
| `get_file_info` | `file_id` | Metadata for one file/folder: name, MIME type, size, owner email, sharing state, parent IDs, web view link. |
| `get_file_content` | `file_id` | Read text content. Docs → plain text, Sheets → CSV, Slides → plain text, other files → raw bytes (first 100 KB). |

### Write (destructive, opt-in per row)

| Op | Input | What it does |
|---|---|---|
| `upload_file` | `name`, `content`, `folder_id`, `mime_type` | Upload a new file from plain-text content. Returns ID + web view link. Default MIME type `text/plain`. |
| `create_folder` | `name`, `parent_folder_id` | Create a folder. Returns ID + web view link. |
| `delete_file` | `file_id` | Move a file/folder to trash (reversible within 30 days via the Drive UI — not a permanent delete). |
| `share_file` | `file_id`, `email`, `role` | Grant access by email. Role: `reader`, `writer`, or `commenter`. Returns the permission ID. |
| `create_doc` | `name`, `folder_id`, `content` | Create a Google Doc, optionally with initial plain-text body. Returns ID + web view link. |
| `create_sheet` | `name`, `folder_id`, `csv_data` | Create a Google Sheet, optionally pre-populated from a CSV string. Returns ID + web view link. |
| `create_slides` | `name`, `folder_id`, `first_slide_text` | Create a Google Slides deck, optionally setting the first slide's title. Returns ID + web view link. |

## Operations — Sheets (Sheets API v4)

| Op | Input | What it does |
|---|---|---|
| `sheets_read_range` | `file_id`, `range` | Read cell values from an A1 range (e.g. `Sheet1!A1:C10`). Returns rows as a JSON array + row count. |
| `sheets_append_rows` | `file_id`, `range`, `csv_data` | Append CSV rows after the last existing row in the table. Does not overwrite. |
| `sheets_update_range` | `file_id`, `range`, `csv_data` | Overwrite an A1 range with CSV data. Existing values in the range are replaced. |
| `sheets_clear_range` | `file_id`, `range` | Clear values from an A1 range. Formatting is preserved; only values are removed. |

## Operations — Docs (Docs API v1)

| Op | Input | What it does |
|---|---|---|
| `docs_append_text` | `file_id`, `text` | Append plain text at the end of the document. |
| `docs_replace_text` | `file_id`, `find`, `replace`, `match_case` | Find-and-replace all occurrences throughout the doc. Case-insensitive by default. Returns the number of replacements. |

## Operations — Slides (Slides API v1)

| Op | Input | What it does |
|---|---|---|
| `slides_get_content` | `file_id` | Get every slide's index, title, and body text, plus the deck title and slide count. |
| `slides_add_slide` | `file_id`, `title`, `body`, `layout`, `insert_at_index` | Add a slide with optional title/body. Layout: `TITLE_AND_BODY` (default), `BLANK`, or `TITLE_ONLY`. `insert_at_index` 0-based; default appends. Returns the new slide ID + index. |
| `slides_duplicate_slide` | `file_id`, `slide_index` | Duplicate a slide by 0-based index. The copy is inserted immediately after the original. |

## Operations — Gmail

All Gmail ops require at least one Gmail scope (see scopes table). `gmail_send`, `gmail_create_draft`, `gmail_reply`, and `gmail_modify_labels` are destructive and opt-in per row.

### Read

| Op | Input | What it does |
|---|---|---|
| `gmail_list_messages` | `query`, `max_results` | Search the mailbox using Gmail query syntax (e.g. `from:alice@abc.com is:unread newer_than:7d`). Returns id, thread_id, from, to, subject, date, snippet. Leave `query` empty to list the whole mailbox. Default `max_results`: 20 (max 100). |
| `gmail_get_message` | `message_id` | Read a single message in full: headers (from, to, cc, subject, date), labels, and the plain-text body. |

### Write (destructive, opt-in per row)

| Op | Input | What it does |
|---|---|---|
| `gmail_send` | `to`, `cc`, `subject`, `body` | Send a new plain-text email. `to` is a comma-separated list. Returns the sent message ID and thread ID. This delivers mail immediately — it is not a draft. |
| `gmail_create_draft` | `to`, `cc`, `subject`, `body` | Create a draft without sending. Returns the draft ID and message ID. The user can review and send it later from Gmail. |
| `gmail_reply` | `message_id`, `body` | Reply to an existing message within its thread. `Re:` prefix and threading headers are set automatically; reply goes to the original sender. Returns the sent message ID. |
| `gmail_modify_labels` | `message_id`, `add_labels`, `remove_labels` | Add and/or remove labels on a message. System labels: `UNREAD`, `STARRED`, `IMPORTANT`, `INBOX`, `SPAM`, `TRASH`. Remove `UNREAD` to mark read; remove `INBOX` to archive. At least one of `add_labels` or `remove_labels` is required. Returns the message's current label set. |

## Operations — Calendar

All Calendar ops require the `calendar` scope. Create/update/delete/RSVP ops are destructive and opt-in per row.

### Read

| Op | Input | What it does |
|---|---|---|
| `calendar_list_calendars` | _(none)_ | List all calendars on the account. Returns id, summary, description, primary flag, and access role. Use a calendar id with the other Calendar ops. |
| `calendar_list_events` | `calendar_id`, `time_min`, `time_max`, `query`, `max_results` | List events in a calendar within an optional time window. `time_min` / `time_max` are RFC3339 strings. Returns id, summary, start, end, attendees, and meet_link for each. Default calendar: `primary`. Default `max_results`: 50 (max 250). |
| `calendar_get_event` | `calendar_id`, `event_id` | Read a single event in full: summary, description, location, start/end, attendees with RSVP status, and Meet link if any. |

### Write (destructive, opt-in per row)

| Op | Input | What it does |
|---|---|---|
| `calendar_create_event` | `calendar_id`, `summary`, `description`, `location`, `start`, `end`, `attendees`, `add_meet` | Create a calendar event. `start` / `end` are RFC3339 (or a date string for all-day events). Set `add_meet=true` to attach a Google Meet video link (returned in `meet_link`). `attendees` is comma-separated emails. Returns the created event. |
| `calendar_update_event` | `calendar_id`, `event_id`, `summary`, `description`, `location`, `start`, `end`, `attendees` | Patch an existing event. Only non-empty fields are applied. Replaces the full attendee list if `attendees` is provided. Attendees are notified of changes. Returns the updated event. |
| `calendar_delete_event` | `calendar_id`, `event_id` | Cancel and delete an event. Attendees are notified of the cancellation. Not reversible via this connector. |
| `calendar_respond_event` | `calendar_id`, `event_id`, `response` | Set your RSVP on an event you were invited to. `response`: `accepted`, `declined`, or `tentative`. Returns the event ID and your new response status. |

## Operations — Meet

All Meet ops require the `meetings.space.readonly` scope. All are read-only; no write ops exist in this category. To create a Meet link for a new meeting, use `calendar_create_event` with `add_meet=true`.

| Op | Input | What it does |
|---|---|---|
| `meet_get_space` | `space` | Get a Meet space's config and active conference by resource name (e.g. `spaces/abc`), meeting code, or full Meet URL. Returns meeting_uri, meeting_code, access_type, and the active conference record if a call is live. |
| `meet_list_conference_records` | `filter`, `page_size` | List past meetings (conference records), optionally filtered by Meet filter syntax (e.g. `space.meeting_code="abc-defg-hij"` or `start_time>="2026-06-01T00:00:00Z"`). Returns name, start_time, end_time, and space per record. Default `page_size`: 25 (max 100). Use a record name with the recordings / transcripts ops. |
| `meet_list_recordings` | `conference_record` | List recordings for a conference record. Returns name, state, start/end time, and the Drive file id of each recording (when available). |
| `meet_list_transcripts` | `conference_record` | List transcripts for a conference record. Returns name, state, start/end time, and the Google Docs document id of each transcript (when available). |

## Quirks worth knowing

- **One token, seven APIs.** The health check (**Test Integration**) reflects which APIs are usable based on the scopes actually granted on the consent screen. If a scope is missing for a given op, the op reports `needs scope: …` and will not run. Re-run Connect Account to widen scopes.
- **Existing accounts need re-consent.** Accounts connected before v0.22.0 lack the Gmail, Calendar, and Meet scopes. Click Connect Account again on the connector row to re-run the consent screen.
- `create_sheet` / `sheets_append_rows` / `sheets_update_range` take **CSV strings** (one row per line; quoted fields and embedded newlines are handled). They do not accept JSON arrays.
- `list_files`, `search_files`, and `gmail_list_messages` return **first page only**. For deeper pagination, loop in your workflow or call `httprest` against the API directly.
- `delete_file` trashes, it does not permanently delete — the owner can restore within 30 days.
- `calendar_delete_event` is **not reversible** via this connector (unlike Drive trash).
- IDs are Google **resource IDs** (the long string in a Drive/Docs/Sheets/Slides URL, or the opaque ID from a list op). Resolve names to IDs with a list or search op first.
- `calendar_create_event` with `add_meet=true` uses the Calendar `conferenceData` API to attach a Meet link. The `meet_link` field in the response contains the generated `meet.google.com` URL.

## See also

- [Connector Module](/guide/connector-module) — module contract, file layout, `wick:"..."` tag grammar.
- [MCP for LLMs](/guide/mcp) — `wick_list` / `wick_get` / `wick_execute` flow.
- [HTTP / REST](./httprest) — fallback for any Google API call wick hasn't typed yet.
- [Encrypted Fields](/reference/encrypted-fields) — how the `secret`-tagged token fields are stored and round-tripped.
