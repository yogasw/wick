## 5. Node catalog

Tiap node punya: `id` (unique), `type`, edge konek di `graph.edges[]`,
field type-spesifik, optional `output_schema`.

### Node description discipline (AI-first mandate)

Tiap node type punya `description:` field — load-bearing, AI baca
verbatim untuk decide kapan pakai. Same discipline sebagai connector
`Operation.Description` di [docs/guide/connector-module.md](../../docs/guide/connector-module.md).

```yaml
- id: classify-intent
  type: classify
  description: |              # ← load-bearing untuk AI MCP discovery
    Klasifikasi pesan support ke bug/question/feature/other.
    Return: verdict (enum) + confidence (0-1) + reasoning (string).
    Pakai kalau input natural language perlu route ke handler spesifik.
  prompt: |
    ...
```

**Write style:**
- ✅ Action verb + what it does + when to use
- ✅ "Classify support message into bug/question/feature/other. Returns verdict + confidence + reasoning. Use when natural language input needs routing."
- ❌ "classify intent" (too short, no when-to-use)
- ❌ "this node uses AI to classify" (no action verb, vague)

`description` shown di:
- UI Inspector node info pane
- MCP `workflow_node_types()` response (AI sees verbatim)
- Canvas tooltip on hover

Default description disediakan untuk built-in node types — user override
per node kalau usage spesifik. Custom node types (future plugin) wajib
declare description.

### Common node fields

```yaml
- id: <unique-id>                  # [a-z0-9-]+
  type: <node-type>
  label: "Display name"            # optional, default = id
  description: "..."               # optional
  timeout_sec: 60                  # optional, default = workflow-level
  retry:                           # optional
    max: 2
    backoff_sec: 5
  on_failure: halt                 # halt | skip | fallback (gotonode)
  fallback: end                    # if on_failure=fallback
  arg_modes:                       # optional — per-field render mode for nodes
    url: fixed                     # whose inspector uses the Fixed|Expression
    body: expression               # toggle (channel, connector, http). Keys
                                   # match the schema field. Missing = expression.
  output_schema:                   # optional, validator
    type: object
    properties:
      result: { type: string }
```

**Edges separate (edge-first).** Node body declares `id` + type-specific
fields only. Connections to other nodes live di `graph.edges[]`. Lihat
§4 schema buat detail edge model. Examples node di bawah ini fokus ke
body content; edge entry-nya disebut di komentar inline.

### `classify` — AI klasifikasi (reliable)

LLM call yang return verdict pendek dari enum tertentu. Output di-stamp ke
`{{.Node.<id>.verdict}}` + `{{.Node.<id>.confidence}}`. Branching via
edges dgn `case:` label (lihat §4 Edge model).

**Masalah klasik:** AI free-text ga deterministik — return "Bug.", "I think
this is a bug", atau "bug_report" instead of "bug". Engine handle lewat 6
lapis defense — defaults sudah agresif, opt-in tambahan kalau perlu.

```yaml
# Node body (di graph.nodes[]) — NO cases here, edges separate
- id: classify-intent
  type: classify
  provider: claude                  # optional: claude | codex | gemini
                                    # default chain: preset → workflow → system
  preset: classifier-cheap          # default = "classifier" preset
  prompt: |
    Klasifikasi pesan support berikut.
    Pesan: {{.Event.Payload.text}}
  prompt_file: nodes/classify-intent.md   # alternatif

  # Enum cases — engine validate verdict against this list
  output_cases: [bug, question, feature_request, other]

  # Defaults aman (active tanpa nulis apa-apa):
  structured_output: true           # paksa AI return JSON via tool_use
  normalize: true                   # lowercase, trim, strip punct/quotes

  # Opt-in:
  fuzzy_match: false                # "bug_report" → "bug" (Levenshtein < 3)
  retry_on_mismatch: 1              # retry kalau output di luar enum
  confidence_threshold: 0.0         # < threshold → fire "default" edge case
  examples:                         # few-shot prompt examples
    - input: "production error di order checkout"
      output: bug
    - input: "gimana cara setup webhook?"
      output: question
    - input: "boleh tambah dark mode?"
      output: feature_request

# Edge entries di graph.edges[] (separate dari node body):
# - { from: classify-intent, case: bug,             to: handle-bug }
# - { from: classify-intent, case: question,        to: handle-question }
# - { from: classify-intent, case: feature_request, to: handle-feature }
# - { from: classify-intent, case: other,           to: silent-end }
# - { from: classify-intent, case: default,         to: silent-end }   # WAJIB
```

### 6-layer reliability (urutan applied)

```
1. structured_output  — AI di-prompt return JSON schema:
                         {verdict, confidence, reasoning}
                         CLI output di-parse + validate.
                         Provider abstract per impl (lihat §5.1 di bawah).

2. normalize          — lowercase, trim, strip "."/,/'/" — "Bug." → "bug"

3. exact match        — cek case keys: "bug" matches case "bug" → handle-bug

4. fuzzy_match (opt)  — Levenshtein < 3 ke case key terdekat:
                         "bug_report" → "bug", "kestion" → "question"

5. retry_on_mismatch  — output ga match? Re-prompt dgn pesan stricter:
                         "JAWAB CUMA satu dari: bug, question, feature_request,
                          other. Tidak ada penjelasan."
                         Retry N kali.

6. confidence_threshold — confidence < threshold → `default` case.
                          AI uncertain = default route, audit warning.
```

**Engine guarantee:**
- Layer 1 enforce JSON shape via prompt + parser. Provider yang
  support strict mode (CLI `--output-format json`) tambahan jaminan
  parse never fail.
- Habis 6 layer masih mismatch → fire `default` case. Audit log entry.
- `default` ga ada di `cases` → `halt` dgn error (config bug, bukan
  runtime).

**Output structure** (schema auto-derived dari `cases`):

```json
{
  "verdict": "bug",
  "confidence": 0.92,
  "reasoning": "mentions production error and widget breakage"
}
```

`confidence` + `reasoning` opt-in (boleh dimatikan via `output_fields:
[verdict]` kalau pengen output minimal).

**UI run timeline wajib tampilin `reasoning`** — user butuh tau "kenapa
AI mutusin verdict ini?". Audit page also indexed by verdict +
confidence buat trace pattern (lihat §13 state persistence + §21 replay).

### 5.1. Structured output via CLI (bukan API SDK)

Wick agent jalan via subprocess CLI ([internal/agents/provider/](../agents/provider/)) —
`claude`, `codex`, `gemini` adalah CLI binary, bukan API SDK call. Beda
penting buat reliability strategy:

| | API SDK (n8n / direct integration) | CLI subprocess (wick) |
|---|---|---|
| Schema enforcement | `tool_use` / `response_format` di API call — provider-level | Prompt-based + parse stdout — application-level |
| Output channel | JSON response body | stdout (text atau `--output-format json` kalau CLI support) |
| Latency | ~200-1000ms | ~500-2000ms (proc spawn overhead) |
| Tool ecosystem | per node config | inherit dari agent session (file/bash/MCP) — built-in |
| Cost predictability | tinggi (token count langsung) | indirect (parse CLI usage report) |
| Streaming | server-sent events | stdin/stdout stream |

**Provider responsibility** — tiap provider di
`internal/agents/provider/<name>/` implement schema enforcement sesuai
kapabilitas CLI-nya:

```go
type Provider interface {
    Name() string
    // Existing agent execution methods...

    // New for classify-style nodes:
    StructuredCall(ctx, prompt, schema, opts) (StructuredResult, error)
}

type StructuredResult struct {
    Raw      string                // stdout dari CLI
    Parsed   map[string]any        // JSON-parsed
    OK       bool                  // parse + schema validate sukses
    Error    string                // kalau gagal
    Usage    Usage                 // tokens, cost
}
```

Implementation per provider:
- **Claude Code CLI** — `claude --output-format json --print <prompt>`
  + system prompt yang minta JSON shape. Parse stdout.
- **Codex CLI** — TBD (cek CLI capabilities).
- **Gemini CLI** — TBD.

Kalau CLI ga support strict JSON mode, fallback ke prompt-only + manual
parse (lebih banyak rely pada layer 4-5). Engine ga peduli; provider
abstract.

**Trade-off accepted:** wick lebih lambat per classify call (~1-2s vs
~500ms API), tapi agent dapet tool ecosystem (Read/Edit/Bash/MCP)
built-in untuk node `agent` yang complex. Cocok untuk wick's domain:
AI orchestration, bukan high-throughput API gateway.

### `branch` — non-AI condition (if / switch)

Cheap deterministic routing tanpa LLM. Dua pattern dari node yang sama:

**Boolean expr → if/else:**
```yaml
- id: check-priority
  type: branch
  expr: '{{.Node.fetch-ticket.priority}} == "critical"'

# Edges di graph.edges[]:
# - { from: check-priority, case: "true",  to: page-oncall }
# - { from: check-priority, case: "false", to: queue-normal }
```

**String expr → switch:**
```yaml
- id: route-severity
  type: branch
  expr: '{{.Node.fetch-ticket.severity}}'

# Edges:
# - { from: route-severity, case: critical, to: page-oncall }
# - { from: route-severity, case: high,     to: notify-team }
# - { from: route-severity, case: medium,   to: queue-normal }
# - { from: route-severity, case: low,      to: queue-low }
# - { from: route-severity, case: default,  to: queue-normal }
```

Engine pakai expression evaluator (CEL atau gval) — fast, no LLM cost,
deterministic. Pakai `branch` dulu kalau aturan bisa di-encode pasti.
`classify` cuma pas input bebas (natural language).

**Trade-off:**

| | `branch` | `classify` |
|---|---|---|
| Latency | < 1ms | 200-2000ms (LLM call) |
| Cost | gratis | per-token |
| Determinism | 100% | 95%+ (dgn 6 layer di atas) |
| Input | structured/typed | natural language |
| When | aturan jelas | text ambiguous |

### `agent` — agent reasoning bebas

Spawn agent dengan prompt, return last assistant text. Provider explicit
boleh (claude/codex/gemini), skills allowed sebagai tools.

```yaml
- id: format-answer
  type: agent
  provider: claude                  # optional: claude | codex | gemini
                                    # default: preset's provider → workflow default
  preset: support-responder
  workspace: default                # optional
  prompt: |
    Doc reference: {{.Node.fetch-docs.result}}
    Pertanyaan user: {{.Event.Payload.text}}
    Format jawaban friendly + cite source.
  prompt_file: nodes/format-answer.md
  session: new                      # new | root | persistent (lihat Session di bawah)
  skills:                           # provider-specific skill bundle
    - docs-search
    - weekly-summary
  tools:                            # optional tool allowlist (in addition to skills)
    - http
  max_turns: 5
  # edge: { from: <this-id>, to: send-reply }
```

Provider resolution (kalau ga explicit di node):
1. Preset `default_provider` field
2. Workflow `default_provider` field
3. System default

### Skill discovery per provider

Skills = provider-specific capability bundle (Claude Code skill, Codex
skill if any, Gemini skill if any). Wick query provider untuk list:

```go
// internal/agents/provider/provider.go
type Provider interface {
    Name() string
    Capabilities() Capabilities
    StructuredCall(...) (StructuredResult, error)

    // NEW: skill catalog discovery
    ListSkills(ctx) ([]Skill, error)
}

type Skill struct {
    Name        string                 // "weekly-summary"
    Description string                 // shown di UI dropdown + AI prompt
    InputSchema map[string]any         // optional, kalau skill butuh args
    Source      string                 // "claude-code-builtin" | "user-bundle" | ...
}
```

Per provider impl:
- **Claude Code CLI** — `claude --list-skills --output-format json` atau
  parse `~/.claude/skills/` directory. Cache di status_cache.
- **Codex CLI** — TBD (cek CLI capabilities).
- **Gemini CLI** — TBD.

**UI Inspector flow:**
1. User pilih `provider: claude` di agent node.
2. UI query `provider.ListSkills()` (cached).
3. Skills dropdown muncul dengan checklist.
4. User pilih → write ke `skills: [...]` di YAML.

Validator workflow load reject kalau skill di-list tapi provider ga
support (mis. claude-only skill di-pair dgn gemini provider).

**MCP discovery:** AI lewat MCP query `workflow_skills(provider?)` —
return list per provider. AI tau apa yg available sebelum compose
workflow.

### Session management — CLI subprocess reuse

CLI subprocess spawn ~500ms-1s overhead. Workflow dgn 5 agent node =
5x spawn kalau setiap node fresh process. **`session:` field per node**
control behavior:

| Mode | Behavior | Use case |
|---|---|---|
| `new` (default) | Fresh subprocess per node, ga share context | Isolation, deterministic, paralel-safe |
| `root` | Share single subprocess di-spawn di awal workflow, all `root`-session nodes interact via same agent process. Sequential within workflow run | Multi-turn reasoning across nodes, faster (1× spawn) |
| `persistent` | Subprocess persist across **workflow runs** (session ID = workflow id). Context inherit dari run sebelumnya | Long-running assistant pattern, learn-from-history |

```yaml
- id: classify-intent
  type: classify
  session: new                      # default, classify usually independent

- id: deep-analysis
  type: agent
  session: root                     # share context dgn agent node lain di run yg sama
  ...

- id: ongoing-monitor
  type: agent
  session: persistent               # context bawa dari run sebelumnya
  ...
```

**Engine session map** per workflow run:
- `state.json` tambah `sessions: {root: <session_record>, persistent: <session_record>}`
- `session_record = {pid, started_at, last_heartbeat, transcript_path}`
- Engine spawn root proc lazy (saat node pertama dgn `session: root` jalan).
- Saat workflow end, kill `new` + `root` sessions, persist `persistent` di-detach.

**Crash detection + recovery:**

```
Per-node execution dgn root/persistent session:
1. Engine read state.json → session_record.pid
2. Check process alive: kill -0 <pid> (Unix) atau OpenProcess (Windows)
3. Alive + last_heartbeat < 60s ago → use existing session
4. Alive + heartbeat stale (>60s) → send health probe (special prompt)
   Probe OK → continue
   Probe timeout/err → mark stale, re-spawn
5. Dead → re-spawn fresh subprocess
   Engine log: "session <id> dead, respawned. Context dari pre-crash lost."
   Next node run dgn fresh agent (cold start, no transcript replay)
```

**Crash mid-node (workflow crashed during node exec):**
- state.json `current = <node>`, last write before node start.
- Restart engine → load state, see `current` = node mid-run, no output yet.
- Re-execute `current` node from scratch (assume idempotent).
- Engine emit warning to events.jsonl: "node X re-executed after crash".

**Crash mid-session (root session subprocess died, workflow run was OK):**
- Pre-crash: nodes [A, B] used root session, both completed.
- Post-crash: state.json has outputs for A, B. session_record stale.
- Next node C (also `session: root`) — engine detect stale, re-spawn.
- C run dgn fresh session, no context dari A/B prompt history.
- Workflow continues. Document this as known trade-off.

**Persistent session lifecycle:**
- Subprocess detached, written ke wick global session registry
- Cleanup: idle > 24h (configurable) → terminate
- Manual cleanup: `wick workflow session kill <id>` CLI / MCP op
- Restart wick: persistent sessions lost (TBD: future = serialize transcript to disk for resume)

**Concurrent runs same workflow + session:**
- `session: new` → no conflict (fresh per node).
- `session: root` → root SCOPED PER RUN (each run has own root). No
  shared state between concurrent runs.
- `session: persistent` → shared across runs. Engine SERIALIZE access
  per session ID (queue + lock). Multiple concurrent runs that hit
  persistent node = sequential through that node. Document throughput
  cap.

**Parallel branches + root session = serialize.** Engine queue
`root`-session nodes sequentially within run (single proc can't handle
concurrent prompts cleanly). Workflow yg butuh parallel + share context
= refactor pakai merge node yg combine outputs after parallel `new`.

**Session ID format:**
- `new`: ephemeral, no ID stored
- `root`: `workflow:<id>:run:<run_id>:root`
- `persistent`: `workflow:<id>:persistent`

**Default decision:**
- `classify` default `session: new` (independent classification, no
  shared context needed)
- `agent` default `session: new` (isolation safe, opt-in `root` kalau
  butuh multi-turn flow)
- `persistent` selalu explicit opt-in

Validator reject kalau skill list ga match provider (e.g. Claude skill
di-list tapi provider gemini).

Output: `{{.Node.format-answer.text}}` (last assistant), `.tools_used`,
`.skills_used`, `.tokens`, `.cost`.

### `connector` — invoke external API operation

Direct call ke connector module operation (lihat
[docs/guide/connector-module.md](../../docs/guide/connector-module.md)).
Reuse 100% existing connector infrastructure — Configs, Input schema,
ExecuteFunc, audit, encrypted fields, tag-based visibility.

```yaml
- id: create-ticket
  type: connector
  module: github                    # connector row slug dari /manager/connectors/github
  op: create_issue                  # operation key declared di connectors/github/
  args:                             # validated terhadap Input struct
    repo: "acme/inbox"
    title: "{{.Event.Payload.text | truncate 80}}"
    body: |
      Reporter: {{.Event.Payload.user}}
      Original: {{.Event.Payload.text}}
  # edge: { from: <this-id>, to: reply-link }
```

In-process call (skip MCP HTTP transport). Same code path = audit ke
`connector_runs`, same encrypted-fields layer, destructive flag
respected (AI guard surface kalau `OpDestructive`).

Output: `Operation.Execute()` return value (typed per connector's `Input`).

### `channel` — channel outbound action (symmetric dgn trigger)

Send message via channel module
([internal/agents/channels/](../agents/channels/)). Same module sbg
trigger inbound — di trigger context = subscribe events, di node
context = invoke outbound op.

```yaml
- id: reply-with-link
  type: channel
  channel: slack                    # channel module name
  op: reply_thread                  # action declared di channel.Actions()
  args:                             # validated per ActionSpec.InputSchema
    channel: "{{.Event.Payload.channel_id}}"
    thread:  "{{.Event.Payload.thread}}"
    text: |
      Bug reported · Tracked in {{.Node.create-ticket.html_url}}
```

Lihat §7 channel registry buat Actions list. `Actions()` declared per
channel impl (slack/telegram/rest). Engine route `channel + op` →
`Channel.Send(ctx, op, args)`.

Output: structured return per `ActionSpec.OutputSchema`.

**Disambiguasi `channel` di trigger vs action node:**

| Position | Behavior |
|---|---|
| `triggers: []` | Subscribe events — uses `Channel.TriggerSpecs()` + `Subscribe()` |
| `graph.nodes: []` | Invoke outbound op — uses `Channel.Send(action, args)` |

Position di YAML disambiguate. Engine pilih impl path.

### `shell` — shell exec

```yaml
- id: check-disk
  type: shell
  command: ["bash", "nodes/check-disk.sh"]
  env:
    TARGET_HOST: prod-1.abc.com
  cwd: ""                           # default = folder workflow
  timeout_sec: 30
  parse_output: json                # raw (default) | json | lines
  # edge: { from: <this-id>, to: analyze }
```

Output: `{{.Node.check-disk.stdout}}`, `.stderr`, `.exit_code`,
`.parsed` (kalau `parse_output: json|lines`).

### `python` — python script

```yaml
- id: process-data
  type: python
  script: nodes/process.py
  requirements: requirements.txt
  python: python3                   # default = python3 di PATH
  env:
    DATA: "{{.Node.fetch.json}}"
  parse_output: json
  # edge: { from: <this-id>, to: notify-result }
```

`.venv/` per workflow, hash-validated dari `requirements.txt`.

### `http` — HTTP request

```yaml
- id: fetch-issues
  type: http
  method: GET
  url: https://api.github.com/repos/{{.Env.REPO}}/issues
  headers:                          # map[string]string — each value template-rendered
    Authorization: "Bearer {{.Secret.GITHUB_PAT}}"
  query:                            # map[string]string — each value template-rendered
    since: "{{.Event.At | addHours -24}}"
  body: ""                          # only used for POST/PUT/PATCH/DELETE
  parse_response: json              # raw | json | bytes (default raw); ignored for GET
  timeout_sec: 30
  retry:
    max: 3
    backoff_sec: 2
  arg_modes:                        # optional — per-field render mode
    url: fixed                      # "fixed" = pass value verbatim, skip template render
    body: expression                # "expression" = template.Render (default behaviour)
  # edge: { from: <this-id>, to: summarize }
```

Output: `.status`, `.headers`, `.body`, `.json` (kalau parse).

**Inspector UI:**
- Headers / Query render sebagai `kvlist` row editor (one row per key/value pair, click `+ Add Row` to append). Each value is rendered as a Go template, so you can pull tokens from upstream nodes (e.g. `Authorization: Bearer {{.Node.login.token}}`).
- `url`, `body`, `method`, `timeout_sec` each carry a `Fixed | Expression` toggle pill. Fixed = passed verbatim, Expression = `template.Render` against the run context (default for backward compat). State persists as `arg_modes` in YAML.
- `body` + `parse_response` rows are hidden when `method = GET` (`visible_when=method:POST|PUT|PATCH|DELETE`).

### `db_query` — SQL query

```yaml
- id: get-active-users
  type: db_query
  database: main                    # configured DSN name; lihat §20 security
  query: |
    SELECT id, email FROM users
    WHERE last_active > $1
    LIMIT 100
  args:
    - "{{.Event.At | addHours -24}}"
  # edge: { from: <this-id>, to: process-users }
```

Output: `.rows` (array of objects), `.row_count`.

### `dataset_*` — wick-native data store

Wick-native data tables (lihat §12 Datasets). Single shared Postgres
table `wick_datasets_rows`, schema YAML-defined, akses cuma lewat node
types ini (no raw SQL).

**6 variant:**

| Node | Use case | Output | Branching |
|---|---|---|---|
| `dataset_exists` | "ada row yang match `where`?" | `.found: bool` | `cases: {"true", "false"}` |
| `dataset_get` | ambil 1 row by primary key | `.found: bool`, `.row: object/null` | `cases: {found, not_found}` |
| `dataset_query` | multi-row search | `.rows: []`, `.row_count`, `.has_more` | `next:` atau branch eksternal |
| `dataset_count` | count tanpa load row | `.count: int` | `next:` |
| `dataset_insert` | INSERT row, fail kalau pk conflict | `.inserted_pk`, `.success` | `next:` |
| `dataset_upsert` | INSERT atau UPDATE based on pk | `.action: "insert"\|"update"`, `.row` | `next:` |
| `dataset_delete` | DELETE rows matching where | `.deleted_count` | `next:` |

**Pattern: webhook dedup (1 node check, lebih ringkas dari query+branch):**

```yaml
- id: check-handled
  type: dataset_exists
  dataset: events
  where:
    id: "{{.Event.Payload.event_id}}"
    is_processed: true

# Edges:
# - { from: check-handled, case: "true",  to: skip-already-done }
# - { from: check-handled, case: "false", to: process }
```

**Pattern: load row by PK + branch by existence:**

```yaml
- id: load-user-state
  type: dataset_get
  dataset: users
  key:
    id: "{{.Event.Payload.user}}"

# Edges:
# - { from: load-user-state, case: found,     to: enrich-existing }   # .row accessible
# - { from: load-user-state, case: not_found, to: create-new-user }

- id: enrich-existing
  type: agent
  prompt: |
    User {{.Node.load-user-state.row.name}} (last seen
    {{.Node.load-user-state.row.last_seen}}) tanya: {{.Event.Payload.text}}
```

**Pattern: multi-row search dgn pagination:**

```yaml
- id: list-pending
  type: dataset_query
  dataset: tickets
  where:
    status: pending
    assignee: "{{.Event.Payload.user}}"
  order_by: [{ column: created_at, direction: desc }]
  limit: 50
  # edge: { from: <this-id>, to: process-each }
```

**Pattern: idempotent upsert (cron poll):**

```yaml
- type: dataset_upsert
  dataset: events
  key: [id]                              # primary key columns
  row:
    id: "{{.Event.Payload.event_id}}"
    event_name: "{{.Event.Payload.event_name}}"
    status: received
    is_processed: false
    received_at: "{{.Event.At}}"
```

**Kapan pakai mana:**

| Kasus | Node |
|---|---|
| Dedup webhook ("udah handle?") | `dataset_exists` |
| Load row by PK terus pake fieldnya | `dataset_get` |
| Cari multiple rows (filter, paginate, sort) | `dataset_query` |
| Count tanpa load row | `dataset_count` |
| Insert fresh, fail kalau ada conflict | `dataset_insert` |
| Insert-or-update (idempotent) | `dataset_upsert` |
| Hapus satu/banyak row | `dataset_delete` |

**Beda dari `db_query`:**
- `db_query` = external SQL DB user-configured (`internal/connectors/`).
- `dataset_*` = wick-internal table di wick's Postgres, schema-aware,
  MCP-discoverable, UI table view built-in, no raw SQL.

Use `db_query` kalau data hidup di system lain. Use `dataset_*` kalau
data baru lahir dari workflow (state, dedup, cache, internal records).

### `transform` — jq / template / Go template

```yaml
- id: extract-ids
  type: transform
  engine: jq                        # jq | gotemplate | jsonpath
  input: "{{.Node.get-users.rows}}"
  expression: "[.[] | .id]"
  # edge: { from: <this-id>, to: enrich }
```

Output: `.result` (whatever expression returns).

### `parallel` — fan-out

```yaml
- id: gather
  type: parallel
  branches:
    - grafana-fetch
    - db-fetch
    - shell-check
  # edge: { from: <this-id>, to: merge-results               # implicit wait-for-all }
```

Engine spawn 3 sub-flows in parallel, all 3 must complete (success or
fail per `on_failure` policy) before `next` runs. Output =
`{{.Node.gather.branches}}` = object dgn key per branch ID.

### `merge` — fan-in (explicit, optional)

```yaml
- id: combine
  type: merge
  inputs: [a, b, c]                 # wait until all listed nodes complete
  strategy: object                  # object | array | first | last
  # edge: { from: <this-id>, to: process }
```

Output structure dari `strategy`. `parallel` punya implicit merge via
`next:`. Use `merge` standalone kalau topology rumit (DAG murni dgn
manual edges).

### `end` — terminator

```yaml
- id: silent-end
  type: end
  result: ""                        # final workflow result, default ""
```

Optional. Workflow juga end kalau node terakhir `next:` kosong.

### Output schema (optional declare)

```yaml
- id: classify-intent
  type: classify
  ...
  output_schema:
    type: object
    properties:
      verdict: { type: string, enum: [bug, question, feature_request, other] }
      confidence: { type: number }
```

Engine validate output. Mismatch → `halt` dgn error. Validator pake
[gojsonschema](https://github.com/xeipuuv/gojsonschema) atau setara.

Tanpa declare → runtime infer, no check. Tradeoff fleksibel vs catch
errors awal.

### Output reference syntax

Template var:
- `{{.Node.<label>}}` — full output object (label is the user-facing name shown in the inspector; falls back to id when label empty)
- `{{.Node.<label>.<field>}}` — sub-field
- `{{.Node.<label>.<field> | <filter>}}` — Go template pipe filter
- **Trigger nodes** also live under `.Node.<label>` — payload at `{{.Node.<trigger-label>.payload.<key>}}`, envelope keys `type/subtype/channel/at` directly under the trigger label
- `{{.Event.*}}` — **legacy**, still resolved by the engine for older workflows; new workflows should use `{{.Node.<trigger-label>.payload.…}}` so triggers and regular nodes share the same access pattern
- `{{.Env.<NAME>}}` — workflow env value, from `env.yaml` (UI-managed, hand-edit OK) — lihat §11
- `{{.Secret.<NAME>}}` — encrypted secret, decrypt runtime. Schema declare `widget: secret` di workflow.yaml, value stored encrypted di `env.yaml` — lihat §11
- `{{.Workflow.<field>}}` — workflow metadata (ID, Version, Name)
- `{{.Run.<field>}}` — runtime metadata (ID, StartedAt)
- `{{.Dataset.<alias>}}` — dataset binding from `datasets:` field — lihat §12

**Label vs id:** Inspector exposes `label` (free-form, must be a Go identifier — letters/digits/underscore, no spaces, unique within workflow). Renaming a label cascades through every `{{.Node.<old>...}}` reference, `index .Node "<old>"` form, graph edges, trigger `entry_node`, and `session_from`. The internal numeric Drawflow id stays stable so saved YAML and runtime state survive renames. When label is empty or not a valid identifier the canvas falls back to id for the template path.

---

