# Ticket Integrations

Wire a project's ticket board to another system, in both directions:

- **Outbound — webhooks.** Wick POSTs a JSON event to your endpoint whenever a
  ticket is created, moved, assigned, or deleted.
- **Inbound — REST API.** Your system creates and updates tickets with a
  Personal Access Token.
- **Custom buttons.** A button on every ticket's page that POSTs the ticket to
  your URL on click — see [Custom buttons](#custom-buttons).

All three are configured per project, under **Project settings → Ticket
system → Integrations**. All are off (empty) until you add one.

## Setup

1. Open **Project settings → Ticket system**, make sure ticket mode is on.
2. Expand **Integrations**.
3. For the API: switch **REST API** on. Copy the base URL shown there.
4. For webhooks: **Add webhook**, fill in the URL, set a signing secret, pick
   the events, then **Send test** to prove the endpoint before a real ticket
   depends on it.
5. For a custom button: **Add button**, fill in a label and a URL, and save —
   see [Custom buttons](#custom-buttons).

You need a Personal Access Token for the REST API. Create one at
`/profile/tokens` — see [Access Tokens](/guide/access-tokens). A token acts as
you: it reaches exactly the projects your user can see, and no others.

::: warning The API toggle is per project
A token cannot touch a project whose REST API is switched off — those requests
answer `403` with `the REST API is disabled for this project`. A project you
cannot see at all answers `403 you don't have access to this project`; one
that does not exist answers `404`.
:::

---

# REST API

## Base URL and auth

```
https://<your-wick-host>/api
```

Every endpoint path in this reference is **relative to this base URL** —
append it as-is. `GET /projects/{id}/tickets` means:

```
https://<your-wick-host>/api/projects/{id}/tickets
```

Every request carries the token:

```bash
-H "Authorization: Bearer $WICK_TOKEN"
```

Set up the shell for every example below:

```bash
export WICK_HOST="https://wick.abc.com"
export WICK_API="$WICK_HOST/api"
export WICK_TOKEN="wick_pat_..."
export PROJECT="proj_7f21c9"
```

## Errors

Failures are JSON with a single `error` key.

```json
{ "error": "invalid status in_review (want open, in_progress, waiting, done)" }
```

| Status | Meaning |
|---|---|
| `400` | Malformed JSON, or a value the board rejects (unknown status, empty title). |
| `401` | Missing, malformed, revoked, or unapproved token. |
| `403` | `you don't have access to this project` — the project exists but your user is not its owner and has no tag grant. Or `the REST API is disabled for this project` — switch it on under Project settings → Ticket system → Integrations. |
| `404` | Ticket or project does not exist. |
| `500` | Server-side failure writing the ticket. |

---

## List tickets

```
GET /projects/{projectID}/tickets
```

| Query | Default | Meaning |
|---|---|---|
| `rows` | `3` | Session rows per card. `0` returns counts only. |
| `statuses` | all | Comma-separated columns to include. `?statuses=` returns none. |
| `assignee` | everyone | A user id, or `me`. |
| `untracked` | `0` | `1` also returns sessions with no ticket. |
| `untracked_limit` | — | Caps that list. |

```bash
curl -s "$WICK_API/projects/$PROJECT/tickets?rows=0" \
  -H "Authorization: Bearer $WICK_TOKEN"
```

Only the tickets that are open and assigned to you:

```bash
curl -s "$WICK_API/projects/$PROJECT/tickets?statuses=open,in_progress&assignee=me" \
  -H "Authorization: Bearer $WICK_TOKEN"
```

## Create a ticket

```
POST /projects/{projectID}/tickets
```

| Field | Required | Notes |
|---|---|---|
| `title` | yes | Trimmed; must not be empty. |
| `body` | no | Markdown description. Rendered as markdown on the ticket page. |
| `id` | no | Adopt an external id instead of a generated one — see [Adopting an external id](#adopting-an-external-id). |
| `status` | no | Defaults to the board's first column. Must be one of the project's keys. |
| `assignee` | no | A wick user id. **Omit** it and the token's own user is assigned; send `""` for deliberately unassigned. |
| `fields` | no | Custom fields, keyed by the project's field keys. |
| `session_id` | no | Attaches an existing chat — "turn this conversation into a ticket". |

```bash
curl -s -X POST "$WICK_API/projects/$PROJECT/tickets" \
  -H "Authorization: Bearer $WICK_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Checkout returns 502 on retry",
    "body": "Reproduced on staging with retry enabled. Fails after the third attempt.",
    "status": "open",
    "assignee": "",
    "fields": { "type": "bug", "priority": "high" }
  }'
```

```json
{
  "id": "T-4F2A",
  "project_id": "proj_7f21c9",
  "title": "Checkout returns 502 on retry",
  "body": "Reproduced on staging with retry enabled. Fails after the third attempt.",
  "status": "open",
  "fields": { "type": "bug", "priority": "high" },
  "created_at": "2026-08-25T04:11:09Z",
  "updated_at": "2026-08-25T04:11:09Z"
}
```

### Adopting an external id

By default wick mints a short, human-quotable id (`T-4F2A`) — the code that
goes on the board card and gets typed into chat. When the ticket mirrors a
record that already has an identity elsewhere, send that identity as `id` and
the ticket becomes addressable by it directly:

```bash
curl -s -X POST "$WICK_API/projects/$PROJECT/tickets" \
  -H "Authorization: Bearer $WICK_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "id": "1f2e3d4c-5b6a-7988-9a0b-1c2d3e4f5a6b",
    "title": "Checkout returns 502 on retry"
  }'
```

An id must fit `[A-Za-z0-9._-]`, be at most 64 characters, and is kept
verbatim — `TIK-2026-001` stays exactly that. Two shapes are refused: anything
that could escape or hide inside the project's ticket directory (`..`, a path
separator, a leading dot), and the generated `T-XXXX` form, which stays
reserved so the generator never has to check whether someone claimed a code by
hand.

A uuid is the one id that gets normalised. Notion hands the same page id out in
two shapes — dashless in a page URL, dashed from its API — and they must not
become two tickets, so both fold to the dashless lowercase form:

```json
{ "id": "1f2e3d4c5b6a79889a0b1c2d3e4f5a6b", "…": "…" }
```

That is what makes the mapping disposable. The source system already knows the
page id, so it can read the ticket back with
`GET /tickets/1f2e3d4c5b6a79889a0b1c2d3e4f5a6b` without storing anything,
and a second create from the same page is refused with `400` rather than
quietly opening a duplicate.

Lookups stay cheap: a ticket id **is** its directory name, so addressing one by
its adopted id is a direct file read — nothing lists the board or opens every
ticket to find it.

Two things to weigh before using it. The id is long where a generated one is
short, so it reads worse on a card and in chat. And a board mixing both kinds
carries two id shapes at once — fine when the external system is the system of
record, worse when tickets arrive from everywhere. Omit `id` and nothing
changes: tickets created by hand, by an agent, or by an auto-create rule keep
getting `T-XXXX`.

## Get one ticket

```
GET /tickets/{ticketID}
```

```bash
curl -s "$WICK_API/tickets/T-4F2A" -H "Authorization: Bearer $WICK_TOKEN"
```

## Update a ticket

```
PATCH /tickets/{ticketID}
```

Every field is optional — send only what changes. Any edit bumps `updated_at`,
which is what the follow-up and auto-resolve timers read.

Move it to another column:

```bash
curl -s -X PATCH "$WICK_API/tickets/T-4F2A" \
  -H "Authorization: Bearer $WICK_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status": "in_progress"}'
```

Assign it:

```bash
curl -s -X PATCH "$WICK_API/tickets/T-4F2A" \
  -H "Authorization: Bearer $WICK_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"assignee": "usr_a91f"}'
```

Retitle, reassign, and set fields at once:

```bash
curl -s -X PATCH "$WICK_API/tickets/T-4F2A" \
  -H "Authorization: Bearer $WICK_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Checkout 502s on payment retry",
    "assignee": "usr_a91f",
    "fields": { "priority": "urgent", "type": "incident" }
  }'
```

::: tip Clearing a field
`fields` merges rather than replaces. Send a field as `""` to delete it; other
fields are left alone. To unassign, send `"assignee": ""`. To clear the
description, send `"body": ""` explicitly — omitting `body` leaves it
unchanged.
:::

Close it — use the key your board marks as finished:

```bash
curl -s -X PATCH "$WICK_API/tickets/T-4F2A" \
  -H "Authorization: Bearer $WICK_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status": "done"}'
```

## Delete a ticket

```
DELETE /tickets/{ticketID}?sessions=keep|delete
```

| `sessions` | Effect |
|---|---|
| `keep` (default) | Ticket goes; its chats survive as untracked. |
| `delete` | The chats are deleted with it, notes and history included. |

```bash
# Default: the conversations survive.
curl -s -X DELETE "$WICK_API/tickets/T-4F2A" \
  -H "Authorization: Bearer $WICK_TOKEN"
```

::: danger `sessions=delete` is not recoverable
A ticket is cheap to recreate; the conversations under it are not. The
destructive shape has to be asked for by name.
:::

## Attach / detach a session

```
PUT    /tickets/{ticketID}/sessions/{sessionID}
DELETE /tickets/{ticketID}/sessions/{sessionID}
```

A session belongs to exactly one ticket, so attaching one that sits on another
ticket **moves** it, carrying its notes across.

```bash
curl -s -X PUT "$WICK_API/tickets/T-4F2A/sessions/sess_9931" \
  -H "Authorization: Bearer $WICK_TOKEN"
```

## Notes

```
GET    /notes?ticket_id=T-4F2A
POST   /notes
PATCH  /notes/{noteID}
DELETE /notes/{noteID}
```

Scope reads with `?ticket_id=` or `?session_id=`; a session that belongs to a
ticket resolves to the ticket's notes.

```bash
curl -s -X POST "$WICK_API/notes" \
  -H "Authorization: Bearer $WICK_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"ticket_id": "T-4F2A", "body": "Reproduced on staging with retry enabled."}'
```

## Read the board's schema

```
GET /projects/{projectID}/tickets?rows=0
```

The board response carries `statuses` and `config.fields` — the valid
`status` keys and field keys for everything above. Read these rather than
hardcoding `open` / `done`: a project renames its own stages. `rows=0` keeps
the response to counts and schema, without paying for session rows.

```bash
curl -s "$WICK_API/projects/$PROJECT/tickets?rows=0" \
  -H "Authorization: Bearer $WICK_TOKEN" | jq '{statuses, fields: .config.fields}'
```

There is no token-authed `GET /projects/{id}` — the bare project route is
part of the browser UI surface and stays cookie-only on purpose.

## The event catalogue

```
GET /ticket-events
```

```bash
curl -s "$WICK_API/ticket-events" -H "Authorization: Bearer $WICK_TOKEN"
```

---

# Webhooks

## Request shape

Each delivery is a `POST` with a JSON body and these headers:

| Header | Value |
|---|---|
| `Content-Type` | `application/json` |
| `User-Agent` | `wick-tickets/1` |
| `X-Wick-Event` | The event name, e.g. `ticket.status_changed`. |
| `X-Wick-Delivery` | Unique delivery id — dedupe on this. |
| `X-Wick-Signature` | `sha256=<hex>` HMAC of the raw body. Only when a secret is set. |

Any custom headers you configure are also sent. `X-Wick-Signature` cannot be
overridden by one — a misconfiguration must not silently disable verification.

## Retries and ordering

- **3 attempts**, backing off ~1s, 5s, 25s. 10s timeout per attempt.
- `2xx` is success. A `4xx` other than `408` / `429` stops the retries — the
  receiver has said the request itself is wrong, so repeating it changes
  nothing.
- Deliveries run **in parallel**. Two rapid changes to one ticket can arrive
  out of order: compare `delivered_at`, or re-read the ticket over the REST API
  when order matters.
- There is **no durable queue**. If your receiver must never miss an event,
  reconcile with `GET /projects/{id}/tickets` on startup.

## Envelope

The full ticket rides on every event, not just the diff — a receiver that
missed an earlier delivery can still act on current state without calling back.

```json
{
  "id": "evt_9K3PQR7A",
  "event": "ticket.status_changed",
  "delivered_at": "2026-08-25T04:11:09.412Z",
  "project_id": "proj_7f21c9",
  "actor": { "type": "user", "id": "usr_a91f", "name": "Dana Reyes" },
  "ticket": {
    "id": "T-4F2A",
    "project_id": "proj_7f21c9",
    "title": "Checkout returns 502 on retry",
    "body": "Reproduced on staging with retry enabled.",
    "status": "in_progress",
    "assignee": "usr_a91f",
    "fields": { "type": "bug", "priority": "high" },
    "sessions": ["sess_9931"],
    "created_at": "2026-08-25T03:02:11Z",
    "updated_at": "2026-08-25T04:11:09Z"
  },
  "changes": {
    "status": { "from": "open", "to": "in_progress" }
  }
}
```

### `actor.type`

| Value | Who |
|---|---|
| `user` | A human in the wick web UI. |
| `api` | A Personal Access Token over the REST API — including your own writes. |
| `agent` | An AI agent acting through MCP. |
| `system` | Wick itself: the follow-up sweeper, auto-resolve, auto-create. |

::: tip Avoiding echo loops
If your system writes back to wick, ignore events whose `actor.type` is `api`
(or match `actor.id` against your token's user) — otherwise your own writes
come straight back at you.
:::

### `changes`

Present on `ticket.updated`, `ticket.status_changed`, and `ticket.assigned`.
Keys are `status`, `assignee`, `title`, `body`, and `fields.<key>`:

```json
{
  "changes": {
    "assignee": { "from": "", "to": "usr_a91f" },
    "fields.priority": { "from": "high", "to": "urgent" }
  }
}
```

A field that was cleared has `"to": ""`; one newly set has `"from": ""`.

## Events

| Event | Fires when | Extra fields |
|---|---|---|
| `ticket.created` | A ticket is created — by hand, by an agent, by auto-create, or over the API. | — |
| `ticket.updated` | **Any** field changed. Also fires alongside the two below. | `changes` |
| `ticket.status_changed` | The status moved to another column. | `changes.status` |
| `ticket.assigned` | The assignee changed (including being cleared). | `changes.assignee` |
| `ticket.deleted` | A ticket was deleted. `ticket` is the last copy you will get. | — |
| `ticket.session_attached` | A chat was linked to the ticket. | `session` |
| `ticket.session_detached` | A chat was unlinked. | `session` |
| `ticket.note_added` | A note was added. | `note` |
| `ticket.followup` | The sweeper nudged a stale ticket's agent. | — |
| `ticket.auto_resolved` | The sweeper closed an untouched ticket. | `changes.status` |

::: info `updated` fires with the specific events
A status move sends **both** `ticket.status_changed` and `ticket.updated`.
Subscribe to the specific event if you only care about board movement; subscribe
to `ticket.updated` to mirror every edit without enumerating each event as new
ones are added. Subscribing to both means two deliveries for one change.
:::

::: info `ticket.action` is not in this list
Clicking a [custom button](#custom-buttons) fires a `ticket.action` event, but
it is delivered **only** to that button's own URL — it is not part of the
catalogue above and cannot be subscribed to from a regular webhook row. See
[Custom buttons](#custom-buttons).
:::

### `ticket.created`

```json
{
  "id": "evt_2B8XKD1M",
  "event": "ticket.created",
  "delivered_at": "2026-08-25T03:02:11.905Z",
  "project_id": "proj_7f21c9",
  "actor": { "type": "api", "id": "usr_a91f", "name": "Dana Reyes" },
  "ticket": {
    "id": "T-4F2A",
    "project_id": "proj_7f21c9",
    "title": "Checkout returns 502 on retry",
    "status": "open",
    "assignee": "",
    "fields": { "type": "bug", "priority": "high" },
    "created_at": "2026-08-25T03:02:11Z",
    "updated_at": "2026-08-25T03:02:11Z"
  }
}
```

### `ticket.assigned`

```json
{
  "id": "evt_5QT0WJ4C",
  "event": "ticket.assigned",
  "delivered_at": "2026-08-25T03:40:52.117Z",
  "project_id": "proj_7f21c9",
  "actor": { "type": "user", "id": "usr_11c4", "name": "Sam Okafor" },
  "ticket": { "id": "T-4F2A", "status": "open", "assignee": "usr_a91f", "…": "…" },
  "changes": { "assignee": { "from": "", "to": "usr_a91f" } }
}
```

### `ticket.session_attached`

```json
{
  "id": "evt_7MC2VB9E",
  "event": "ticket.session_attached",
  "delivered_at": "2026-08-25T03:12:44.301Z",
  "project_id": "proj_7f21c9",
  "actor": { "type": "system" },
  "ticket": { "id": "T-4F2A", "sessions": ["sess_9931"], "…": "…" },
  "session": "sess_9931"
}
```

### `ticket.note_added`

```json
{
  "id": "evt_8ND4YF2G",
  "event": "ticket.note_added",
  "delivered_at": "2026-08-25T04:20:03.884Z",
  "project_id": "proj_7f21c9",
  "actor": { "type": "agent", "id": "main", "name": "main" },
  "ticket": { "id": "T-4F2A", "…": "…" },
  "note": "Reproduced on staging with retry enabled."
}
```

### `ticket.auto_resolved`

```json
{
  "id": "evt_3RH6ZP5K",
  "event": "ticket.auto_resolved",
  "delivered_at": "2026-08-28T04:00:00.000Z",
  "project_id": "proj_7f21c9",
  "actor": { "type": "system" },
  "ticket": { "id": "T-4F2A", "status": "done", "…": "…" },
  "changes": { "status": { "from": "waiting", "to": "done" } }
}
```

### `ticket.deleted`

```json
{
  "id": "evt_6PJ1LM8T",
  "event": "ticket.deleted",
  "delivered_at": "2026-08-25T05:00:17.226Z",
  "project_id": "proj_7f21c9",
  "actor": { "type": "user", "id": "usr_a91f", "name": "Dana Reyes" },
  "ticket": { "id": "T-4F2A", "title": "Checkout 502s on payment retry", "…": "…" }
}
```

## Custom buttons

A custom button is a single label + URL pair that appears on **every ticket's
page**. Clicking it POSTs the ticket to that URL and shows the delivery
outcome to whoever clicked — use it for things a human decides to trigger,
like "Sync to abc.com" or "Deploy", rather than something that should fire on
every change (that is what webhooks are for).

Add one under **Project settings → Ticket system → Integrations → Custom
buttons**: fill in a label and a full `http(s)://` URL, then save.

Clicking the button calls:

```
POST /api/tickets/{ticketID}/actions/{buttonID}
```

which delivers a `ticket.action` event to the button's own URL — same
envelope shape as a webhook delivery, same headers (`X-Wick-Event:
ticket.action`, `X-Wick-Signature` if the button carried a secret), and the
same SSRF guard and retry schedule as a configured webhook. The event's
`action` field carries the button's id, so one receiver serving several
buttons can tell them apart:

```json
{
  "id": "evt_4KQ8ZN2R",
  "event": "ticket.action",
  "action": "btn_9f3a1c2d4e5b6a7f",
  "delivered_at": "2026-08-26T04:11:09.412Z",
  "project_id": "proj_7f21c9",
  "actor": { "type": "user", "id": "usr_a91f", "name": "Dana Reyes" },
  "ticket": {
    "id": "T-4F2A",
    "project_id": "proj_7f21c9",
    "title": "Checkout returns 502 on retry",
    "body": "Reproduced on staging with retry enabled.",
    "status": "in_progress",
    "assignee": "usr_a91f",
    "fields": { "type": "bug", "priority": "high" }
  }
}
```

The click response mirrors the delivery outcome, not a generic "queued":

```json
{ "ok": true, "status": 200, "error": "", "attempts": 1 }
```

`ticket.action` is **not** subscribable from a regular webhook row — it only
ever reaches the button's own URL, because the button already names its
receiver.

## Verifying the signature

::: danger Verify the raw bytes
Compute the HMAC over the **exact body you received**. JSON key order and
whitespace are not stable across languages, so parsing and re-serialising
before verifying will produce a different digest and every check will fail.
Compare with a constant-time function, never `==`.
:::

**Node / Express**

```js
const crypto = require("crypto");
const express = require("express");

const app = express();
const SECRET = process.env.WICK_WEBHOOK_SECRET;

// express.raw, not express.json: the signature covers the raw bytes.
app.post("/hooks/wick-tickets", express.raw({ type: "application/json" }), (req, res) => {
  const expected =
    "sha256=" + crypto.createHmac("sha256", SECRET).update(req.body).digest("hex");
  const got = req.get("X-Wick-Signature") ?? "";

  if (
    expected.length !== got.length ||
    !crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(got))
  ) {
    return res.status(401).send("bad signature");
  }

  const event = JSON.parse(req.body.toString("utf8"));
  console.log(req.get("X-Wick-Event"), event.ticket.id, event.changes);

  // Answer fast; do the work after. Wick gives up after ~30s of retries.
  res.sendStatus(202);
});

app.listen(3000);
```

**Go**

```go
func handler(secret string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read", http.StatusBadRequest)
			return
		}
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Wick-Signature"))) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}

		var ev struct {
			Event  string `json:"event"`
			Ticket struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"ticket"`
		}
		if err := json.Unmarshal(body, &ev); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		log.Printf("%s %s → %s", ev.Event, ev.Ticket.ID, ev.Ticket.Status)
		w.WriteHeader(http.StatusAccepted)
	}
}
```

**Python / Flask**

```python
import hashlib, hmac, os
from flask import Flask, request, abort

app = Flask(__name__)
SECRET = os.environ["WICK_WEBHOOK_SECRET"].encode()

@app.post("/hooks/wick-tickets")
def hook():
    expected = "sha256=" + hmac.new(SECRET, request.get_data(), hashlib.sha256).hexdigest()
    if not hmac.compare_digest(expected, request.headers.get("X-Wick-Signature", "")):
        abort(401)
    event = request.get_json()
    print(request.headers["X-Wick-Event"], event["ticket"]["id"])
    return "", 202
```

## Receiver checklist

- **Answer in under 10s** — do the real work asynchronously. Retries stop after
  ~30 seconds.
- **Dedupe on `X-Wick-Delivery`.** A retry after a timeout can deliver an event
  your handler already processed.
- **Do not assume order.** Deliveries are parallel; use `delivered_at` or
  re-read the ticket.
- **Ignore your own writes** — filter `actor.type == "api"` to avoid loops.
- **Reconcile on startup** if you cannot afford to miss an event.

## Troubleshooting

Every attempt is recorded. Open the webhook row in **Integrations → Recent
deliveries** for the status, the error, and the attempt count. **Send test**
delivers a synthetic `ticket.updated` for `T-TEST` to the *saved* endpoint —
save the row before testing it.

| Symptom | Cause |
|---|---|
| `webhook url resolves to a private address (…) — refused` | The URL points at loopback, a private range, or link-local. Wick refuses these by default: a webhook URL is fetched by the server, so allowing them would let it be pointed at internal services. |
| `401` from your receiver | Signature mismatch — almost always re-serialising the body before verifying. |
| Nothing arrives | Row disabled, event not in its filter, or ticket mode off for the project. |
| Two deliveries per change | Subscribed to both `ticket.updated` and a specific event. Pick one. |
| `404` on every API call | The project's REST API toggle is off, or the token's user cannot see that project. |

## See also

- [Access Tokens (PAT)](/guide/access-tokens) — creating and revoking tokens.
- [Tickets connector](/connectors/tickets) — the same board over MCP, for agents.
- [Projects](/guide/agents/projects) — board columns and custom fields.
