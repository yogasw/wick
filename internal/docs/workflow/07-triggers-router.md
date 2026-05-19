## 7. Triggers + router

Sama dengan desain routine sebelumnya. Inline disini biar self-contained.

### Dua entry path

```
        ┌──────────────────┐      ┌──────────────────────┐
        │  cron tick (60s) │      │  channel adapter     │
        │  worker/server   │      │  (slack/wa/email)    │
        └────────┬─────────┘      └──────────┬───────────┘
                 │                           │
                 │     ┌──────────────────┐  │
                 │     │ webhook handler  │──┤  Event{Type:"webhook"}
                 │     │ POST /hooks/...  │  │
                 │     └──────────────────┘  │
                 │                           │
                 │     ┌──────────────────┐  │
                 │     │ UI Run Now btn   │──┤  Event{Type:"manual"}
                 │     └──────────────────┘  │
                 │                           │
                 ▼                           ▼
              ┌──────────────────────────────────┐
              │   trigger.Router.Dispatch(evt)   │
              │     - match trigger spec         │
              │     - check whitelist            │
              │     - dedup (channel event_id)   │
              └────────────────┬─────────────────┘
                               │
                               ▼
              ┌──────────────────────────────────┐
              │   per-workflow FIFO queue        │
              │   1 worker goroutine / workflow  │
              └────────────────┬─────────────────┘
                               │
                               ▼
                  engine.Run(ctx, workflow, evt)
```

### Cron path

`worker/server.go` tetep loop `jobs` table tiap 60s. Workflow muncul di
table sebagai `workflow:<id>` dengan `Schedule` dari trigger cron
pertama. Multi-cron → multi job rows: `workflow:<id>:cron-0`,
`:cron-1`, dst.

### Event path

Channel adapter di [internal/agents/channels/](../agents/channels/)
panggil `OnAnyMessage(evt)` → `triggerRouter.Dispatch(evt)`. Hook fire
SEBELUM session routing. Workflow match? Enqueue + skip session routing
kalau `consume: true`.

Webhook adapter = HTTP handler di `internal/pkg/api/` — mount
`/hooks/{id}/{path}`, verify HMAC, parse body, build Event.

### Per-workflow FIFO queue

Tiap workflow punya queue + 1 worker goroutine. Worker ngedrain serial.
Antar workflow paralel sesuai pool capacity. Overflow → drop_oldest |
drop_new | reject (per workflow config).

### Dedup

`(channel, event_id)` di LRU in-memory + file fallback `dedup.json` di
workflow folder (TTL = `dedup_ttl_sec`, default 24h). Same event_id
muncul = skip.

### Channel registry contract — generic interface

Channels di [internal/agents/channels/](../agents/channels/) (`slack`,
`telegram`, `rest`, `whatsapp` later, `email` later) bukan hardcoded di
workflow engine. Workflow konsumsi channel lewat **registry yang
self-describing** — channel daftarin trigger spec + action spec, workflow
auto-discover.

```go
// internal/agents/channels/channel.go
type Channel interface {
    Name() string                              // "slack", "telegram", "rest", ...
    IsConfigured() bool                        // udah ada sudah di kode existing

    // Inbound: trigger spec yang channel bisa fire.
    TriggerSpecs() []TriggerSpec

    // Outbound: action yang workflow invoke via node `type: channel`.
    Actions() []ActionSpec
    Send(ctx context.Context, action string, args map[string]any) (any, error)

    // Event subscription — channel push event ke router.
    Subscribe(handler EventHandler)
}

type TriggerSpec struct {
    Type        string                          // "channel" — only top-level type per channel
    Events      []string                        // sub-event types: ["message", "reaction", "mention", ...]
    Description string
    MatchSchema map[string]any                  // JSON schema buat field `match:` di YAML
}

type ActionSpec struct {
    ID          string                          // "reply_thread", "send_dm", "react", ...
    Description string
    Destructive bool                            // outbound write → need approval di guard
    InputSchema map[string]any
    OutputSchema map[string]any
}

type EventHandler func(Event)
```

### Channel Actions diakses via `type: channel` node

Channel module `Actions()` di-expose ke workflow sbg action node type
`channel`. **Ga auto-promote ke skill registry** — channel = own
domain (agents-conversation), terpisah dari skill (Claude Code skill
bundle).

Workflow konsumsi:

```yaml
- type: channel
  channel: slack                     # channel module name
  op: reply_thread                   # one of Actions()
  args:
    channel: "{{.Event.Payload.channel_id}}"
    thread:  "{{.Event.Payload.thread}}"
    text: "..."
```

Engine route: `Channel.Send(ctx, "reply_thread", args)`. UI Inspector
auto-render form dari `ActionSpec.InputSchema`. AI MCP discover via
`workflow_channels()`.

**Symmetric dgn trigger:**

| YAML position | Backend |
|---|---|
| `triggers: [type: channel, channel: slack]` | `slack.TriggerSpecs()` + `Subscribe()` |
| `graph.nodes: [type: channel, channel: slack, op: reply_thread]` | `slack.Send("reply_thread", args)` |

Single module, dua arah. Naming consistent — user/AI inget "channel"
handle both inbound + outbound.

### Discovery via MCP

AI editing workflow lewat MCP butuh tau action apa aja yg tersedia per
channel:

```
workflow_channels()
  → [{name: "slack", configured: true,
      triggers: [...], actions: [...]},
     {name: "telegram", configured: false, ...}]

workflow_connectors()
  → [{module: "github", rows: ["github-prod", "github-staging"],
      operations: [...]},
     {module: "loki", rows: ["loki-default"], operations: [...]},
     ...]
```

UI Inspector pake endpoint ini — dropdown channel + dropdown
connector module + op auto-populate.

### Implicit reply-to-source

Trigger event dari channel → engine inject implicit reply node di akhir
flow kalau:
- Trigger `type: channel`
- Workflow ga punya explicit `type: channel` action node yang `op:
  reply_thread` / `reply` / `send_message` ke event source
- `reply_source: true` di trigger spec (default true)

Engine bikin synthetic node:
```yaml
- type: channel
  channel: <event-channel>
  op: reply_thread
  args:
    channel: "{{.Event.Payload.channel_id}}"
    thread:  "{{.Event.Payload.thread}}"
    text:    "{{.Run.final_result}}"
```

Override:
- `reply_source: false` di trigger → ga inject.
- Workflow define explicit `type: channel` action ke source thread → engine ga inject (detect by channel + op + Event.Thread match).

### Channel event subtypes payload

Tiap channel declare event types yg dia support via `TriggerSpec.Events`.
Payload shape per event subtype:

**`event: message`** — pesan masuk (most common):
```yaml
Event:
  Type: channel
  Subtype: message
  At: 2026-05-14T10:00:00Z
  User: "<sender_id>"
  Text: "<message body>"
  Channel: "<channel_id>"
  Thread: "<thread_ts or empty>"
  Payload:
    message_id: "<channel-specific ID>"
    raw: { ... channel-specific full event ... }
```

**`event: action`** — user click button / select dropdown (Slack-style):
```yaml
Event:
  Type: channel
  Subtype: action
  At: 2026-05-14T10:00:00Z
  User: "<clicker_id>"
  Channel: "<channel_id>"
  Thread: "<thread_ts of source>"
  Payload:
    trigger_id: "<short-lived ID for modal open>"
    action_id: "<button action_id>"
    value: "<button value>"
    message_ts: "<ts of message containing button>"
    metadata: { ... arbitrary JSON propagated dari button posting ... }
```

**`event: submission`** — modal form submit:
```yaml
Event:
  Type: channel
  Subtype: submission
  At: 2026-05-14T10:00:00Z
  User: "<submitter_id>"
  Payload:
    view_id: "<modal view ID>"
    callback_id: "<modal callback_id from open_modal>"
    values:
      <field_name>: <field_value>
      # all field values dari modal
    trigger_id: "<for chaining new modal>"
    metadata: { ... propagated dari modal open args ... }
```

**`event: reaction`** — user react to message (emoji):
```yaml
Event:
  Type: channel
  Subtype: reaction
  Payload:
    emoji: "👍"
    message_ts: "<ts of reacted message>"
    item_user: "<author of reacted message>"
```

Other subtypes (`mention`, `join`, `leave`, `file_upload`) — channel
impl declare own payload via `TriggerSpec.PayloadSchema`.

### Slack channel — interactive Actions spec

Slack channel module declare Actions:

```go
// internal/agents/channels/slack/actions.go
func (s *Channel) Actions() []ActionSpec {
    return []ActionSpec{
        {
            ID:          "send_message",
            Description: "Post plain message to channel. Returns posted ts.",
            InputSchema: jsonSchema{
                channel:   { type: string, required: true },
                thread_ts: { type: string, required: false },
                text:      { type: string, required: true },
            },
            OutputSchema: jsonSchema{
                ts:      string,
                channel: string,
            },
        },
        {
            ID:          "reply_thread",
            Description: "Reply to existing thread. Same as send_message with thread_ts.",
            InputSchema: { /* channel, thread, text */ },
        },
        {
            ID:          "send_dm",
            Description: "Send direct message to user.",
            InputSchema: { user, text },
        },
        {
            ID:          "post_message_with_button",
            Description: "Post message with interactive button. Returns ts. Click fires event: action.",
            InputSchema: jsonSchema{
                channel:   string_required,
                thread_ts: string_optional,
                text:      string_required,
                button: {
                    text:      { type: string, required: true },
                    action_id: { type: string, required: true },
                    value:     { type: string },
                    metadata:  { type: object },          // propagate ke event payload
                },
            },
            OutputSchema: jsonSchema{
                ts:      string,
                channel: string,
            },
        },
        {
            ID:          "open_modal",
            Description: "Open modal dialog. Requires trigger_id from prior action event (<3s ago). Submit fires event: submission.",
            InputSchema: jsonSchema{
                trigger_id:  string_required,
                callback_id: string_required,    // identifies modal di submission event
                title:       string_required,
                fields: {
                    type: array,
                    items: oneOf [
                        { type: text,     name, prefill, required, max_length },
                        { type: textarea, name, prefill },
                        { type: select,   name, options[], prefill },
                        { type: multiselect, name, options[] },
                        { type: datepicker, name },
                    ],
                },
                metadata: { type: object },     // propagate ke submission payload
                submit_text: string_optional,    // button label, default "Submit"
            },
            OutputSchema: jsonSchema{
                view_id: string,
            },
        },
        {
            ID: "react",
            Description: "Add emoji reaction to message. Idempotent (re-react safe).",
            InputSchema: { channel, message_ts, emoji },
        },
        {
            ID: "update_message",
            Destructive: true,
            Description: "Edit posted message. Useful for marking button consumed.",
            InputSchema: { channel, ts, text, button?: { ... } },
        },
    }
}
```

### Error-handler workflow example

Error workflow = workflow biasa dgn trigger `type: error`:

```yaml
# workflow: error-handler.yaml
id: 0193...
id: error-handler
name: "Centralized error handler"

triggers:
  - type: error
    source_workflow: "*"                       # catch all workflows
    severity: [high, critical]
    dedup_ttl_sec: 300                         # avoid error storm

graph:
  entry: route-by-severity
  nodes:
    - id: route-by-severity
      type: branch
      expr: '{{.Event.Payload.severity}}'

    - id: page-oncall
      type: connector
      module: pager
      op: trigger_incident
      args:
        summary: "Workflow {{.Event.Payload.source_workflow}} failed at {{.Event.Payload.failed_node}}"
        details: "{{.Event.Payload.error}}"
        urgency: high

    - id: notify-slack
      type: connector
      module: chat
      op: send_message
      args:
        channel: "#workflow-failures"
        text: |
          ⚠️ {{.Event.Payload.source_workflow}} ({{.Event.Payload.severity}})
          Failed at: {{.Event.Payload.failed_node}}
          Error: {{.Event.Payload.error}}
          Run ID: {{.Event.Payload.source_run_id}}

    - id: log-to-dataset
      type: dataset_insert
      dataset: workflow_failures
      row:
        run_id: "{{.Event.Payload.source_run_id}}"
        workflow: "{{.Event.Payload.source_workflow}}"
        node: "{{.Event.Payload.failed_node}}"
        error: "{{.Event.Payload.error}}"
        severity: "{{.Event.Payload.severity}}"
        timestamp: "{{.Event.At}}"

  edges:
    - { from: route-by-severity, case: critical, to: page-oncall }
    - { from: route-by-severity, case: high,     to: notify-slack }
    - { from: route-by-severity, case: default,  to: log-to-dataset }
    - { from: page-oncall,  to: log-to-dataset }                  # always log
    - { from: notify-slack, to: log-to-dataset }
```

Source workflow opt-in via `on_error:`:
```yaml
# workflow: critical-pipeline.yaml
on_error:
  trigger_workflow: error-handler
  severity: critical              # tag error events this workflow generates
  include_state: true             # ship state.json snapshot
  include_node_output: true
```

### Webhook path templating

Webhook trigger declare path; engine mount handler:

```yaml
- type: webhook
  path: /hooks/orders/{order_id}              # path param available di Event.Payload.params
  method: POST                                # GET | POST | PUT | DELETE
  secret_ref: wick_enc_...                    # HMAC SHA-256 di X-Wick-Sig
  parse_body: json                            # json | form | raw
  whitelist:
    ips: ["10.0.0.0/8"]
```

Mount path: `<wick-host>/hooks/orders/{order_id}` matched ke workflow.
Multiple webhooks per workflow OK (different paths or methods). Engine
de-dupe via `idempotency_key: <header_name>` (optional).

Path params di `Event.Payload.params`:
```yaml
Event:
  Type: webhook
  At: ...
  Payload:
    method: POST
    path: /hooks/orders/12345
    params: { order_id: "12345" }              # from path template
    body: { ... parsed JSON body ... }
    query: { ... query string ... }
    headers: { ... selected headers ... }
```

**HMAC enforcement** (wired di
[`trigger/webhook.go`](../../agents/workflow/trigger/webhook.go) +
[`trigger/router.go`](../../agents/workflow/trigger/router.go)):
- `Router.WebhookSecretFor(path, method)` returns the `secret_ref`
  of the first matching webhook trigger, decrypted to plaintext.
- `webhook.ServeHTTP` reads `X-Wick-Sig` header and verifies via
  `VerifyHMAC(body, secret, sig)` before dispatching.
- No `secret_ref` declared → no header check (open webhook,
  caller's choice).
- `secret_ref` declared + missing/invalid `X-Wick-Sig` → 401, no
  dispatch.

Use `secret_ref: wick_enc_...` (encrypted via wick's
[encrypted-fields](../encrypted-fields.md) layer); plaintext
secrets in YAML are rejected at validation.

### Adding new channel

Bikin sub-package `internal/agents/channels/<name>/`, implement
`Channel` interface (`TriggerSpecs` + `Actions` + `Send`), register di
`setup.Compose`. Trigger router + action dispatcher otomatis pick up.
Workflow engine + UI ga butuh perubahan.

---

