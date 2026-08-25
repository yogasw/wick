# Ticket system — Integrations menu

Add an **Integrations** section to the project ticket-system settings: outbound
webhooks (wick → your system) plus a documented, token-authed REST surface
(your system → wick). Today the ticket API exists but is cookie-only, so no
external system can call it, and nothing is emitted when a ticket changes.

## TODO

- [x] 1. `project.TicketIntegrations` config (webhooks + API toggle) on `TicketConfig`
- [x] 2. Event emitter: `internal/agents/ticket/events.go` — all 10 events wired
- [x] 3. Dispatcher: `internal/agents/ticket/webhook.go` — HMAC sign, retry, SSRF guard, delivery log
- [x] 4. Bearer auth for the ticket REST API (PAT), scoped to project access
- [x] 5. `TicketIntegrationsEditor.svelte` + wired into `TicketSystemEditor`
- [x] 6. Docs page: `docs/guide/agents/ticket-integrations.md` + nav entry
- [x] 7. Tests: 21 new (emission, diff, HMAC, retry, SSRF, allowlist, redaction)

Remaining / deliberately out of scope:

- [ ] Durable delivery queue. Deliveries are fire-and-forget with 3 retries;
      a receiver that cannot miss an event reconciles over REST. Documented.
- [ ] Per-webhook event ordering. Parallel fan-out means two rapid changes can
      arrive out of order; receivers dedupe on `X-Wick-Delivery`. Documented.
- [ ] MCP surface for managing webhooks. Config is UI + ticket-config API only.

## Current state

| Piece | Where | Status |
|---|---|---|
| Ticket entity + CRUD | `internal/agents/ticket/ticket.go` | exists |
| REST handlers | `internal/tools/agents/api_tickets.go` | exists, **cookie-auth only** |
| Route table | `internal/tools/agents/handler.go:341-356` | exists |
| Settings UI | `fe/agents/project-settings/.../TicketSystemEditor.svelte` | exists, no integrations |
| Outbound webhooks | — | **missing** |
| Token auth on ticket API | — | **missing** |

Two real gaps: `login.Middleware.Session` (`internal/login/middleware.go:87`)
reads only the session cookie, and `/tools/` sits behind `RequireToolAccess`,
so a PAT cannot reach `/tools/agents/api/tickets/*`. And no ticket mutation
emits anything.

## 1. Config

`internal/agents/project/ticket.go` — new field on `TicketConfig`:

```go
Integrations TicketIntegrations `json:"integrations,omitempty"`
```

```go
type TicketIntegrations struct {
    APIEnabled bool             `json:"api_enabled,omitempty"`
    Webhooks   []TicketWebhook  `json:"webhooks,omitempty"`
}

type TicketWebhook struct {
    ID      string   `json:"id"`               // stable, generated
    Name    string   `json:"name,omitempty"`
    URL     string   `json:"url"`
    Secret  string   `json:"secret,omitempty"` // HMAC-SHA256 key
    Events  []string `json:"events,omitempty"` // empty = all
    Headers map[string]string `json:"headers,omitempty"`
    Enabled bool     `json:"enabled"`
}
```

Zero value = off, so every existing `meta.json` decodes unchanged. Secret is
write-only over the API (redacted to `""` on read, blank on save = keep).

## 2. Events

One catalogue, in `internal/agents/ticket/events.go`:

| Event | Fires when |
|---|---|
| `ticket.created` | `Create` |
| `ticket.updated` | any field changed via `Save` |
| `ticket.status_changed` | status differs from stored (also emits `updated`) |
| `ticket.assigned` | assignee differs |
| `ticket.deleted` | `Delete` |
| `ticket.session_attached` | `AttachSession` |
| `ticket.session_detached` | `DetachSession` |
| `ticket.note_added` | note POST |
| `ticket.followup` | sweeper stale nag |
| `ticket.auto_resolved` | sweeper auto-close |

Envelope:

```json
{
  "id": "evt_9f3a...",
  "event": "ticket.status_changed",
  "delivered_at": "2026-08-25T04:11:09Z",
  "project_id": "proj_x",
  "actor": {"type": "user|agent|system", "id": "...", "name": "..."},
  "ticket": { "...full ticket..." },
  "changes": {"status": {"from": "open", "to": "in_progress"}}
}
```

`changes` present only on `updated` / `status_changed` / `assigned`.

Emitter is a package-level hook (`ticket.SetEmitter`) so the `ticket` package
does not import HTTP or config-service code — same shape as the existing
`SetManager`/`SetLayout` wiring in `tools/agents`.

Status-change detection needs the pre-image: `Save` loads the stored copy
first and diffs. `SaveKeepingTimestamp` (sweeper path) diffs too but tags the
actor as `system`.

## 3. Dispatcher

`internal/agents/ticket/webhook.go`:

- Fan out per matching webhook, each in its own goroutine — one dead endpoint
  must not stall a ticket write. Mutations never block on delivery.
- `POST` JSON, headers: `X-Wick-Event`, `X-Wick-Delivery`, `X-Wick-Signature`
  (`sha256=<hex hmac of raw body>`), plus configured custom headers.
- Retry 3x, backoff 1s/5s/25s, 10s per-attempt timeout. 2xx = success.
- Ring-buffer delivery log (last 20/webhook) surfaced in the UI so a broken
  integration is debuggable without server logs.

## 4. Token auth

New middleware in `internal/tools/agents/` mirroring
`internal/agents/channels/rest/rest.go:437` (`authBearer`):

- If `Authorization: Bearer <pat>` present and no cookie user, resolve via
  `accesstoken.Service.Authenticate` → inject the user into ctx exactly as
  `login.Session` does, so `projectAccessMW` and every handler stay unchanged.
- Applies to `/tools/agents/api/tickets*`, `/api/projects/{id}/tickets*`,
  `/api/notes*` only. Rest of the app stays cookie-only.
- Gated on `Integrations.APIEnabled` per project; 404 (not 403) when off, so
  existence isn't leaked.
- PAT already carries a user identity, so per-project access control is the
  existing `CanAccessProject` check — no new permission model.

## 5. UI

New `TicketIntegrationsEditor.svelte`, rendered inside the existing Ticket
system section (below auto-create), collapsed by default:

- **REST API** — toggle, base URL readout, "copy token" link to `/tokens`.
- **Webhooks** — list rows (name, URL, events multi-select, enabled), add /
  remove, "Send test event", per-row last-delivery status pill.
- Header summary line gains `· N webhook(s)` / `· API on`.

Follows the existing editor idioms: `patch()` fan-out into `onChange`,
`$derived` for computed views, English copy, no new deps.

## 6. Docs

`docs/tickets-api.md` (linked from the settings section):

- Auth, base URL, error shape, status codes.
- Every endpoint with a runnable curl: list, create, get, patch (status /
  assignee / fields), delete, attach/detach session, notes CRUD, board config.
- Every webhook event with a full example payload.
- Signature verification snippet (Go + Node).

## Risks

- **Secret handling.** Never log the secret or echo it back; redact on read.
  Signature is over the raw body, so the doc's verification snippet must warn
  against re-serialising before checking.
- **SSRF.** A user-supplied webhook URL is fetched by the server. Reuse
  whatever guard the workflow http node already applies; if none, block
  loopback/link-local by default.
- **Ordering.** Parallel fan-out means two rapid status changes can arrive out
  of order. `delivered_at` + `id` let receivers dedupe; documented, not solved.
