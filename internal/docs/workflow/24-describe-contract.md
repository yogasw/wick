## 24. Describe() contract — self-documenting MCP catalog

**Goal:** AI editing workflow (Claude Code, Cursor, ChatGPT, Gemini, custom MCP
client) dapat **full context tiap node** lewat MCP, tanpa baca skill markdown
atau source code. Provider baru (channel / connector / node executor) yang
sudah implement contract → MCP otomatis expose tanpa edit MCP layer **dan
tanpa daftar manual di catalog**. Tambah connector baru = drop module + register
ops seperti biasa. Tambah node baru = drop executor + `Descriptor()` seperti
biasa. Detail auto-generate dari descriptor yg sudah ada.

### TODO

- [x] Define shared `Docs` struct (optional fields only) — re-used by all 4 descriptors → [pkg/wickdocs/docs.go](../../../pkg/wickdocs/docs.go)
- [x] Embed `Docs` di `engine.NodeDescriptor` (built-in nodes)
- [x] Embed `Docs` di `integration.EventDescriptor` (channel triggers)
- [x] Embed `Docs` di `integration.ActionDescriptor` (channel actions)
- [x] Embed `Docs` di connector `Operation` (connector ops) — `connector.Op` / `OpDestructive` signature breaking refactor (last param `wickdocs.Docs`)
- [x] Add `TriggerDescriptor` + `TriggerRegistry` + `DefaultTriggerDescriptors()` (6 trigger types) → [engine/trigger_registry.go](../../agents/workflow/engine/trigger_registry.go)
- [x] Implement **`workflow_node_detail(node_type)`** MCP op — universal lookup, project descriptor source jadi unified response → [mcp/node_detail.go](../../agents/workflow/mcp/node_detail.go)
- [x] Add `InputSample` + `OutputSample` (JSON string) ke `Docs` — for "try-it" panel rendering
- [x] Populate Tier-1 descriptors (Slack 7 events + 10 actions + 6 connector ops, GitHub 3 connector ops, 4 built-in nodes, channel trigger). Total ~32 descriptors.
- [x] Smoke test via real `lab mcp serve` → all 4 routing branches resolve + 8 sampled descriptors return populated Docs
- [ ] **Tier 2 (deferred):** wickmanager / workflow / httprest / crudcrud connector ops, secondary slack ops (search_channels, get_channel_info, get_thread_replies, get_user_info, get_permalink, send_ephemeral, delete_message, remove_reaction), secondary github ops (get_file, list_prs, add_comment), remaining built-in nodes (shell, transform, db_query, dataset, go_script, switch, end, channel, connector)
- [ ] **Helper ops (deferred):** `workflow_validate`, `workflow_template_test`, `workflow_picker_resolve`, `workflow_describe`
- [ ] **Skill slim (deferred):** strip skill `wick-workflow` ke pointer doang setelah catalog complete

### Why this doc exists

Sekarang AI trial-error karena MCP **passive** — dump schema tapi gak ada
example, gak ada quirk, gak ada cross-ref. Skill markdown (client-side) gak
portable: provider lain (Cursor, ChatGPT, Gemini, custom MCP client) gak dapat.

Pindah static knowledge ke catalog source-side → semua MCP client otomatis dapat
context. Wick standalone tanpa skill = self-documenting.

### Re-use what already exists

Existing surface udah cover banyak. Detail auto-generate dari ini — **gak perlu
nulis 2x**:

| Bagian | Sumber existing |
|---|---|
| Input schema | `EventDescriptor.MatchSchema` / `ActionDescriptor.InputType` / connector `Op.Input` / per-node `Schema` struct → `integration.StructSchema(v)` reflect ke JSON Schema |
| Output schema | `ActionDescriptor.OutputType` / connector `Op.Output` / node executor return → same `StructSchema()` |
| Description | `*Descriptor.Description` (built-in node, event, action, connector op semua udah punya) |
| WhenToUse | `NodeDescriptor.WhenToUse` |
| Destructive flag | `ActionDescriptor.Destructive` / connector `OpDestructive` |
| Match schema | `EventDescriptor.MatchSchema` (per event) |

MCP layer `workflow_node_detail` cuma project field-field ini ke response shape.
Zero hand-coded schema duplicate.

### Contract: shared `Docs` struct, opt-in fields

Tambahin 1 struct `Docs` (shared antar 4 descriptor), embed di tiap descriptor.
Semua field opsional. If-present → MCP serve. If-absent → key omitted dari
response (bukan `null`, bukan empty array).

```go
// internal/agents/workflow/docs/docs.go (or near engine — TBD)
type Docs struct {
    OutputShape        map[string]string // per-field human desc beyond schema type
    TemplateableFields []string          // field names accepting {{...}}; nil = unknown
    Quirks             []string          // gotchas — expiry, side effects, normalization
    Examples           []Example         // copy-pasteable full YAML node
    PairWith           []string          // related descriptor keys
    CommonPitfalls     []string          // known AI mistakes
}

type Example struct {
    Name string // slug — "basic", "with_structured_output"
    YAML string // full node block, copy-pasteable
}
```

Embed:

```go
type NodeDescriptor struct {
    Type        workflow.NodeType
    Description string
    WhenToUse   string
    Schema      map[string]any
    Output      map[string]string
    docs.Docs   // ← embed
}

type EventDescriptor struct {
    Channel     string
    Event       string
    Name        string
    Description string
    PayloadType any
    MatchSchema []entity.Config
    Match       MatchFunc
    docs.Docs   // ← embed
}

type ActionDescriptor struct {
    Channel     string
    Action      string
    Name        string
    Description string
    InputType   any
    OutputType  any
    Destructive bool
    Execute     ExecuteFunc
    docs.Docs   // ← embed
}
```

Same untuk connector `Operation` + future `TriggerDescriptor`. **No hierarchy
break, no existing caller change** — Docs zero-value = current behaviour.

### MCP surface — 1 new op

```
workflow_node_detail(node_type) → unified detail
```

`node_type` format (prefix tells category, AI copy-paste dari `workflow_workspace` listing):

| Prefix | Example | Source descriptor |
|---|---|---|
| (no prefix) | `agent`, `branch`, `classify`, `http` | `engine.NodeDescriptor` (built-in) |
| `channel:` | `channel:slack.message` | `integration.EventDescriptor` |
| `channel:` | `channel:slack.send_message` | `integration.ActionDescriptor` |
| `connector:` | `connector:slack.chat.postMessage` | connector `Operation` |
| `trigger:` | `trigger:cron`, `trigger:webhook` | future `TriggerDescriptor` |

**Note:** `channel:` prefix dipakai untuk both event + action — disambiguasi via
suffix (`.message` = event, `.send_message` = action). AI gak perlu tau —
listing dari `workflow_workspace` udah expose key lengkap.

### Unified response shape

```json
{
  "node_type": "channel:slack.open_modal",
  "kind": "channel_action",
  "description": "Open a Slack modal for the user.",
  "when_to_use": "Use when you need a form / confirmation UI in Slack.",
  "schema": { /* JSON Schema */ },
  "output_shape": {
    "result.view_id": "modal view ID — needed for update_modal",
    "result.view_hash": "view hash for optimistic concurrency"
  },
  "templateable_fields": ["trigger_id", "view"],
  "quirks": [
    "trigger_id expires 3 seconds after event — call open_modal first, agent reasoning after"
  ],
  "pair_with": ["channel:slack.update_modal", "channel:slack.push_modal"],
  "common_pitfalls": [
    "Don't run agent node before open_modal — trigger_id will expire"
  ],
  "examples": [
    {"name": "skeleton_then_update", "yaml": "- id: open-modal\n  type: channel\n  ..."}
  ]
}
```

Optional fields yg kosong → key omitted dari response (bukan `null`, bukan
empty array). AI bisa baca semua key yg ada langsung tanpa null-check.

### Implementation per source

**A. Built-in nodes** (`internal/agents/workflow/nodes/<type>.go`)
- Existing `Descriptor() engine.NodeDescriptor`
- Embed `docs.Docs` di `engine.NodeDescriptor`
- Per executor: populate `Docs` kalau worth-it, skip kalau gak ada gotcha. Zero-value = backward compat.

**B. Channels** (`internal/agents/workflow/integration/` + per-channel package)
- Existing `EventDescriptor`, `ActionDescriptor`
- Embed `docs.Docs` di kedua struct
- Per channel package (`internal/channels/slack/...`): populate `Docs` saat
  build descriptor sebelum `Registry.RegisterEvent` / `RegisterAction`

**C. Connectors** (`internal/connectors/<module>/`)
- Existing `Operation` struct (di `pkg/connector`)
- Embed `docs.Docs` di `Operation`
- Per connector: populate `Docs` per-op (e.g. `slack.chat.postMessage`)

**D. Trigger types** (future — gak ada equivalent sekarang)
- Add `TriggerDescriptor` struct (Type, Schema, Description, embed `docs.Docs`)
- Register di trigger package — `cron`, `webhook`, `manual`, `channel`

### MCP wrapping logic

`workflow_node_detail` di MCP layer (`internal/agents/workflow/mcp/`):

1. Parse `node_type` prefix → route ke source registry
2. Lookup descriptor di source
3. Project ke unified response shape:
   - `description`, `when_to_use` ← descriptor existing fields
   - `schema` ← `integration.StructSchema(d.InputType)` (atau equivalent per source)
   - `output` ← `integration.StructSchema(d.OutputType)` (kalau ada)
   - `output_shape`, `quirks`, `templateable_fields`, `examples`, `pair_with`, `common_pitfalls` ← `d.Docs.*` if-present
   - `kind` ← derived dari source registry (built_in / channel_event / channel_action / connector_op / trigger)
4. Return JSON, omit kosong keys

Source descriptor stays canonical — MCP cuma presenter. Tambah node type baru di
source = otomatis discoverable via MCP, gak edit MCP layer. Tambah connector
baru = drop module, register ops via `connector.Module` seperti biasa — kalau
populate `Docs` di Op-nya, otomatis muncul di `workflow_node_detail`. Kalau
gak, tetap muncul dgn description + schema saja (graceful degrade).

### Sample priority — review-first workflow

User akan review sample dulu sebelum replicate ke existing. Sample bikin **per
kategori**, masing-masing 1 representative:

| Kategori | Sample subject | Alasan dipilih |
|---|---|---|
| Built-in node | `agent` | paling kompleks, structured_output quirk, templateable fields banyak |
| Channel event | `slack.message` | payload normalization quirk (flat vs `.raw.*`), match_schema relevan |
| Channel action | `slack.open_modal` | trigger_id 3s expiry quirk (paling sering AI tabrak), pair_with chain |
| Connector op | `slack.chat.postMessage` | familiar, channel ID vs name pitfall, blocks vs text |
| Trigger type | `channel` | multi-trigger routing quirk (per-trigger entry_node) |

**Flow:** implement struct extension + `workflow_node_detail` + 5 sample di
atas → user review style/wording/completeness → kalau OK → replicate pattern ke
seluruh existing node/event/action/connector-op (separate PRs per kategori).

### Provider-agnostic property

Catalog = data, MCP tool = thin presenter. Tambah connector baru (discord,
teams):

```
internal/connectors/discord/        # connector module
internal/channels/discord/          # channel package
```

Tiap descriptor opt-in populate Examples / Quirks. MCP otomatis expose
`workflow_node_detail("connector:discord.send_message")` tanpa code change di
`internal/agents/workflow/mcp/`. Same untuk wick standalone tanpa skill — AI
yang connect via HTTP MCP dapat full context dari descriptor.

### Non-goals (deferred)

- Helper ops: `workflow_validate`, `workflow_template_test`,
  `workflow_picker_resolve`, `workflow_describe` — defer sampai detail flow
  proven.
- Auto-generate examples dari schema. Examples hand-written only.
- Linter wajibin Examples / Quirks. Contract opsional — descriptor minimal yg
  cuma punya description + schema tetap valid.
- Replace `workflow_get` / `workflow_write_file`. Detail op = type/category
  lookup, bukan instance.
- Slim skill `wick-workflow` — defer sampai detail flow + populate complete.

### Cross-ref

- §5 Node catalog — base node fields + description discipline
- §9 MCP surface — existing tier 1/2/3 ops (this doc adds 1 new op only)
- `workflow-node-module` skill — Descriptor() implementation rule
- [internal/agents/workflow/integration/registry.go](../../agents/workflow/integration/registry.go) — Event/ActionDescriptor source
- [internal/agents/workflow/engine/engine.go](../../agents/workflow/engine/engine.go) — NodeDescriptor source
- [pkg/wickdocs/docs.go](../../../pkg/wickdocs/docs.go) — shared Docs + Example struct
- [internal/agents/workflow/mcp/node_detail.go](../../agents/workflow/mcp/node_detail.go) — workflow_node_detail projector

### Implementation summary (2026-05-19)

**Surface added:**
- 1 MCP op: `workflow_node_detail(node_type)` — single universal lookup. Prefix routing: `channel:`, `connector:`, `trigger:`, no-prefix = built-in.
- `connector.Op` / `connector.OpDestructive` signature now takes `wickdocs.Docs` as the last param (breaking — all 117 existing callsites updated via one-shot AST rewriter, then discarded).

**Tier 1 descriptors populated** (representative quirks/examples/I-O samples):
- Slack events (8): message, app_mention, app_home_opened, block_action, command, shortcut, view_submission, view_closed
- Slack actions (10): send_message, open_modal, update_modal, push_modal, update_message, send_ephemeral, add_reaction, open_dm, publish_home, respond_url
- Slack connector ops (7): send_message, list_channels, get_channel_history, list_users, get_user_by_email, update_message, add_reaction
- GitHub connector ops (3): list_repos, list_issues, create_issue
- Built-in nodes (4): agent, branch, classify, http
- Trigger types (1): channel (multi-trigger routing quirks)

**Tests:** unit + table-driven coverage for `wickdocs.Docs`, `engine.TriggerRegistry`, `mcp.NodeDetail` (all 5 routing branches + error paths). End-to-end smoke via real `lab mcp serve` confirms projection works through the JSON-RPC stack.
