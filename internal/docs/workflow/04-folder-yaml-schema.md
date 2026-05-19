## 4. Layout folder + YAML schema

### Folder per workflow

```
<BaseDir>/workflows/<id>/
  workflow.yaml          # graph + triggers (wajib) — published copy
  workflow.draft.yaml    # editor draft (lives only when user has unsaved edits)
  nodes/                 # per-node assets (opsional, kalau perlu)
    classify-msg.md      # prompt panjang
    fetch-data.sql       # query
    process-bug.sh       # script
    summarize.md         # prompt agent
  __tests__/             # fixture per node (test mode)
    classify-msg.json    # {input: {...}, expected_output: {...}}
    fetch-data.json
  runs/                  # state per run (auto-managed, gitignore)
    index/               # sharded run summary index (see §13)
      2026-05-15-01.jsonl
      2026-05-15-02.jsonl
    <run-id>/            # per-run dir, runID = plain UUID
      state.json         # {current, completed[], outputs{}, error?}
      events.jsonl       # append-only log per step
      nodes/<id>.json    # output cache per node
  README.md              # 1-paragraph deskripsi
```

### Identity — folder name = workflow ID

Folder name is the workflow `id` — UUID for canvas-created workflows,
arbitrary `[a-z0-9-]+` id for legacy hand-edited ones (the regex
still accepts hex+dashes so UUIDs pass). Display title lives in
`name:` and is freely renameable through the editor toolbar; the
folder, URL (`/workflows/edit/<id>`), and run index all stay anchored
to the original `id` so rename never invalidates history or links.

`Parse(id, data)` overwrites `workflow.yaml > id` with the folder name
on every load — the YAML value is informational, the folder is
authoritative.

### `workflow.yaml` — common fields (edge-first)

```yaml
id: 0193e2b4-6c20-7a5f-9c1c-...    # UUIDv7, assigned saat Create, never change
version: 3                          # integer, manual bump = signal major change
name: Inbound Inquiry Triage
description: Klasifikasi pesan inbound, route ke handler sesuai kategori
enabled: true
max_duration_sec: 600               # total flow timeout, default 5min

triggers:                           # WAJIB minimal 1. Multi-trigger supported.
  # Each trigger owns its own routing — manual fires its chain,
  # slack fires another, cron fires yet another. The engine matches
  # `evt.Type` to `Trigger.Type` and walks from that trigger's
  # `entry_node`. `graph.entry` (below) is only a legacy fallback
  # for hand-edited YAML; canvas-driven workflows leave it empty.
  - id: trigger-channel             # stable canvas identity, round-trips
    type: channel                   # save/load. Codec generates it if absent.
    channel: chat
    event: message
    target: "#inbox"
    match:
      mention_bot: true
    entry_node: classify-intent     # the node this trigger fires
  - id: trigger_manual              # second trigger in the same workflow
    type: manual                    # fires a different chain
    entry_node: handle-question

queue:
  max_size: 20
  on_overflow: drop_oldest          # drop_oldest | drop_new | reject

env:                                # config schema (values di env.yaml; lihat §11)
                                    # reuse config-tags widget vocab
  - name: NOTIFY_CHANNEL
    widget: text
    desc: "Channel target untuk notify"
    default: "#inbox-audit"
  - name: TRACKER_API_TOKEN
    widget: secret                  # encrypted di disk
    desc: "Token issue tracker"
    required: true

datasets:                           # optional, link ke datasets (§12)
  - name: handled
    ref: inquiry-events             # dataset slug di <BaseDir>/datasets/
    mode: read_write
    expected_version: 1             # break loud kalau dataset schema drift

graph:
  entry: classify-intent            # default entry kalau trigger ga override
  nodes:                            # FLAT list — node = declaration only
    - id: classify-intent
      type: classify
      provider: claude
      prompt: "Klasifikasi: {{.Event.Payload.text}}"
      # NO next: / cases: di sini — connection di edges[] below
    - id: handle-bug
      type: connector
      module: tracker
      op: create_issue
    - id: handle-question
      type: agent
      provider: claude
    - id: silent-end
      type: end

  edges:                            # SEPARATE list — connections explicit
    - { from: classify-intent, case: bug,      to: handle-bug }
    - { from: classify-intent, case: question, to: handle-question }
    - { from: classify-intent, case: default,  to: silent-end }
    # Fan-out: multiple edges same source no case = parallel
    # Branch routing: case label per edge
    # Linear: edge no case from non-classify/branch node

created_by: yoga@abc.com
created_at: 2026-05-14T08:00:00Z
```

### Edge model (edge-first, n8n-style)

| Feature | YAML |
|---|---|
| Linear chain | `{from: A, to: B}` |
| Branch routing (classify/branch node) | `{from: A, case: bug, to: B}` |
| Fan-out parallel | Multiple `{from: A, to: X}`, `{from: A, to: Y}`, no `case` |
| Fan-in (wait-for-all) | Multiple `{from: X, to: M}`, `{from: Y, to: M}` + node M `type: merge` |
| Per-trigger entry | `entry_node:` di trigger spec (authoritative for canvas-driven workflows; `graph.entry` is the legacy hand-edited fallback) |

**Why edge-first:**
- Match canvas mental model (nodes = cards, edges = lines)
- MCP canvas ops (`workflow_connect`, `workflow_disconnect`) map 1:1 to edge mutations — atomic operations
- Refactor surgical — swap target = swap 1 edge, ga touch node body
- Fan-out / fan-in trivial — add/remove edges
- AI compose lebih natural — "from X go to Y when condition" = single edge entry
- Validation simple — edge `{from, to}` both must exist in `nodes[]`

**Validation rules:**
- Edge `case:` field only valid kalau `from` node `type: classify` atau `type: branch`
- `case: default` wajib ada di classify/branch (safety net)
- Edge target node must exist di `nodes[]`
- Cycle detection at parse time (Kahn's algorithm)
- Unreachable nodes (not target of any edge + not entry) = warning, ga block

### Canvas codec contract (Drawflow ↔ YAML)

The Workflows tab UI uses Drawflow on the canvas. The codec at
[`internal/tools/agents/workflows_codec.go`](../tools/agents/workflows_codec.go)
bridges Drawflow's JSON with `workflow.yaml`. Key invariants:

**Trigger phantom nodes**

- Each entry in `triggers:` renders as one phantom node on the canvas
  (CSS `node-trigger`, `data.type = "trigger"`,
  `data.data.triggerKind = "manual" | "cron" | "channel" | "webhook" | "error" | "schedule_at"`).
- The canvas node name (`drawflowNode.Name`) is the `Trigger.ID` value.
  Codec auto-generates one (`trigger-<type>` or
  `trigger-<type>-<idx>`) when YAML omits it, so legacy YAMLs still
  render on first open.
- On save, decoder scans every `type: trigger` node, reads its first
  outgoing connection target, and emits one `wf.Trigger` entry per
  phantom with `EntryNode` set to that target.
- Trigger phantoms NEVER leak into `Graph.Nodes` or `Graph.Edges` —
  they are visual-only. The engine routes via `Trigger.EntryNode`
  matched by event type.

**Save-time merge** ([`mergeTriggers`](../tools/agents/workflows.go))

| Source of truth | Owns |
|---|---|
| Canvas (decoder output) | `ID`, `Type`, `EntryNode` |
| Prev draft (loaded from disk) | Schedule, ChannelName, Match, Path, Method, SecretRef, DedupTTLSec, … (everything else) |

The merge key is `Trigger.ID`. Triggers present in prev but missing
from the canvas are **dropped** — canvas is authoritative for which
triggers exist. New canvas triggers (no prev match) start with
empty config; the inspector editor fills it in.

**Entry resolution**

- `triggerHasEntry(w, type)` is the UI gate for Run Now / external
  fire. Logic:
  1. If any `Trigger.ID` is set → canvas-driven workflow → strict
     per-trigger rules (find matching type with non-empty
     `EntryNode`).
  2. Otherwise (legacy hand-edited YAML, no IDs) → fall back to
     `graph.entry`.
- Engine's `pickEntry` (called inside `Engine.Run`) follows the same
  per-trigger-first / `graph.entry`-fallback chain.

**Execute workflow picker**

- Toolbar Run Now is removed. The floating "Execute workflow" pill
  at the canvas bottom opens a menu listing every trigger on the
  canvas with its wired target.
- POST `/run` accepts `trigger_id` (form field). `pickTriggerByID`
  resolves it; an empty `trigger_id` is acceptable only when the
  workflow has exactly one trigger (legacy compat). Anything else
  returns 400 with a human-readable error.

**Draft vs published at Run time**

- Run Now LOADS the draft (`workflow.draft.yaml`) and passes it as
  an explicit override via `MCP.RunNowWith` → `Router.RunNowWith`
  → `WorkItem.Workflow`. The worker prefers the override over
  `Router.defs[id]`.
- Live triggers (cron, channel, webhook) keep firing the published
  copy registered in `Router.defs[id]`. This separation lets the
  user iterate on a draft via Run Now without re-publishing every
  edit.

### Identitas + governance

- **`id`** — stable identitas (= folder name). `name:` field freely
  renameable; rename writes both `workflow.yaml` and the draft so list
  page and editor never drift. Approval nempel ke `id`.
- **`version`** — sinyal manual. User bump kalau perubahan material.
  AI guard juga lihat field ini buat keputusan re-approve.
- **`approved` TIDAK di YAML.** Hidup di file `state.json` di folder
  workflow (atau DB; lihat §18). Kalau di YAML, hand-edit bisa
  bypass guard.

### Trigger types catalog

Every trigger entry can carry an `id:` (stable canvas identity, used
by the codec to round-trip wiring + by the Execute workflow picker
to disambiguate Run Now) and an `entry_node:` (the node this
trigger fires). Omitted in the catalog below for readability — see
the YAML example above for the full shape.

**`cron`** — jadwal periodik.
```yaml
- id: trigger-cron
  type: cron
  entry_node: daily-digest
  schedule: "0 8 * * *"
  timezone: Asia/Jakarta            # optional, default UTC
```

**`channel`** — event dari channel adapter (Slack/Telegram/REST/WhatsApp/...).
Channel-agnostic; `channel:` field cuma routing hint ke registry. Setiap
channel ([internal/agents/channels/](../agents/channels/)) self-register
dgn schema-nya sendiri.
```yaml
- id: trigger-channel-slack
  type: channel
  entry_node: classify-intent
  channel: slack                    # nama channel di registry; "*" = semua channel
  event: message                    # event subtype the channel emits;
                                    # each EventDescriptor in
                                    # internal/agents/channels/<name>/
                                    # workflow registers one
  target: "#support"                # channel-specific addressing
                                    # (channel ID, JID, etc.)
  match_enabled: true               # gate: false = dump-all (default)
  match:                            # MatchSchema-driven filter form
    mode: whitelist                 # dropdown rendered from
                                    #   SlackMessageMatch struct
    channel_id: '[{"id":"C123","name":"general"}]'   # picker output
    user: '[{"id":"U987","name":"yoga"}]'             # picker output
    text_contains: "support"        # case-insensitive substring
  match_modes:                      # per-key Fixed/Expression toggle
    text_contains: fixed
  whitelist:                        # legacy field, still honoured
    users: ["U123"]
    groups: ["@support-team"]
  dedup_ttl_sec: 86400              # default 24h
```

**Per-event MatchSchema**. Every event descriptor under
`internal/agents/channels/<name>/workflow/event_*.go` declares a
wick-tagged Go struct via `entity.StructToConfigs`. The workflow API
renders the schema to HTML and ships it as `match_html`; the trigger
inspector innerHTML-injects + hydrates it. Adding a new filter field
= one extra struct field on the event's MatchSchema — no router or
inspector edit.

```go
// internal/agents/channels/slack/workflow/event_message.go
type SlackMessageMatch struct {
    Mode            string `wick:"dropdown=all|whitelist;default=all"`
    AllowedChannels string `wick:"picker=slack.channels;visible_when=mode:whitelist;key=channel_id"`
    AllowedUsers    string `wick:"picker=slack.users;visible_when=mode:whitelist;key=user"`
    TextContains    string `wick:"desc=Substring filter"`
}
```

**Match evaluation** lives in
`internal/agents/workflow/trigger/router.go matchEventPayload`. The
router runs match only when `match_enabled: true` (dump-all stays the
safer default). Semantics:

- empty / missing spec value → key skipped (no filter)
- string spec → payload[key] equals spec
- JSON array (picker output, `[{id,name},...]`) → payload[key] is in
  the id list; empty array = "no chips selected yet → no filter"

Events that need fancier semantics (regex, set difference, custom
transform) fall back to dump-all + filter inside the graph (branch /
transform node).

**Event sub-types** are descriptor-driven. Slack ships eight events
today (message, app_mention, app_home_opened, block_action,
view_submission, view_closed, shortcut, command). Telegram, Discord,
etc. register their own.

**`webhook`** — HTTP POST eksternal.
```yaml
- id: trigger-webhook
  type: webhook
  entry_node: ingest-payload
  path: /hooks/pagerduty
  secret_ref: wick_enc_...          # HMAC SHA-256 di header X-Wick-Sig
  whitelist:
    ips: ["10.0.0.0/8"]             # optional CIDR allowlist
  body_to_var: payload              # body JSON → {{.Event.Payload}}
```

**`manual`** — UI button + MCP op. Fired via the Execute workflow
picker on the canvas (or via the MCP `workflow_run` op).
```yaml
- id: trigger_manual
  type: manual
  entry_node: classify-intent
  label: "Run digest now"
  require_role: admin
```

**`schedule_at`** — one-shot.
```yaml
- id: trigger-schedule-at
  type: schedule_at
  entry_node: classify-intent
  at: 2026-06-01T08:00:00+07:00
  delete_after: true                # auto-disable workflow setelah fire
```

**`error`** — fire on failure of another workflow (n8n-style error workflow).
```yaml
- id: trigger-error
  type: error
  entry_node: notify-oncall
  source_workflow: "*"              # workflow id atau pattern; "*" = semua
  severity: [high, critical]        # optional filter: error severity levels
  node_types: [shell, http, connector]  # optional filter: cuma error dari node types ini
  dedup_ttl_sec: 300                # default 5min, hindari error storm
```

Use case:
- Centralized error handler workflow yang catch failures dari workflow lain
- Notify on-call team kalau workflow critical fail
- Auto-retry pattern (error → wait → re-trigger original workflow)
- Audit trail di dataset events untuk root-cause analysis

Source workflow `on_error` opt-in:
```yaml
# Source workflow declares error handler binding
on_error:
  trigger_workflow: "error-handler"   # workflow id yg pasang trigger type: error
  severity: critical                  # filter mana error yg fire handler
  include_state: true                 # ship full state.json ke handler
  include_node_output: true           # include outputs hingga node yg fail
```

Engine flow saat workflow fail:
1. Workflow A fail di node X → state.status = failed
2. Engine check `on_error.trigger_workflow` di workflow A
3. Build synthetic event `Event{Type: "error", ...}` dgn payload
4. Dispatch ke router → workflow B (error-handler) jalan
5. Workflow B akses error context via `{{.Event.Payload.source_workflow}}`,
   `{{.Event.Payload.failed_node}}`, `{{.Event.Payload.error}}`, dst.

Loop protection: error-handler workflow yang sendiri fail = ga trigger
error-handler lagi (engine track origin chain di state.json,
max depth 3).

### Event payload → template var

Tiap node bisa pake Go template `{{.Event.*}}`:

| Field | cron | channel | webhook | manual | schedule_at | error |
|---|---|---|---|---|---|---|
| `.Event.Type` | "cron" | "channel" | "webhook" | "manual" | "schedule_at" | "error" |
| `.Event.Subtype` | schedule | event name (message, block_action, …) | "" | "" | "" | source workflow |
| `.Event.Channel` | "" | module name (slack, telegram) | "" | "" | "" | "" |
| `.Event.At` | tick time | msg time | recv time | click time | scheduled at | error time |
| `.Event.Payload` | schedule meta | full channel payload (user, text, channel_id, thread, callback_id, action_id, …) | parsed body | nil | nil | error context |

Channel-specific keys (user, text, channel_id, thread, callback_id, action_id, …) all live under `.Event.Payload` — each channel owns the keys it populates. Reference them via `{{.Event.Payload.<key>}}`.

Error event `.Event.Payload` shape:
```json
{
  "source_workflow": "support-triage",
  "source_run_id": "0193...",
  "failed_node": "create-ticket",
  "node_type": "connector",
  "error": "linear API 503: service unavailable",
  "severity": "high",
  "state_snapshot": { ... full state.json ... },    // kalau include_state
  "node_outputs": { ... previous nodes' outputs ... }  // kalau include_node_output
}
```

Plus `{{.Node.<id>.<field>}}` buat output dari node lain.

### Fixed vs Expression mode (per-arg)

Tiap arg di node yang punya structured input (connector + channel
action) bisa di-toggle **Fixed** atau **Expression** lewat inspector
pill UI. Default = **Fixed** (literal, ngak render template).

Storage di YAML:

```yaml
nodes:
  - id: send-reply
    type: connector
    module: slack
    op: send_message
    args:
      channel: "{{.Event.Payload.channel_id}}"   # template
      text: "Hello world"                         # literal
    arg_modes:
      channel: expression
      text: fixed
```

Engine behavior ([nodes/args.go renderArgsWithModes](../agents/workflow/nodes/args.go)):

- `arg_modes[key] == "fixed"` → value passed as literal, no template
  render. Safe against accidental `{{...}}` substring in user-typed
  text.
- `arg_modes[key] == "expression"` atau missing → render via Go
  template. Backward compatible — workflows tanpa `arg_modes` block
  jalan persis kayak sebelumnya.

Drag-drop dari INPUT pane → drop ke arg field → auto-flip ke
Expression mode + insert `{{.Event.Payload.<key>}}` di posisi cursor.

Template path normalization: legacy `{{.Event.payload.x}}` (huruf
kecil dari JSON tag) di-rewrite jadi `{{.Event.Payload.x}}` saat
render — ngak break workflow YAML lama yang typed lowercase, tapi
canonical form yang baru pakai Capital sesuai Go field name.

---

