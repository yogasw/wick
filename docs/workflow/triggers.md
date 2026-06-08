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

```yaml
triggers:
  - type: cron
    schedule: "*/15 * * * *"   # every 15 minutes
```

Standard 5-field cron expression, same parser as [Background Jobs](/guide/job-module). Disabled workflows skip cron ticks but still respond to manual runs.

## channel

```yaml
triggers:
  - type: channel
    source: slack
    match:
      event: app_mention
      channel_id: ["C12345"]
```

`source` is the channel adapter key (`slack`, `telegram`, …). `match` is event-shape-specific:

- **Slack** events: `event_message`, `event_app_mention`, `event_command`, `event_block_action`, `event_view_submission`, `event_shortcut`, `event_app_home_opened`, `event_view_closed` — each comes with its own filter schema (channel ID whitelist, action_id, text_contains, callback_id, …).
- **Telegram** events: `event_message`, `event_command`.

Discover the exact filter schema per event via the [`workflow_integration` MCP op](/connectors/workflow#tier-1-introspection-read-only) — it returns the full per-channel event + action catalog with `match_schema` and `payload_schema`.

The full event payload (user, text, thread_ts, …) lands in `.Event.Payload` — see the channel docs for the per-platform shape.

## webhook

```yaml
triggers:
  - type: webhook
    slug: my-hook          # URL-safe slug, no slashes — becomes the last path segment
    method: POST
    secret_ref: WEBHOOK_TOKEN   # optional HMAC secret (env var name in workflow secrets)
```

Wick exposes **two** endpoints for each webhook trigger:

| Endpoint | Target | Use |
|---|---|---|
| `POST /webhook/{wf_id}/{slug}` | Published workflow | Production inbound traffic |
| `POST /webhook-test/{wf_id}/{slug}` | Draft workflow | Testing before publish |

The **Test URL** fires the current draft so you can iterate without publishing. The **Live URL** only works after the workflow is published. Both URLs are shown in the trigger inspector's tabbed preview inside the canvas editor, with one-click copy buttons.

### Path storage

The `slug` field stores only the URL-safe suffix — no leading slash, no `wf_id` prefix. The engine constructs the full `/{wf_id}/{slug}` path at runtime. A slug can contain path segments (e.g. `orders/new`) but cannot begin with `/`.

If `slug` is omitted the trigger accepts any path for that workflow.

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

```yaml
triggers:
  - type: manual
```

A trigger row exists but no scheduler fires it. The workflow only runs from:

- the canvas **Run** button,
- the workflow detail page's manual runner,
- an `Replay` link from a past run,
- `workflow_run_now` over MCP.

Useful for workflows you only want to fire from inside wick.

## schedule_at

```yaml
triggers:
  - type: schedule_at
```

Paired with a node that emits a "fire at T+N" intent (typically a `transform` or `go_script` returning `{schedule_at: "2026-05-19T10:00:00Z"}`). The scheduler queues a one-shot run at that time. Cancellation: drop the queued entry from the scheduler tab.

## error

```yaml
triggers:
  - type: error
    workflow_id: support-triage   # which workflow to listen to
```

Fires when the referenced workflow's run ends in `failed`. The failed run's metadata (`run_id`, `error`, `node_id`, `event`) lands in `.Event.Payload` so you can post an alert, file an issue, or kick off remediation.

## See also

- [Built-in Workflow connector ▶ `workflow_trigger_types`](/connectors/workflow#tier-1-introspection-read-only) — live trigger catalog with schemas.
- [Channels](/guide/agents/channels) — what `source: slack` / `telegram` resolves to.
