---
outline: deep
---

# Triggers

A trigger decides **when** a workflow run starts. One workflow can carry multiple triggers — the queue policy (per-workflow concurrency cap, drop / queue / parallel) decides what happens when two land at once.

| Trigger | When it fires |
|---|---|
| [`cron`](#cron) | On a cron schedule. |
| [`channel`](#channel) | An inbound message from Slack / Telegram / Web matches `source` + `match.event`. |
| [`webhook`](#webhook) | Public HTTP endpoint at `/webhook/<workflow-id>`. |
| [`manual`](#manual) | Triggered from the canvas Run button or the workflow detail page. |
| [`schedule_at`](#schedule_at) | One-shot timer — a previous node emits a "fire at T+N" intent, the scheduler queues a fresh run. |
| [`error`](#error) | Another workflow failed — receives the failed run's metadata so you can route alerts. |

Every trigger contributes to `.Event` in the [render context](./nodes#render-context): payload, source identity, ts, user, etc.

## cron

Fields: `schedule` (6-field cron expression, same parser as Background Jobs). Disabled workflows skip cron ticks but still respond to manual runs.

## channel

Fields: `channel` (adapter key: `slack`, `telegram`, …), `event` (event id), `match` (event-shape-specific filter map).

`match` is event-shape-specific:

- **Slack** events: `message`, `app_mention`, `command`, `block_action`, `view_submission`, `shortcut`, `app_home_opened`, `view_closed` — each comes with its own filter schema (channel ID whitelist, action_id, text_contains, callback_id, …).
- **Telegram** events: `message`, `command`.

Discover the exact filter schema per event via the [`workflow_integration` MCP op](/connectors/workflow#tier-1-introspection-read-only) — it returns the full per-channel event + action catalog with `match_schema` and `payload_schema`.

The full event payload (user, text, thread_ts, …) lands in `.Event.Payload` — see the channel docs for the per-platform shape.

## webhook

Fields: `path` (slug only, no leading slash), `method`, `secret_ref`, `respond_mode`.

Wick exposes **two** endpoints for each webhook trigger:

| Endpoint | Target | Use |
|---|---|---|
| `POST /webhook/{wf_id}/{slug}` | Published workflow | Production inbound traffic |
| `POST /webhook-test/{wf_id}/{slug}` | Draft workflow | Testing before publish |

The **Test URL** fires the current draft so you can iterate without publishing. The **Live URL** only works after the workflow is published. Both URLs are shown in the trigger inspector's tabbed preview inside the canvas editor, with one-click copy buttons.

### Path storage

The `path` field stores only the URL-safe slug — no leading slash, no `wf_id` prefix. The engine constructs the full `/{wf_id}/{slug}` path at runtime. A slug can contain path segments (e.g. `orders/new`) but cannot begin with `/`.

If `path` is omitted the trigger accepts any path for that workflow.

### respond_mode

`respond_mode` controls when and what the HTTP endpoint returns to the caller.

| Value | HTTP response | Use when |
|---|---|---|
| `immediately` _(default)_ | `202 Accepted` + `{"matched":1}` as soon as the run is enqueued | Fire-and-forget; caller doesn't need the result |
| `last_node` | Blocks until the workflow finishes (≤ 30 s), then returns the last node's output as JSON with `200` | Caller needs the result synchronously and the workflow is fast |
| `respond_node` | Blocks until the first `webhook_respond` node completes (≤ 30 s), then returns whatever that node emits (custom status + body + headers) | Full control over the HTTP response shape |

Both blocking modes time out after **30 seconds** and return `504 Gateway Timeout`. For workflows that regularly take longer, use `immediately` and poll via the run-status API.

#### respond_node mode and the webhook_respond node

When `respond_mode = respond_node`, add a `webhook_respond` node to the graph and connect it anywhere downstream of the entry node. Set the trigger's **Respond** field to "Using 'Respond to Webhook' Node" in the canvas inspector.

The `webhook_respond` node fields:

| Field | Type | Description |
|---|---|---|
| `respond_status` | int | HTTP status code. Default `200`. |
| `respond_body` | string | Response body. Go-template rendered — use `{{.Node.<id>.<field>}}` to embed upstream output. |
| `respond_headers` | map[string]string | Extra response headers. Each value is template-rendered. If `Content-Type` is omitted, wick auto-detects `application/json` vs `text/plain` from the body. |

**Validation:** publishing a workflow that has `respond_mode = respond_node` but no reachable `webhook_respond` node from the trigger's `entry_node` raises a **Warning** at publish time. The publish still succeeds, but the caller will receive `502 Bad Gateway` at runtime when no respond node runs.

**First node wins:** if multiple `webhook_respond` nodes can complete in the same run, only the first one's output is sent back.

### HMAC verification

When `secret_ref` is set, every inbound request must include an `X-Wick-Sig` header containing `sha256=<hex-hmac>` of the raw request body, signed with the resolved secret. Requests without a valid signature are rejected with `401 Unauthorized`. The secret value is looked up from the workflow's env/secrets store (see [Workflow settings](./canvas#workflow-settings-env--secrets)).

### Payload

Body is parsed (JSON / form / raw) and lands in `.Event.Payload`:

```
.Event.Payload.path      # stripped path, e.g. /wf_id/my-hook
.Event.Payload.method    # HTTP method
.Event.Payload.headers   # flattened header map
.Event.Payload.query     # flattened query params
.Event.Payload.body      # parsed JSON body (when Content-Type is application/json)
.Event.Payload.raw       # raw body bytes (always present)
```

### Multi-trigger routing

A single workflow can carry multiple webhook triggers with different slugs. Each slug routes to its own entry node — the engine resolves `TriggerID` on the event and starts execution from the matching entry node.

## manual

A trigger row exists but no scheduler fires it. The workflow only runs from:

- the canvas **Run** button,
- the workflow detail page's manual runner,
- a **Replay** link from a past run,
- `workflow_run_now` over MCP.

Useful for workflows you only want to fire from inside wick.

## schedule_at

Paired with a node that emits a "fire at T+N" intent (typically a `transform` or `go_script` returning `{schedule_at: "2026-05-19T10:00:00Z"}`). The scheduler queues a one-shot run at that time. Cancellation: drop the queued entry from the scheduler tab.

## error

Fields: `source_workflow` (which workflow to listen to), `severity` (optional filter).

Fires when the referenced workflow's run ends in `failed`. The failed run's metadata (`run_id`, `error`, `node_id`, `event`) lands in `.Event.Payload` so you can post an alert, file an issue, or kick off remediation.

## See also

- [Built-in Workflow connector ▶ `workflow_trigger_types`](/connectors/workflow#tier-1-introspection-read-only) — live trigger catalog with schemas.
- [Channels](/guide/agents/channels) — what `channel: slack` / `telegram` resolves to.
