# Workflow — Desain & State

Status: **proposed** — belum ada kode. Doc ini kontrak desain sebelum
implementasi.
Update terakhir: 2026-05-14.

> **Pas implement, baca DUA file:**
> - **`workflow-design.md` (ini)** — developer contract, spec lengkap
> - **`workflow-mockup.html`** — visual reference (UI layout, canvas, run timeline, anatomy diagrams)
>
> User-facing docs: belum ada. Setelah implement, tulis
> `docs/guide/workflows.md` yang ngajarin admin cara bikin workflow.
> Doc ini sumber kebenaran developer, bukan panduan user.

Workflow gantiin konsep "routine" sebelumnya. Routine = workflow dengan
1 node body. Schema YAML routine lama auto-translate ke workflow saat
load.

---

## Implementation roadmap

13 phase berurutan. Tiap phase = vertical slice (tested, mergeable PR).
Jangan skip — phase N+1 butuh phase N. Tiap phase **WAJIB include unit
+ integration test** sesuai §14.

### Status (2026-05-15)

| Phase | Status | Notes |
|---|---|---|
| 1. Foundation | ✅ done | types + parser + validator + Service di `internal/agents/workflow/` |
| 2. Engine core | ✅ done | walker + shell/http/branch/transform/end executors + state |
| 3. Parallel + merge | ✅ done | fan-out goroutines, 4 merge strategies, diamond proven |
| 4. Trigger router | ✅ done | router + queue + dedup + webhook handler |
| 5. Channel integration | ✅ done | WorkflowChannel iface + registry + executor + implicit-reply injection |
| 6. Connector integration | ✅ done | in-process Execute() dispatch + audit hook |
| 7. Provider + AI nodes | ✅ done | 6-layer classify + agent + session ID derivation; provider impls deferred |
| 8. Datasets | ✅ done | in-mem store + 7 dataset_* executors; Postgres impl deferred |
| 9. Env + secrets | ✅ done | schema/value resolver + secret leak guard |
| 10. AI guard | ✅ done | 5 rule packs + ContentHash + override flow |
| 11. Interactive + error trigger | ✅ done | Slack interactive Action specs + error router with depth guard |
| 12. MCP + canvas + test framework | ✅ done | MCPOps surface + Canvas mutator + JSON-fixture TestRunner |
| 13. Polish + observability | ✅ done | Bootstrap + HotReload + CleanupRuns + CostTracker |
| 14. Canvas editor v1 | ✅ done | Drawflow + draft/publish, soft validation, port hit-target, reverse drag-to-connect, trigger fan-out, palette drawer, n8n-style toolbar |
| 15. Run observability v1 | ✅ done | SSE-driven per-node progress (pulsing borders, latency badges), bottom Logs tab live tail, structured zerolog mirror with `wf_run_id` + `request_id` correlation, Loki-ready payload shape |
| 16. Per-trigger routing | ✅ done | each canvas trigger node = one wf.Trigger entry with own EntryNode; multi-trigger workflows route via `evt.Type` match; Execute workflow picker on canvas replaces toolbar Run Now |
| 17. Sharded run index | ✅ done | `runs/index/YYYY-MM-DD-NN.jsonl` keeps Runs panel pagination constant-time at any history size; generic `internal/shardedlog/` module reusable for sessions / log views |
| 18. Inspector debug shell | ✅ done | n8n-style 3-col modal (Input \| Parameters \| Output) opens on dblclick/right-click; Execute step runs one node in isolation; output preview JSON/Schema tabs |

Deferred from above (out-of-scope for the package, wire when concrete UIs land):
- fsnotify watcher (Bootstrap + HotReload entrypoints sudah ada; watcher
  loop tinggal mount di server.go)
- Postgres-backed DatasetService + Postgres `wick_workflow_state` table
  (in-memory + per-workflow `state.json` shim sudah jalan)
- Per-provider impls (Claude Code / Codex / Gemini) — abstraction
  Provider tinggal di-implement di `internal/agents/provider/`
- CLI `wick workflow test <slug>` — TestRunner sudah ada, tinggal cobra
  subcommand
- Webhook HMAC enforcement (VerifyHMAC helper ada, dispatch-side wiring
  belum)
- Loki push for the structured log mirror — payload shape is already
  Loki-compatible (label dimensions = `wf_slug`/`wf_run_id`/`wf_event`),
  just need the HTTP sink wired
- Run history import from Loki — reverse direction, rebuild
  `runs/<id>/state.json` from log entries when local files purged
- Per-trigger fan-out in engine — today's engine fires one chain
  per trigger event; fan-out (one trigger → many parallel chains)
  needs Router/Engine support for multi-EntryNode per Trigger

**Phase 1 — Foundation** *(folder + types, no execution)* ✅

Implementation:
- [ ] `internal/agents/workflow/` package skeleton
- [ ] Domain types per §8: `Workflow`, `Graph`, `Edge`, `Node`, `Trigger`, `RunState`
- [ ] YAML schema parser + validator (cycle detect Kahn's, edge ref resolution, case label rules, default mandatory)
- [ ] File CRUD via Service interface (List/Load/Create/Update/Delete/Toggle)
- [ ] Atomic write tmp+rename

Unit tests:
- [ ] `parser_test.go` — 10+ valid YAML fixtures parse to expected struct
- [ ] `parser_test.go` — 10+ invalid YAML return specific error (cycle, missing edge target, missing default case, duplicate node ID, unreachable node, missing entry, missing required field)
- [ ] `validator_test.go` — cycle detect (Kahn's) on 5+ graph topologies
- [ ] `validator_test.go` — edge case rules (case only on classify/branch, default mandatory, fan-in only to merge)
- [ ] `service_test.go` — Service CRUD ops dgn temp folder, atomic write verify (tmp+rename never corrupt)
- [ ] `types_test.go` — Edge/Node/Trigger YAML marshal+unmarshal round-trip

**Phase 2 — Engine core** *(execution, simple linear flow only)* ✅

Implementation:
- [ ] DAG walker algo per §6 (edge-first resolution, classify/branch case filter, fan-out detect)
- [ ] State persistence: `runs/<id>/state.json` + `events.jsonl` atomic write per node completion
- [ ] Built-in executors: `shell`, `http`, `branch`, `end`, `transform`
- [ ] Resume from crash (load state.json, re-exec current node)
- [ ] Output ref template render (`{{.Event.X}}`, `{{.Node.X.Y}}`, `{{.Env.X}}`, `{{.Secret.X}}`)

Unit tests:
- [ ] `engine_test.go` — walker traverse linear chain 5 nodes, output captured per node
- [ ] `engine_test.go` — edge resolution: classify verdict → matching edge case, no match → default
- [ ] `engine_test.go` — fan-out: 1 source 3 outgoing edges (no case) → 3 paralel exec
- [ ] `state_test.go` — state.json atomic write, recovery from partial write
- [ ] `state_test.go` — events.jsonl append correct order, resume reads last completed
- [ ] `executor_shell_test.go` — shell exec dgn mock command, stdout/exit_code/timeout
- [ ] `executor_http_test.go` — http dgn `httptest.Server`, retry policy, parse json/raw
- [ ] `executor_branch_test.go` — boolean expr (CEL/gval) + string switch, default fallback
- [ ] `executor_transform_test.go` — jq + gotemplate + jsonpath
- [ ] `template_test.go` — render `{{.Event.X}}` / `{{.Node.X.Y}}` / `{{.Env.X}}` / `{{.Secret.X}}`, secret leak prevention
- [ ] `template_test.go` — missing field → render error (not `<no value>`)

Integration test:
- [ ] `integration_linear_test.go` — end-to-end 3-node linear workflow dgn mock executors, verify state + events

**Phase 3 — Parallel + merge** *(DAG topology)* ✅

Implementation:
- [ ] `parallel` node executor (fan-out goroutines, on_failure per-branch policy)
- [ ] `merge` node executor (wait-for-all per §6 readiness rule, strategy: object/array/first/last)
- [ ] Fan-out via multiple edges (no explicit parallel node)

Unit tests:
- [ ] `executor_parallel_test.go` — 3 branches success, output keyed per branch
- [ ] `executor_parallel_test.go` — 1 branch fail dgn `on_failure: halt` → cancel sisanya
- [ ] `executor_parallel_test.go` — 1 branch fail dgn `on_failure: skip` → continue, output `{error: ...}`
- [ ] `executor_parallel_test.go` — fallback policy dgn fallback node ID
- [ ] `executor_merge_test.go` — wait-for-all 3 inputs, strategy `object` (keyed by node ID)
- [ ] `executor_merge_test.go` — strategy `array` preserve inputs[] order
- [ ] `executor_merge_test.go` — strategy `first` / `last` race detection
- [ ] `engine_test.go` — fan-out via multiple edges (no parallel node) spawn N paralel

Integration test:
- [ ] `integration_diamond_test.go` — diamond topology (A → [B,C] → merge → D), partial failure, all strategies

**Phase 4 — Trigger router** ✅

Implementation:
- [ ] Cron path: register sebagai `jobs.Module` Key `workflow:<slug>:cron-<idx>`, reuse existing scheduler
- [ ] Manual trigger: UI button → enqueue
- [ ] Webhook handler: mount `/hooks/<slug>/<path>`, HMAC verify, path templating
- [ ] Per-trigger `entry_node` override
- [ ] Per-workflow FIFO queue + dedup (LRU + file fallback)

Unit tests:
- [ ] `router_test.go` — trigger match: cron, manual, webhook, schedule_at
- [ ] `router_test.go` — per-trigger `entry_node` override picks correct start node
- [ ] `router_test.go` — multi-trigger workflow: each trigger fires correct entry
- [ ] `webhook_test.go` — HMAC SHA-256 verify, reject invalid signature
- [ ] `webhook_test.go` — path templating `/hooks/orders/{id}` → `Event.Payload.params.id`
- [ ] `webhook_test.go` — body parse: json/form/raw
- [ ] `queue_test.go` — FIFO per workflow, overflow policy (drop_oldest/drop_new/reject)
- [ ] `dedup_test.go` — LRU evict, TTL expiry, same event_id skip

Integration test:
- [ ] `integration_triggers_test.go` — workflow w/ 3 triggers (cron + manual + webhook), each fires correct path

**Phase 5 — Channel integration** *(symmetric trigger + action)* ✅

Implementation:
- [ ] Channel interface extension: `TriggerSpecs()`, `Actions()`, `Send()`, `Subscribe()`
- [ ] Channel registry router (`OnAnyMessage` hook fire trigger router)
- [ ] `type: channel` action node executor
- [ ] Slack channel concrete impl per §7 (start dgn `send_message`, `reply_thread`, `send_dm`; interactive `post_message_with_button`, `open_modal` di Phase 11)
- [ ] Implicit reply-to-source synthetic node injection
- [ ] Channel event subtypes: `message` first (interactive subtypes `action`/`submission` di Phase 11)

Unit tests:
- [ ] `channel_registry_test.go` — register/discover channels, TriggerSpec + Actions schema
- [ ] `channel_action_test.go` — `type: channel` executor resolves channel + op via registry
- [ ] `channel_action_test.go` — InputSchema validation reject malformed args
- [ ] `implicit_reply_test.go` — detect explicit reply node → skip synthetic
- [ ] `implicit_reply_test.go` — no explicit reply + `reply_source: true` → synthetic injected
- [ ] `implicit_reply_test.go` — `reply_source: false` → skip
- [ ] `slack_actions_test.go` — `send_message` builds correct API payload (mock HTTP)
- [ ] `slack_actions_test.go` — `reply_thread` includes thread_ts
- [ ] `slack_trigger_test.go` — message event payload shape per §7

Integration test:
- [ ] `integration_slack_test.go` — Slack message trigger → workflow → reply (mock Slack API)

**Phase 6 — Connector integration** *(reuse existing)* ✅

Implementation:
- [ ] `type: connector` node executor — in-process call `Operation.Execute()`
- [ ] Resolve `module + op` ke connector row + operation
- [ ] Args binding terhadap `Input` struct + validation
- [ ] Audit ke existing `connector_runs` table
- [ ] Destructive flag surfaced via AI guard (Phase 10)

Unit tests:
- [ ] `executor_connector_test.go` — resolve module + op + row → invoke Execute
- [ ] `executor_connector_test.go` — args bind ke Input struct, missing required → error
- [ ] `executor_connector_test.go` — template `{{.Event.X}}` di args render correctly
- [ ] `executor_connector_test.go` — destructive op flag surfaced di audit + guard signal
- [ ] `executor_connector_test.go` — `connector_runs` audit row written

Integration test:
- [ ] `integration_connector_test.go` — workflow invokes `crudcrud` connector (existing template module), verify audit + output ref

**Phase 7 — Provider + AI nodes** *(classify, agent, sessions)* ✅

Implementation:
- [ ] Provider interface extension: `Capabilities()`, `StructuredCall()`, `ListSkills()`
- [ ] Per-provider impl (Claude Code first): `--output-format json` parser, skill listing
- [ ] `type: classify` executor dgn 6-layer reliability (structured_output, normalize, exact, fuzzy, retry, confidence_threshold)
- [ ] `type: agent` executor (provider, skills, session field)
- [ ] Session map per §5 Session management: `new` (default), `root` (lazy spawn), `persistent` (cross-run)
- [ ] Crash recovery (PID check, heartbeat, respawn)

Unit tests:
- [ ] `provider_claude_test.go` — `StructuredCall()` builds correct CLI args, parses JSON output
- [ ] `provider_claude_test.go` — `ListSkills()` reads `~/.claude/skills/` directory, caches result
- [ ] `executor_classify_test.go` — layer 1 (structured_output) parse provider JSON response
- [ ] `executor_classify_test.go` — layer 2 (normalize) "Bug." → "bug", lowercase + strip punct
- [ ] `executor_classify_test.go` — layer 3 (exact match) edge case match
- [ ] `executor_classify_test.go` — layer 4 (fuzzy_match) "bug_report" → "bug" Levenshtein < 3
- [ ] `executor_classify_test.go` — layer 5 (retry) retry on mismatch dgn stricter prompt
- [ ] `executor_classify_test.go` — layer 6 (confidence_threshold) < threshold → default case
- [ ] `executor_classify_test.go` — `default` case mandatory, missing → halt
- [ ] `executor_agent_test.go` — agent node spawn dgn skills + tools allowlist
- [ ] `session_test.go` — `new` mode fresh process per node
- [ ] `session_test.go` — `root` mode lazy spawn pertama, reuse subsequent root nodes
- [ ] `session_test.go` — `persistent` mode persist cross-run via session ID `workflow:<slug>:persistent`
- [ ] `session_crash_test.go` — PID check dead → respawn, log "session lost context"
- [ ] `session_crash_test.go` — heartbeat stale → probe → respawn on timeout
- [ ] `session_concurrent_test.go` — concurrent runs persistent session → serialize via lock

Integration test:
- [ ] `integration_classify_test.go` — workflow dgn classify dgn 5 mock provider responses (success, fuzzy, default fallback, low confidence, retry)
- [ ] `integration_agent_test.go` — agent dgn skill invocation, root session shared 2 agent nodes

**Phase 8 — Datasets** ✅

Implementation:
- [ ] `wick_datasets_rows` table migration (Postgres)
- [ ] Dataset Service interface per §12: Create/UpdateSchema/Query/Insert/Upsert/Delete/Count/Get/Exists
- [ ] Schema validation (strict/lax/extensible), JSONB shape enforcement
- [ ] Partial GIN index management per declared `indexed: true` columns
- [ ] Node executors: `dataset_query`/`dataset_get`/`dataset_exists`/`dataset_insert`/`dataset_upsert`/`dataset_delete`/`dataset_count`
- [ ] File-based versioning (`history/v<N>.yaml`), version pin enforcement
- [ ] Adoption flow (orphan rows detect → schema infer)
- [ ] Migration job (atomic batch + rollback)

Unit tests:
- [ ] `dataset_schema_test.go` — strict mode reject extra key, lax accept + warn
- [ ] `dataset_schema_test.go` — column type validation (string, int, float, bool, timestamp, json, enum)
- [ ] `dataset_schema_test.go` — primary_key extract to `pk` column
- [ ] `dataset_service_test.go` — Query dgn structured where → parameterized SQL (no injection)
- [ ] `dataset_service_test.go` — Insert reject schema mismatch (strict)
- [ ] `dataset_service_test.go` — Upsert idempotent by pk
- [ ] `dataset_service_test.go` — Delete dgn where filter
- [ ] `executor_dataset_test.go` — all 7 dataset_* nodes, branching cases (`true/false`, `found/not_found`)
- [ ] `dataset_versioning_test.go` — schema bump → snapshot `history/v<N>.yaml`, version increment
- [ ] `dataset_versioning_test.go` — rollback swap files + version meta
- [ ] `dataset_migration_test.go` — dry-run validate without write
- [ ] `dataset_migration_test.go` — apply atomic per-batch (1000 rows), idempotent re-run skip migrated
- [ ] `dataset_migration_test.go` — fail mid-batch → rollback transaction
- [ ] `dataset_adoption_test.go` — orphan rows detect, infer schema from samples
- [ ] `dataset_access_test.go` — workflow not in `access.workflows` → reject write
- [ ] `dataset_access_test.go` — `row_filter: by_creator` enforce
- [ ] `dataset_access_test.go` — `expected_version` pin mismatch → workflow load reject

Integration test:
- [ ] `integration_dedup_test.go` — webhook dedup workflow (dataset_exists + branch + upsert)
- [ ] `integration_sharing_test.go` — 2 workflows share dataset, strict schema enforced, both write OK

**Phase 9 — Environment & secrets** ✅

Implementation:
- [ ] `env:` schema parser di workflow.yaml (reuse config-tags vocab)
- [ ] `env.yaml` values loader + writer (atomic)
- [ ] Secret encryption integration (existing `wick_enc_` layer)
- [ ] Validator: required field check, type validation, kvlist/picker JSON shape
- [ ] Env reference resolver (`{{.Env.X}}` non-secret only, `{{.Secret.X}}` decrypt runtime)
- [ ] MCP ops: `workflow_get_env_schema`, `workflow_get_env_values`, `workflow_set_env_values`

Unit tests:
- [ ] `env_schema_test.go` — parse 10 widget types (text/textarea/secret/number/checkbox/dropdown/email/url/color/date/datetime/kvlist/picker)
- [ ] `env_schema_test.go` — modifier (required/locked/regen/hidden/visible_when) honored
- [ ] `env_schema_test.go` — kvlist + picker JSON shape `[{id, name}]`
- [ ] `env_values_test.go` — atomic write tmp+rename
- [ ] `env_values_test.go` — secret encrypted via `wick_enc_` saat save
- [ ] `env_values_test.go` — required missing → workflow load reject "Missing config"
- [ ] `env_resolver_test.go` — `{{.Env.X}}` non-secret render plain
- [ ] `env_resolver_test.go` — `{{.Secret.X}}` decrypt + render runtime
- [ ] `env_resolver_test.go` — secret di `{{.Env.X}}` field → reject (prevent leak)
- [ ] `env_resolver_test.go` — orphan field warning (in env.yaml but not schema)

Integration test:
- [ ] `integration_env_test.go` — workflow w/ secret → run → verify secret never in events.jsonl, run logs

**Phase 10 — AI guard + governance** ✅

Implementation:
- [ ] Guard config struct (entity.Config reflected)
- [ ] Default rules (destructive shell, prompt injection, network allowlist, dst per §17)
- [ ] Guard runner: spawn ephemeral agent, review folder, parse JSON verdict
- [ ] Mode: warn / block / off
- [ ] Override flow dgn audit log
- [ ] `wick_workflow_state` table (approved version + hash + governance)
- [ ] Auto-extend on cosmetic diff, stale on material

Unit tests:
- [ ] `guard_rules_test.go` — destructive shell pattern detect (`rm -rf`, `dd`, `mkfs`)
- [ ] `guard_rules_test.go` — prompt injection detect (raw `{{.Event.Payload.text}}` to shell)
- [ ] `guard_rules_test.go` — network non-allowlisted host
- [ ] `guard_rules_test.go` — plaintext secret di YAML
- [ ] `guard_rules_test.go` — classify prompt manipulable
- [ ] `guard_rules_test.go` — db_query unparameterized
- [ ] `guard_runner_test.go` — spawn agent, parse JSON `{ok, violations[]}`, timeout fail-closed
- [ ] `guard_mode_test.go` — warn/block/off behavior matrix
- [ ] `state_test.go` — `wick_workflow_state` table CRUD
- [ ] `state_test.go` — content_hash compute, fresh vs edited vs stale
- [ ] `state_test.go` — auto-extend approval on cosmetic diff (engine verdict)

Integration test:
- [ ] `integration_guard_test.go` — workflow w/ `rm -rf /` di shell node → guard blocks publish
- [ ] `integration_guard_test.go` — override w/ reasoning → commit + audit log entry

**Phase 11 — Interactive + error trigger** *(advanced channel + on_error)* ✅

Implementation:
- [ ] Slack interactive Actions: `post_message_with_button`, `open_modal`, `react`, `update_message`
- [ ] Channel event subtypes: `action`, `submission` (Slack interactive events)
- [ ] Multi-trigger workflow w/ per-trigger entry_node (test ticket-flow per §3 UC4)
- [ ] `type: error` trigger
- [ ] `on_error:` workflow binding di workflow.yaml
- [ ] Loop protection (max 3 nested error workflows)

Unit tests:
- [ ] `slack_interactive_test.go` — `post_message_with_button` builds Block Kit payload
- [ ] `slack_interactive_test.go` — `open_modal` uses trigger_id correctly
- [ ] `slack_interactive_test.go` — modal field types (text/textarea/select/multiselect/datepicker)
- [ ] `channel_subtype_test.go` — action event payload (trigger_id, action_id, value, message_ts, metadata)
- [ ] `channel_subtype_test.go` — submission event payload (view_id, callback_id, values, metadata)
- [ ] `multi_trigger_test.go` — 3 trigger w/ entry_node beda → 3 separate runs
- [ ] `error_trigger_test.go` — workflow A fail → fire workflow B (subscribe type: error)
- [ ] `error_trigger_test.go` — payload shape (source_workflow, failed_node, error, severity, state_snapshot)
- [ ] `error_trigger_test.go` — severity filter
- [ ] `error_trigger_test.go` — dedup_ttl_sec avoid error storm
- [ ] `error_loop_test.go` — error-handler workflow itself fails → no nested error fire (depth track), max 3 nested

Integration test:
- [ ] `integration_ticket_flow_test.go` — UC4 multi-stage interactive: message → button → action → modal → submission → ticket → reply
- [ ] `integration_error_handler_test.go` — failing workflow fires error-handler, escalates per severity

**Phase 12 — UI + MCP surface + Test framework** ✅ *(MCP + test framework done; UI tab/Drawflow pending)*

Implementation:
- [ ] Tab Workflows di `internal/tools/agents/` (list, detail, edit, runs)
- [ ] Canvas editor (Drawflow integration) per §10 — node palette, edges editor, inspector
- [ ] Test panel: per-node + full-flow + coverage map
- [ ] Test framework engine per §14:
  - `EngineContext.Mode` test/production
  - MockRegistry + service wrapping
  - CaptureLog untuk assertions
  - Determinism (frozen clock + seeded random)
  - Assertion DSL parser
- [ ] CLI: `wick workflow test <slug> --filter --integration --watch --coverage --record`
- [ ] MCP ops: full set per §9 (introspection + write + canvas + action + test)
- [ ] Datasets UI tab (table view, schema editor, query console)

Unit tests:
- [ ] `mcp_introspection_test.go` — `workflow_node_types`, `workflow_trigger_types`, `workflow_channels`, `workflow_connectors`, `workflow_skills`, `workflow_providers` return correct schemas
- [ ] `mcp_canvas_test.go` — `workflow_add_node`, `workflow_connect`, `workflow_update_node`, `workflow_delete_node`, `workflow_move_node` atomic mutate YAML
- [ ] `mcp_action_test.go` — `workflow_validate`, `workflow_simulate`, `workflow_test`, `workflow_run_now`, `workflow_request_review`
- [ ] `mcp_test_test.go` — `workflow_test` runs test files, returns per-case result
- [ ] `mockregistry_test.go` — Provider/Connector/Channel/HTTP/Dataset/Shell interception via service wrapping
- [ ] `mockregistry_test.go` — strict mode (no mock → fail), permissive mode (`--allow-unmocked`)
- [ ] `capturelog_test.go` — channel/connector outbound captured, assertions read
- [ ] `determinism_test.go` — frozen clock `2026-05-14T10:00:00Z`, seeded random
- [ ] `assertion_test.go` — DSL parser: equality, comparison, contains, matches, in, typeof
- [ ] `assertion_test.go` — assertions: case_fired, edge_traversed, layer_applied, mock_called, node_skipped, path_taken
- [ ] `cli_workflow_test_test.go` — filter, integration, watch, coverage, record flags

Integration test:
- [ ] `integration_ai_first_test.go` — end-to-end AI flow: MCP introspect → compose workflow + tests → workflow_test → fix → request_review
- [ ] `integration_canvas_test.go` — workflow build via canvas ops only, YAML produced equivalent to hand-write

**Phase 13 — Polish + observability** ✅ *(Bootstrap/HotReload/Cleanup/Cost done; fsnotify watcher loop pending)*

Implementation:
- [ ] fsnotify watcher di `<BaseDir>/workflows/` + `<BaseDir>/datasets/`
- [ ] Daily cleanup job (run retention per §4 5a)
- [ ] Cost tracking aggregation (per-node + workflow rollup)
- [ ] Audit log queryable (filter by workflow, user, op, date range)
- [ ] Bootstrap & hot-reload per §15
- [ ] Implicit-reply detection edge case handling

Unit tests:
- [ ] `watcher_test.go` — fsnotify trigger reload service, debounce rapid edits
- [ ] `cleanup_test.go` — retention: success 30d, failed 90d, keep_max enforcement
- [ ] `cost_test.go` — per-node cost capture (tokens, usd, ms), rollup workflow + daily
- [ ] `audit_query_test.go` — filter by workflow/user/op/date range
- [ ] `bootstrap_test.go` — startup register all workflows, hot-reload on update

Integration test:
- [ ] `integration_polish_test.go` — full lifecycle: create → edit → run → cleanup, all observability data correct

---

## TODO / Open decisions

Putusin sebelum nyentuh kode:

- [ ] **Visual editor library.** [Drawflow](https://github.com/jerosoler/Drawflow)
  (vanilla JS, ~28KB, CDN) cocok dengan wick stack (templ + vanilla JS).
  Alternatif: [Rete.js](https://retejs.org/) (lebih powerful tapi butuh
  bundler), [LiteGraph.js](https://github.com/jagenjo/litegraph.js)
  (game-dev oriented). Rekomendasi: **Drawflow** — simple, sesuai stack,
  drag-drop nodes + edges out-of-the-box.
- [ ] **Engine shape.** DAG (multi-parent, merge, parallel) vs tree
  (single-parent, sequential)? Rekomendasi: **DAG-capable engine**,
  tapi default workflow yang dibuat user/AI = tree (lebih simple buat
  dipahami). Engine ga enforce tree.
- [ ] **Node types minimum.** `classify, agent, channel, connector, shell, branch,
  end` cukup buat 80% kasus. `python, http, db_query, transform, parallel`
  tambah sesuai kebutuhan. Lihat §5.
- [ ] **Channel scope.** Slack dulu. WA + email reply nyusul.
- [ ] **Whitelist source-of-truth.** Per-trigger inline list, atau
  global config table? Lihat §18.
- [ ] **Dedup TTL.** 24 jam cukup? Kalau lebih = state file membengkak.
- [ ] **State persistence granularity.** Per-node state (resume mid-flow)
  atau cuma per-run (restart full kalau crash)? Lihat §13.
- [ ] **Cost tracking.** Track LLM cost per-node? Display di run detail?
- [ ] **Jawab §22 pertanyaan terbuka** sebelum coding.

---

## 1. Latar belakang

Wick punya tiga kelas modul:

- **Tools** — UI manual buat manusia.
- **Connectors** — kapabilitas terstruktur buat LLM lewat MCP.
- **Jobs** — RunFunc statis yang dijalanin scheduler cron tiap menit.

Yang kurang: admin (dan AI) bisa **bikin tugas otomatis multi-step tanpa
recompile**. Step bisa: tanya AI, klasifikasi, query DB, call HTTP,
exec shell, panggil skill, branching kondisional. Dipicu cron, pesan
Slack, webhook, atau klik manual. Plus AI lewat MCP bisa nge-design
workflow sendiri di canvas: drag node, sambungin edge, test, deploy.

Hari ini skenario itu mengharuskan: tulis Go module → register → build →
deploy. **Workflow** = entry baru sebagai folder di
`<BaseDir>/workflows/`, ngikutin pola preset/workspace yang sudah ada.

Konsepnya mirip n8n/Zapier (visual node-based automation) tapi
**AI-native**: node `classify` pakai LLM buat decision, AI bisa
edit canvas via MCP, AI guard review workflow sebelum publish, dan
runtime nyambungin natural ke channel registry + connector registry + agent pool.

### Differentiator vs n8n

| Aspect | n8n | Wick Workflow |
|---|---|---|
| Decision logic | If/Switch dgn expression | `branch` (if/switch) + `classify` (AI natural lang) — dua-duanya tersedia |
| AI integration | API SDK per node (OpenAI/Anthropic) — 1 node = 1 API call | CLI subprocess (`claude`/`codex`/`gemini`) — AI = agent dgn tool ecosystem built-in (Read/Edit/Bash/MCP) |
| Output reliability | `tool_use` schema enforcement di API level | Prompt-based JSON + parser + 5-layer fallback (lihat §5.1) |
| AI nodes | Add-on, generic LLM call | First-class, agent pool + session reuse, share state antar node |
| Editor | Wajib UI | UI **atau** YAML **atau** AI-via-MCP — semua valid |
| Storage | DB | File-based, gitops-friendly |
| External integration | Generic webhook + custom code per node | Reuse connector module (existing wick infra). Channel module = bidirectional via `type: channel` symmetric trigger+action. Skill = local agent capability inside `type: agent` |
| Self-built | Hosted/self-host k8s | Embedded in wick binary |
| Latency per AI node | ~200-1000ms (API direct) | ~500-2000ms (proc spawn overhead) — accepted trade-off |

Bukan kompetitor n8n. n8n = generic workflow engine dgn API-SDK approach;
wick = AI-agent orchestration dgn CLI subprocess approach. Overlap area =
"automation", tapi pendekatan AI fundamentally beda:

- **n8n** call OpenAI API per node, each independent. Cocok buat "LLM
  as one of many service integrations".
- **Wick** spawn agent CLI yang punya tool ecosystem inheriting dari
  agent session. Cocok buat "AI orchestration dgn skill + file
  manipulation + MCP tools".

Workflow ga ngejar fitur n8n generic (CRM connector, Sheets transform,
dst). Ngejar "LLM + skill + channel" pipeline yang n8n kurang ergonomis
karena AI di n8n cuma node biasa, sementara di wick AI adalah
orchestrator dgn tools.

---

## 2. Prinsip

0. **AI-first design — mandatory.** Workflow harus bisa dibuat efektif
   oleh AI (Claude Code, Cursor, Claude Desktop, ChatGPT custom GPT,
   Gemini Gem) dengan **cuma prompt user natural language**, tanpa user
   tulis YAML manual. Pola sama dengan tools/jobs/connectors yang sudah
   ada di wick — AI = primary author, UI = secondary review/tweak. Test
   AI-first dgn checklist berikut sebelum impl ship:

   - **Schema fully introspectable via MCP.** Setiap node type, trigger
     type, channel, connector op, skill, dataset, dan provider tersedia
     via MCP introspection op (`workflow_node_types`, `workflow_trigger_types`,
     `workflow_channels`, `workflow_connectors`, `workflow_skills`,
     `workflow_providers`). Tiap return JSON schema + description +
     example.
   - **Description load-bearing.** Tiap node type, op, trigger punya
     `description` yang AI baca verbatim (same discipline sebagai connector
     `Operation.Description` di [docs/guide/connector-module.md](../../docs/guide/connector-module.md)).
     Action verb + what it does + when to use. ✅ "Send Slack reply to
     {thread}. Returns posted message timestamp." ❌ "send slack".
   - **Naming consistency.** Predictable conventions — `type: channel`
     simetric trigger+action, `<channel>.<op>` skill-promoted naming,
     `{{.Event.X}}` / `{{.Node.X}}` / `{{.Env.X}}` template refs. AI
     ga perlu trial-error untuk guess naming.
   - **Validate + simulate + test sebelum deploy.** AI bisa iterate di
     `workflow_validate` (parse + cycle + schema), `workflow_simulate`
     (event sintetis, no side effect), `workflow_test` (fixtures). Error
     messages structured + actionable (path field, expected vs got).
   - **Scaffold templates.** `workflow_create(slug, template)` punya 4+
     starter (empty, support-triage, incident-response, daily-digest)
     yang AI extend, bukan generate from-scratch.
   - **Canvas ops as alternative to file edit.** AI di remote env (Claude
     Desktop, ChatGPT) ga punya file tool — pakai `workflow_add_node`,
     `workflow_connect`, etc. Same outcome (file di folder), beda channel.
   - **Composition over invention.** AI compose dari building blocks yang
     sudah ada (connector ops, channel actions, skills, dataset). Ga
     perlu mikir "gimana cara call GitHub API" — connector existing handle.
     Adding new integration = bikin connector module (existing pattern,
     well-documented).

   By design wick adalah AI-buildable workflow engine, bukan
   workflow-engine-with-AI-as-afterthought. Setiap design decision di doc
   ini di-evaluate melalui filter "apakah AI bisa compose ini lewat MCP +
   prompt?". Kalau jawaban "perlu human read docs first" → redesign.

1. **File-based, UI = primary editor.** Workflow = folder
   `<BaseDir>/workflows/<slug>/` dengan `workflow.yaml` (graph + triggers)
   + folder `nodes/` (per-node script/prompt). File adalah storage,
   canvas adalah surface utama. Hand-edit YAML tetep didukung (gitops,
   power-user). Atomic write via `tmp+rename`.
2. **Domain di [internal/agents/workflow/](../agents/workflow/).** Sejajar
   dengan `preset/`, `workspace/`, `gate/`, `session/`. Service-nya satu,
   dipake tiga caller: UI handler, MCP connector, runtime engine.
3. **DAG-capable engine, tree-shape default.** Engine support multi-parent
   + merge (wait-for-all) + parallel fan-out. Tapi workflow yang dibuat
   user/AI biasanya tree (single-parent, sequential branches). Engine
   ga enforce shape — admin bebas.
4. **Node-based, polymorphic via `type:` field.** Tipe node:
   `classify, agent, channel, connector, shell, python, http, db_query, transform,
   branch, parallel, merge, end`. Dispatcher di runtime per-type.
5. **Output reference: `{{.Node.<id>.<field>}}`.** Tiap node simpan
   output ke run context, downstream node baca via template.
6. **Trigger polymorphic — cron cuma salah satu.** Workflow punya
   `triggers: []Trigger` list. Tipe: `cron`, `channel`,
   `webhook`, `manual`, `schedule_at`. Satu workflow boleh multi-trigger.
7. **Per-workflow FIFO queue.** Concurrent fire di-queue per workflow.
   Worker pool ngedrain serial — gak ada race condition antar run dalam
   1 workflow. Cross-workflow tetep paralel.
8. **State file-based, resume-able.** Tiap run = folder
   `runs/<run-id>/` dengan `state.json` + `events.jsonl`. Crash di
   tengah → resume dari node terakhir sukses. Mirip session di
   [internal/agents/session/](../agents/session/) — file-based, ga
   butuh DB.
9. **Test mode wajib.** Tiap node bisa punya fixture di `__tests__/`.
   "Test Workflow" button jalankan dgn fixture, output kelihatan
   color-coded di canvas. AI/admin iterasi tanpa fire trigger asli.
10. **AI bisa edit canvas via MCP.** Canvas ops (`add_node`, `connect`,
    `update_node`, `delete_node`) di-expose ke MCP. AI bisa "buatkan
    workflow: trigger Slack `!support`, klasifikasi, kalau bug bikin
    Linear ticket, kalau pertanyaan jawab dgn skill qiscus-docs."
11. **AI guard sebelum publish.** Saat user/MCP commit workflow baru
    atau enable → optional AI reviewer baca semua node + edges + script
    + prompt, banding dgn rule. Block kalau melanggar, audit kalau
    override.
12. **Skill ≠ workflow.** Skill = local Claude Code skill bundle (atomic
    AI capability, provider-specific). Workflow = multi-node graph.
    Skill diakses cuma di dalam `type: agent` node lewat `skills: []`
    field — BUKAN standalone node type. Channel actions + connector ops
    punya node type sendiri (`type: channel`, `type: connector`).

---

## 3. Use cases (canonical examples)

Validasi desain dgn 5 contoh konkret + generic identifiers (abc.com,
example.com). Workflow yang ga muat ke salah satu pattern ini = scope
creep.

### Use case 1: Inbound inquiry triage (single trigger, branch routing)

Pattern paling umum — channel message masuk, klasifikasi AI, route ke
handler.

```
trigger: channel chat, event=message, target="#inbox", mention_bot=true
  ↓
classify (AI): "bug" / "question" / "feature" / "other"
  ├─ bug      → connector tracker.create_issue → channel reply "tracked: <url>"
  ├─ question → agent (skills: doc-search) → channel reply <answer>
  ├─ feature  → connector airtable.append_row → channel reply "noted"
  └─ other    → agent (bounce text) → channel reply
```

Tree-shape. Single trigger entry → 4 leaves.

### Use case 2: Multi-source incident response (parallel + merge)

Pattern fan-out → gabung → process. Cocok buat alert response.

```
trigger: webhook /hooks/alerts
  ↓
parallel (3 branches):
  ├─ connector grafana.fetch_dashboard
  ├─ shell run "df -h" on target host
  └─ connector log_store.query_errors (last 5min)
  ↓ merge (wait-for-all)
agent: "Analyze: metrics + disk + logs. Identify root cause."
  ↓
classify: "needs_human" / "self_heal"
  ├─ needs_human → connector pager.escalate → channel #oncall send_message
  └─ self_heal   → shell run runbook.sh → channel #oncall send_message (result)
```

DAG (parallel + merge). Edge-first model:
- Trigger entry → `parallel-fetch` node
- `parallel-fetch` fan-out via 3 edges to grafana/shell/db_query
- All 3 → `merge-results` node
- Linear chain to `analyze` → `classify-severity` → branch

### Use case 3: Daily metric digest (cron + parallel)

```
trigger: cron "0 8 * * *" timezone=UTC
  ↓
parallel (3 sources):
  ├─ connector github.list_recent_issues (24h)
  ├─ connector status_page.list_incidents (24h)
  └─ connector customer_db.query_active_count
  ↓ merge
agent: "Format digest markdown dari 3 sumber"
  ↓
channel chat.send_message #leadership <digest>
  └─ parallel-fan-out:
       → dataset_insert digest_archive (audit trail)
```

### Use case 4: Multi-stage interactive flow (3 trigger, 1 workflow)

Pattern stateful tanpa pause/resume — 3 trigger ke entry node terpisah
di workflow yang sama.

```
WORKFLOW ticket-flow:

triggers:
  1. event=message  → entry: post-button     (inquiry datang)
  2. event=action   → entry: fetch-thread    (user click button)
  3. event=submission → entry: create-ticket (user submit modal)

graph:
  STAGE 1 (run #1):
    post-button (connector chat.post_with_button)
      [run ends — wait next trigger via Slack interaction event]

  STAGE 2 (run #2, fired by action event):
    fetch-thread (connector chat.get_thread_messages)
      → summarize (agent skills=[validator])
      → show-modal (connector chat.open_modal)
      [run ends — wait submit]

  STAGE 3 (run #3, fired by submission event):
    create-ticket (connector helpdesk.create)
      → confirm (connector chat.send_message)
```

Context dipropagasi via Slack `metadata` di button/modal payload,
retrieved via `{{.Event.Payload.metadata.*}}` di stage berikutnya.
3 independent runs di JobRun history (traceable per stage).

### Use case 5: Nested classification (deep tree)

```
trigger: channel chat, event=message, target="#support"
  ↓
classify-1: "question" / "statement"
  └─ question →
       classify-2: "product-A" / "product-B" / "other"
       └─ product-A →
            classify-3: "how-to" / "bug-report"
            ├─ how-to     → agent skills=[product-A-docs] → reply
            └─ bug-report → connector tracker.create + reply
       ├─ product-B → agent skills=[product-B-docs] → reply
       └─ other     → end silent
  └─ statement → end silent
```

Pure tree, depth 3 classify. Bukti tree cukup buat 80% AI orchestration.

### Edge-first vs embed comparison (use case 1 in YAML)

```yaml
# EDGE-FIRST (n8n-style, what wick uses)
graph:
  entry: classify-intent
  nodes:
    - { id: classify-intent, type: classify, prompt: "..." }
    - { id: handle-bug,      type: connector, module: tracker, op: create_issue }
    - { id: handle-question, type: agent, skills: [doc-search] }
    - { id: handle-feature,  type: connector, module: airtable, op: append_row }
    - { id: handle-other,    type: agent, prompt: "friendly bounce" }
    - { id: reply,           type: channel, channel: chat, op: reply_thread }
  edges:
    - { from: classify-intent, case: bug,      to: handle-bug }
    - { from: classify-intent, case: question, to: handle-question }
    - { from: classify-intent, case: feature,  to: handle-feature }
    - { from: classify-intent, case: default,  to: handle-other }
    - { from: handle-bug,      to: reply }
    - { from: handle-question, to: reply }
    - { from: handle-feature,  to: reply }
    - { from: handle-other,    to: reply }
```

Add new handler = `workflow_add_node` + 1 `workflow_connect` MCP call.
Reroute = swap edge target. Atomic operations.

---

## 4. Layout folder + YAML schema

### Folder per workflow

```
<BaseDir>/workflows/<slug>/
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

Slug = `[a-z0-9-]+`, divalidasi sama kayak preset name.

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
  - id: trigger-manual              # second trigger in the same workflow
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
  `Router.defs[slug]`.
- Live triggers (cron, channel, webhook) keep firing the published
  copy registered in `Router.defs[slug]`. This separation lets the
  user iterate on a draft via Run Now without re-publishing every
  edit.

### Identitas + governance

- **`id`** — stable identitas. Rename folder atau ganti `name:` ga
  pengaruh. Approval nempel ke `id`. Folder slug cuma alias.
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
  event: message                    # event sub-type, default: message
                                    # future: reaction, mention, join, leave, ...
                                    # channel boleh declare event types via TriggerSpec
  target: "#support"                # channel-specific addressing (channel name/ID/JID/...)
  match:
    keywords: ["help", "bug"]       # case-insensitive substring, OR
    regex: "^!support\\b"           # optional
    mention_bot: true               # generic across channels yang support mention
    from_threads_only: false        # ignore parent msg, cuma reply
  whitelist:
    users: ["U123"]
    groups: ["@support-team"]
  dedup_ttl_sec: 86400              # default 24h
```

**Channel-specific match fields**. Channel boleh extend `match:` dgn
field unik (Slack `app_id`, Telegram `chat_type`, REST `header.*`).
Schema discoverable via `channel.TriggerSpec()` — UI form auto-render
dari schema.

**Event sub-types**. Channel boleh declare event types yang dia support
di `TriggerSpec.Events`. V1 = `message` saja (cover use case sekarang);
channel future bisa expose `reaction`, `mention`, `join`, `leave`,
`file_upload`, dst. AI/admin pilih event di UI dropdown atau YAML.

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
- id: trigger-manual
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
  source_workflow: "*"              # workflow slug atau pattern; "*" = semua
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
  trigger_workflow: "error-handler"   # workflow slug yg pasang trigger type: error
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
    - qiscus-docs-search
    - weekly-product-sync
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
    Name        string                 // "weekly-product-sync"
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
| `persistent` | Subprocess persist across **workflow runs** (session ID = workflow slug). Context inherit dari run sebelumnya | Long-running assistant pattern, learn-from-history |

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
- Manual cleanup: `wick workflow session kill <slug>` CLI / MCP op
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
- `root`: `workflow:<slug>:run:<run_id>:root`
- `persistent`: `workflow:<slug>:persistent`

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
    repo: "qiscus/inbox"
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
  headers:
    Authorization: "Bearer {{.Secret.GITHUB_PAT}}"
  query:
    since: "{{.Event.At | addHours -24}}"
  body: ""                          # raw atau {{.Node.x.json}}
  parse_response: json              # raw | json | bytes
  timeout_sec: 30
  retry:
    max: 3
    backoff_sec: 2
  # edge: { from: <this-id>, to: summarize }
```

Output: `.status`, `.headers`, `.body`, `.json` (kalau parse).

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
- `{{.Node.<id>}}` — full output object
- `{{.Node.<id>.<field>}}` — sub-field
- `{{.Node.<id>.<field> | <filter>}}` — Go template pipe filter
- `{{.Event.*}}` — trigger event
- `{{.Env.<NAME>}}` — workflow env value, from `env.yaml` (UI-managed, hand-edit OK) — lihat §11
- `{{.Secret.<NAME>}}` — encrypted secret, decrypt runtime. Schema declare `widget: secret` di workflow.yaml, value stored encrypted di `env.yaml` — lihat §11
- `{{.Workflow.<field>}}` — workflow metadata (Slug, ID, Version, Name)
- `{{.Run.<field>}}` — runtime metadata (ID, StartedAt)
- `{{.Dataset.<alias>}}` — dataset binding from `datasets:` field — lihat §12

---

## 6. Graph & engine

### Engine = DAG walker dengan state

Algoritma (edge-first):

```
1. Load workflow.yaml, parse nodes[] + edges[].
2. Validate: 
   - Pick entry from trigger.entry_node OR graph.entry.
   - All edge from/to references exist in nodes[].
   - case: field only on edges from classify/branch source.
   - case: default present for classify/branch (safety net).
   - No cycles (Kahn's topological sort).
3. Build adjacency: source_id → []{target_id, case?}.
4. Init state: current=[entry], completed=[], outputs={}.
5. Persist state to runs/<run-id>/state.json.
6. Loop:
   a. Pick next executable node (all upstream completed atau is_merge_wait_satisfied).
   b. If multiple ready → parallel: spawn N goroutines per branch.
   c. Dispatch by type → execute → capture output.
   d. Validate output_schema kalau declared.
   e. Persist output to nodes/<id>.json + append to events.jsonl.
   f. Resolve next nodes:
      - For classify/branch source: filter edges by case == verdict.
      - For other source: all outgoing edges fire (fan-out).
      - Merge node: wait until all inputs[] completed.
   g. If error: apply on_failure (halt/skip/fallback).
7. No more executable nodes → finalize state. Engine inject implicit
   channel reply node kalau trigger dari channel dan workflow ga punya
   explicit reply (lihat §16).
```

**Fan-out via edges (no `parallel` node needed for simple case):**

```yaml
nodes:
  - { id: A, type: connector, module: store, op: fetch }
  - { id: log, type: dataset_insert, dataset: audit }
  - { id: notify, type: channel, op: send_message }
edges:
  - { from: A, to: log }
  - { from: A, to: notify }   # A → 2 branches paralel
```

Engine deteksi: node A punya 2 outgoing edges, ga ada `case:` → fan-out
spawn 2 goroutines (`log` + `notify` paralel).

**`parallel` node masih useful** untuk explicit fan-out dgn named
branches + named outputs:

```yaml
nodes:
  - id: gather
    type: parallel
    branches: [grafana-fetch, db-fetch, shell-check]
edges:
  - { from: gather, to: grafana-fetch }    # explicit
  - { from: gather, to: db-fetch }
  - { from: gather, to: shell-check }
  - { from: grafana-fetch, to: merge }
  - { from: db-fetch,      to: merge }
  - { from: shell-check,   to: merge }
```

Output `gather.branches.grafana-fetch.<result>` dst — keyed access.

**`merge` node** explicit wait-for-all:

```yaml
nodes:
  - id: merge
    type: merge
    inputs: [grafana-fetch, db-fetch, shell-check]
    strategy: object         # object | array | first | last
```

Engine wait sampai semua `inputs[]` selesai sebelum execute merge node.

### Cycle detection

Static analysis at parse time. Kahn's algorithm — kalau topological sort
ga complete = cycle. Reject save dengan error "cycle detected at
nodes [A, B, C]".

### Parallel exec

`parallel` node spawn N goroutines, semua share parent context. Output
collected dgn map keyed by branch ID. Fail policy:
- `on_failure: halt` (default) — 1 branch fail = cancel sisanya, workflow halt
- `on_failure: skip` — continue, output branch yang fail = `{error: "..."}`
- `on_failure: fallback` — wait all, fallback node jalanin sub-graph

**Per-branch on_failure override** — `parallel` node punya `branches:`
list of node IDs. Tiap branch boleh punya `on_failure` sendiri di node
body — overrides parallel-level policy.

### Merge node readiness (DAG diamond topology)

Merge node `type: merge` punya `inputs: [<node_id>, ...]` — engine wait
sampai SEMUA listed nodes complete sebelum merge execute. Rules:

```
Merge node M with inputs [A, B, C] is "ready" when:
  - All nodes in inputs[] have status: completed | failed | skipped
  - No inflight upstream that hasn't yet reached any of [A, B, C]
  - For each input node, its on_failure outcome is final (not still
    retrying)

Merge fires regardless of which inputs succeeded/failed.
Output composition (`strategy:`):
  - object — { "A": output_A, "B": output_B, "C": output_C }
    Branch ID = input node ID. Failed nodes have output = {error: "..."}.
  - array  — [output_A, output_B, output_C] preserving inputs[] order
  - first  — output dari node yang complete duluan
  - last   — output dari node yang complete terakhir
```

**Merge fail policy** — same `on_failure: halt|skip|fallback` di merge
node body. Default `halt` kalau ANY input failed AND merge ga skip.

**Edge resolution untuk fan-in:**
```yaml
edges:
  - { from: A, to: merge-results }
  - { from: B, to: merge-results }
  - { from: C, to: merge-results }
```
Engine deteksi merge node (by type) → wait-for-all behavior. Non-merge
target dgn multiple incoming edges = engine error at validation (only
merge can fan-in).

### Edge resolution rule (classify/branch source)

Source node `type: classify` atau `type: branch` output verdict string.
Engine filter outgoing edges:

```
1. Find outgoing edges where edge.case == output.verdict
2. If found → fire those edges (1 or more, fan-out allowed even with case)
3. If not found → find edges where edge.case == "default"
4. If still not found → halt with error "verdict not in cases"
```

`case: default` WAJIB ada untuk classify/branch — validator reject save
kalau missing.

**Note:** untuk classify dgn `output_cases: [...]` declared, validator
also check that every value in `output_cases[]` has corresponding edge
(plus `default`) — else warn at save.

### Error propagation di parallel

Per-branch error handling — propagate via parallel/merge node policy:

```
parallel: [A, B, C]
  ├─ A succeeded
  ├─ B failed → apply B.on_failure
  └─ C succeeded
  
on_failure di parallel node body:
  - halt: signal cancellation ke A, C goroutines. Parallel node fail.
  - skip: continue dgn B output = {error: ...}. Parallel completes.
  - fallback: wait A, C done. Apply fallback node ID.

Then downstream merge (kalau ada) receives partial results sesuai
strategy.
```

### Engine struct sketch

```go
// internal/agents/workflow/engine.go
type Engine struct {
    layout  config.Layout
    pool    *pool.Pool             // agent
    skills  skill.Registry         // skill nodes
    db      *sql.DB                // db_query nodes
    audit   AuditWriter
}

type Run struct {
    ID         string                   // UUIDv7
    WorkflowID string                   // workflow.id
    Slug       string
    StartedAt  time.Time
    Event      Event                    // trigger event
    State      RunState
}

type RunState struct {
    Status      string                   // queued | running | paused | success | failed
    Current     string                   // current node ID
    Completed   []string                 // node IDs done
    Outputs     map[string]any           // node_id → output
    Error       *NodeError               // last error if any
    UpdatedAt   time.Time
}

func (e *Engine) Run(ctx context.Context, w Workflow, evt Event) (Run, error)
func (e *Engine) Resume(ctx context.Context, runID string) (Run, error)
func (e *Engine) Pause(runID string) error
func (e *Engine) Cancel(runID string) error
```

### State persistence

`runs/<run-id>/state.json` ditulis atomic (tmp+rename) setelah tiap
node selesai. Crash mid-node = next start baca state, current node
re-execute dari awal (idempotent assumption — node author's
responsibility).

`events.jsonl` append-only:
```
{"ts":"2026-05-14T08:00:01Z","event":"node_started","node":"classify-intent"}
{"ts":"2026-05-14T08:00:03Z","event":"node_completed","node":"classify-intent","output":{"verdict":"bug"}}
{"ts":"2026-05-14T08:00:03Z","event":"node_started","node":"create-ticket"}
{"ts":"2026-05-14T08:00:05Z","event":"node_failed","node":"create-ticket","error":"linear API 503"}
{"ts":"2026-05-14T08:00:05Z","event":"workflow_failed"}
```

Render to UI timeline. Resume reads events to find last `node_completed`,
state.current = next of that.

### Test mode

`__tests__/<node-id>.json`:
```json
{
  "input": {
    "Event": {"Type": "channel", "Text": "ada bug di chat widget"},
    "Node": {}
  },
  "expected_output": {
    "verdict": "bug"
  }
}
```

`workflow_test(slug)` MCP op (atau "Test" button UI) → engine jalankan
dgn fixture sebagai input, compare output ke `expected_output`. Mode:
- **Per-node**: run 1 node dgn fixture, check output.
- **Full flow**: run dari entry, kalau ada fixture per-node pakai itu,
  kalau ga ada panggil real (atau mock LLM dgn fixture).

Hasil: per-node ✓/✗ + diff kalau mismatch. Color-coded di canvas.

---

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
table sebagai `workflow:<slug>` dengan `Schedule` dari trigger cron
pertama. Multi-cron → multi job rows: `workflow:<slug>:cron-0`,
`:cron-1`, dst.

### Event path

Channel adapter di [internal/agents/channels/](../agents/channels/)
panggil `OnAnyMessage(evt)` → `triggerRouter.Dispatch(evt)`. Hook fire
SEBELUM session routing. Workflow match? Enqueue + skip session routing
kalau `consume: true`.

Webhook adapter = HTTP handler di `internal/pkg/api/` — mount
`/hooks/{slug}/{path}`, verify HMAC, parse body, build Event.

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
slug: error-handler
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

### Adding new channel

Bikin sub-package `internal/agents/channels/<name>/`, implement
`Channel` interface (`TriggerSpecs` + `Actions` + `Send`), register di
`setup.Compose`. Trigger router + action dispatcher otomatis pick up.
Workflow engine + UI ga butuh perubahan.

---

## 8. Domain package

```
internal/agents/workflow/
  workflow.go              # Workflow, Node, types
  nodes/
    classify.go            # classify node executor
    agent.go
    skill.go
    shell.go
    python.go
    http.go
    db_query.go
    transform.go
    branch.go
    parallel.go
    merge.go
  engine.go                # graph walker, state persister
  resolver.go              # template render, output ref resolution
  validator.go             # cycle detect, schema check
  trigger/
    router.go              # event matching, dedup, enqueue
    cron.go
    channel.go
    webhook.go
    manual.go
    schedule_at.go
  service.go               # CRUD on folder
  manager.go               # service + state + guard
  scaffold.go              # template per node type for MCP create
```

### Core types

```go
type NodeType string

const (
    NodeClassify    NodeType = "classify"
    NodeAgent       NodeType = "agent"        // skills accessed via skills:[] field
    NodeChannel     NodeType = "channel"      // symmetric: also a trigger type
    NodeConnector   NodeType = "connector"    // reuse internal/connectors/ ops
    NodeShell       NodeType = "shell"
    NodePython      NodeType = "python"
    NodeHTTP        NodeType = "http"
    NodeDBQuery     NodeType = "db_query"
    NodeTransform   NodeType = "transform"
    NodeBranch      NodeType = "branch"
    NodeParallel    NodeType = "parallel"
    NodeMerge       NodeType = "merge"
    NodeEnd         NodeType = "end"
    NodeDatasetGet     NodeType = "dataset_get"
    NodeDatasetExists  NodeType = "dataset_exists"
    NodeDatasetQuery   NodeType = "dataset_query"
    NodeDatasetInsert  NodeType = "dataset_insert"
    NodeDatasetUpsert  NodeType = "dataset_upsert"
    NodeDatasetDelete  NodeType = "dataset_delete"
    NodeDatasetCount   NodeType = "dataset_count"
)

type Workflow struct {
    ID             uuid.UUID
    Slug           string
    Version        int
    Name           string
    Description    string
    Enabled        bool
    MaxDurationSec int
    Triggers       []Trigger
    Queue          QueuePolicy
    Graph          Graph
    Env            map[string]string         // workflow-level env
    Secrets        map[string]string         // encrypted, decrypt runtime
    CreatedBy      string
    CreatedAt      time.Time
}

type Graph struct {
    Entry string            // default entry kalau trigger ga set entry_node
    Nodes []Node            // flat list, no embedded edges
    Edges []Edge            // separate edge list (n8n-style)
}

type Edge struct {
    From string             // source node ID
    To   string             // target node ID
    Case string             // optional: case label, only for classify/branch source
    Label string            // optional: display label di canvas (UI hint, no semantic)
}

type Node struct {
    ID          string
    Type        NodeType
    Label       string
    Description string                       // load-bearing untuk AI (§5)
    TimeoutSec  int
    Retry       *RetryPolicy
    OnFailure   string                       // halt | skip | fallback
    Fallback    string                       // node ID (kalau OnFailure=fallback)
    OutputSchema map[string]any              // JSON schema
    // NO Next/Cases here — di Graph.Edges

    // For parallel/merge node — declared per node
    Branches []string                        // parallel node: explicit branch list
    Inputs   []string                        // merge node: wait-for-all inputs
    Strategy string                          // merge strategy: object|array|first|last

    // type-specific fields, union-style
    Classify  *ClassifyNode
    Agent     *AgentNode
    Channel   *ChannelNode
    Connector *ConnectorNode
    Shell     *ShellNode
    Python    *PythonNode
    HTTP      *HTTPNode
    DBQuery   *DBQueryNode
    Transform *TransformNode
    Branch    *BranchNode
    Dataset   *DatasetNode                    // unified for dataset_get/exists/query/insert/upsert/delete/count
}

// Trigger ditambah entry_node (override Graph.Entry per-trigger)
type Trigger struct {
    Type      string                          // cron | channel | webhook | manual | schedule_at | error
    EntryNode string                          // override Graph.Entry kalau diset
    // ... type-specific fields per trigger type
}

type Service interface {
    List() ([]Workflow, error)
    Load(slug string) (Workflow, error)
    Create(w Workflow, files map[string][]byte) error
    Update(slug string, w Workflow, files map[string][]byte) error
    Delete(slug string) error
    Toggle(slug string, enabled bool) error
    Approve(slug, userID string, override *Override) error
}
```

---

## 9. MCP surface — complete API for any AI environment

Workflow editing dirancang biar **AI dari mana saja** bisa bikin/edit,
ga peduli punya file tool atau tidak. Tiga tier op + remote access
patterns.

### Akses environment matrix

| Env | File access | MCP transport | Pattern |
|---|---|---|---|
| Claude Code, Cursor (local CLI) | ✓ native | stdio (local) | File tool + thin MCP introspection |
| Claude Desktop | ✗ | stdio atau HTTP/SSE ke wick | **Full MCP ops** (tier 1 + 2 + 3) |
| ChatGPT (custom GPT, plugin) | ✗ | HTTP `/mcp` + bearer | Full MCP ops |
| Gemini Gems / custom action | ✗ | HTTP `/mcp` + bearer | Full MCP ops |
| Wick built-in UI assistant | ✓ (server-side proxy) | in-process | File tool internal |

Kunci: **tanpa file tool, MCP harus self-sufficient**. Semua yang AI
butuh — read state, write state, action, file CRUD — semua ada di MCP.

Wick HTTP MCP udah ada (lihat
[docs/guide/connector-module.md](../docs/guide/connector-module.md)
"`/mcp` endpoint, bearer token"). Tinggal register workflow ops ke
existing server.

### Tier 1 — introspection (read-only)

AI butuh tau apa yang ada sebelum edit.

| Op | Param | Hasil |
|---|---|---|
| `workflow_workspace` | — | `{base_dir, schema_ref, node_types[], trigger_types[], templates[]}` — entry point |
| `workflow_node_types` | — | `[{type, schema, example, when_to_use}]` |
| `workflow_trigger_types` | — | `[{type, schema, example}]` |
| `workflow_channels` | — | `[{name, configured, triggers[], actions[]}]` — channel registry (lihat §7) — used for both trigger + action node discovery |
| `workflow_connectors` | — | `[{module, rows: [], operations: []}]` — connector module rows + ops (existing `wick_list` discoverable via tool_id `conn:{id}/{op}`) |
| `workflow_skills` | provider? | `[{name, provider, description, input_schema, source}]` — per-provider skill catalog discovered via `Provider.ListSkills()`. Filter by provider param atau return semua kalau kosong. NOT channel actions / connector ops (lihat `workflow_channels` / `workflow_connectors`) |
| `workflow_providers` | — | `[{name, configured, capabilities, default_preset}]` — list providers (claude/codex/gemini) + their capabilities (structured_output support, etc.) |
| `workflow_list` | filter? | list semua workflow `[{slug, id, name, enabled, approved}]` |
| `workflow_get` | slug | full workflow definition `{id, name, triggers[], graph{...}, files[]}` — sumber kebenaran AI buat edit |
| `workflow_list_files` | slug | list isi folder `[{path, size, modified}]` — buat AI tau ada file apa |
| `workflow_read_file` | slug, path | content file (prompt.md, script.sh, dst) — replace `Read` tool buat AI tanpa file access |

### Tier 2 — write (state-mutating)

Edit workflow. AI bisa pilih: write file langsung (kalau ada file tool)
atau canvas ops (deklaratif).

**File ops (replace native file tool buat remote AI):**

| Op | Param | Hasil |
|---|---|---|
| `workflow_create` | slug, template? | scaffold folder lengkap (id, default workflow.yaml, README); return `{slug, path, files, id}` |
| `workflow_write_file` | slug, path, content | atomic write ke `<base>/<slug>/<path>` — sanitize (no `..`, no symlink, no escape folder) |
| `workflow_delete_file` | slug, path | hapus file dalam folder workflow |
| `workflow_delete` | slug | hapus full workflow folder + unregister scheduler |

**Canvas ops (deklaratif, lebih ringkas dari nulis YAML):**

| Op | Param | Hasil |
|---|---|---|
| `workflow_add_node` | slug, node | add node to graph, validate; return updated YAML |
| `workflow_update_node` | slug, id, patch | merge patch ke node fields |
| `workflow_delete_node` | slug, id | remove node + edges yang refer ke dia |
| `workflow_connect` | slug, from_id, to_id, case? | add edge; case = key kalau dari classify/branch |
| `workflow_disconnect` | slug, from_id, to_id | remove edge |
| `workflow_move_node` | slug, id, x, y | canvas position hint |
| `workflow_set_triggers` | slug, triggers[] | replace triggers list |
| `workflow_toggle` | slug, enabled | enable/disable |

Canvas position disimpan di `workflow.yaml` field optional `_canvas:`:
```yaml
_canvas:
  positions:
    classify-intent: {x: 120, y: 200}
    handle-bug: {x: 380, y: 100}
```

YAML engine ignore `_canvas`; UI baca buat render.

### Tier 3 — action (validate, simulate, test, run, approve)

| Op | Param | Hasil |
|---|---|---|
| `workflow_validate` | slug | parse + cycle + schema + guard dry-run; return `{ok, errors[], warnings[]}` |
| `workflow_simulate` | slug, event | run dgn event sintetis, ga persist, ga notify. Return per-node output + final result |
| `workflow_test` | slug | run dengan `__tests__/` fixtures, compare ke expected |
| `workflow_run_now` | slug, event? | trigger run beneran (manual trigger pattern), return run_id |
| `workflow_get_runs` | slug, limit | list runs dgn event + status + cost |
| `workflow_get_run` | slug, run_id | full run state + events.jsonl + node outputs |
| `workflow_request_review` | slug, message | notify admin di UI; workflow stay `enabled=false` |
| `workflow_capture_fixture` | slug, run_id, node_id | snapshot run sebagai `__tests__/<node>.json` |

### Pattern per environment

**Local AI dgn file tool (Claude Code, Cursor):**
```
1. workflow_workspace()          ← tau lokasi + schema
2. workflow_create(slug, template) ← scaffold
3. Edit workflow.yaml via Write/Edit native
4. Edit nodes/*.md, script.sh native
5. workflow_validate(slug)       ← check
6. workflow_simulate(slug, evt)  ← dry-run
7. workflow_request_review(slug) ← admin approve
```

**Remote AI tanpa file tool (Claude Desktop, ChatGPT, Gemini):**
```
1. workflow_workspace()          ← entry
2. workflow_node_types()         ← discover apa yg bisa dipake
3. workflow_create(slug)         ← scaffold (lewat MCP)
4. workflow_add_node(slug, ...)  ← bangun graph step by step
5. workflow_connect(slug, ...)   ← sambungin edge
6. workflow_write_file(slug, "nodes/prompt.md", content)
                                 ← isi prompt panjang via MCP
7. workflow_validate(slug)
8. workflow_simulate(slug, evt)
9. workflow_request_review(slug)
```

Dua flow output sama — file di folder yang sama, approval flow sama.
Diferensiator cuma channel komunikasi: native file tool vs MCP write
op.

### HTTP MCP transport — setup buat remote AI

Wick MCP server udah ada di `/mcp` (lihat existing connector-module
docs). Buat workflow ops:

```
POST https://wick.your-host.com/mcp
Authorization: Bearer <token>
Content-Type: application/json

{
  "jsonrpc": "2.0",
  "method": "tools/call",
  "params": {
    "name": "workflow_add_node",
    "arguments": {
      "slug": "support-triage",
      "node": {"id": "classify-intent", "type": "classify", ...}
    }
  },
  "id": 1
}
```

**Setup AI client:**

- **Claude Desktop** — edit `claude_desktop_config.json`:
  ```json
  {
    "mcpServers": {
      "wick": {
        "url": "https://wick.your-host.com/mcp",
        "headers": {"Authorization": "Bearer wick_token_..."}
      }
    }
  }
  ```
- **ChatGPT (custom GPT)** — Action dgn OpenAPI spec yang reference
  `/mcp` endpoint. Bearer token di Action authentication.
- **Gemini Gems** — Function calling dgn HTTP action ke `/mcp`.
- **Wick UI assistant** — in-process MCP client, ga butuh auth (already
  authenticated session).

### Auth + permission

Token scope (per token):
- **Workflow allowlist** — bisa edit workflow apa aja? `["*"]` atau
  list slug.
- **Op allowlist** — read-only? write-only? full? Default sesuai role.
- **Approval cap** — bisa langsung enable atau wajib request_review?
  Token AI default = ga bisa enable, wajib `request_review`.

Audit log catat tiap MCP call: token ID, user yang issue token, op,
arguments hash, result, timestamp.

### Limit MCP tanpa file tool

Beberapa hal yang lebih ribet di remote-AI mode:

- **Long file edit** — AI ga punya Edit/PartialEdit, harus full-replace
  via `workflow_write_file`. Engine handle diff via tmp+rename atomic.
- **Browse files** — `workflow_list_files` cuma list path; isi besar
  harus `workflow_read_file` per-file. AI biasa cope, tapi lebih round
  trip.
- **Search** — ga ada Grep equivalent saat ini. Tambah `workflow_grep`
  kalau use-case sering muncul (future).

Trade-off: AI tanpa file tool sedikit lebih ribet tapi tetap full-capable.
Workflow logic ga compromise.

### Template per starter

`workflow_create(slug, template)` scaffold:

- `template: empty` — folder kosong + workflow.yaml minimal (1 trigger
  manual + 1 node end).
- `template: support-triage` — Use case 1 di §3.
- `template: incident-response` — Use case 2.
- `template: daily-digest` — Use case 3.

User pilih template di UI Create. AI lewat MCP pake `template: empty`
+ langsung edit, atau pake pre-built starter.

### Contoh AI flow

User: *"AI, buatin workflow: trigger `!support` di Slack, klasifikasi
bug/question/feature, bug ke Linear, question ke skill qiscus-docs."*

```
AI → MCP: workflow_workspace()
       ← {base_dir, node_types, trigger_types, ...}

AI → MCP: workflow_node_types()
       ← [classify, agent, channel, connector, shell, branch, ...]

AI → MCP: workflow_create(slug="support-triage", template="empty")
       ← {slug, path, id, files: [workflow.yaml, README.md]}

AI → Edit workflow.yaml (or use workflow_add_node + workflow_connect)
       set triggers, add 4 nodes:
         classify-intent (cases: bug/question/feature/other)
         handle-bug (skill: create-linear-ticket)
         handle-question (skill: qiscus-docs-search)
         handle-feature (skill: log-airtable)

AI → MCP: workflow_validate("support-triage")
       ← {ok: true, warnings: []}

AI → MCP: workflow_simulate("support-triage", {
            Type: "channel",
            Text: "chat widget error di production"
          })
       ← {final_result: "ticket created LINEAR-123",
          node_outputs: {
            classify-intent: {verdict: "bug"},
            handle-bug: {ticket_id: "LINEAR-123", url: "..."}
          },
          path: ["classify-intent", "handle-bug"]}

AI → MCP: workflow_request_review("support-triage",
            "Workflow triage #support: klasifikasi LLM + route ke skill.")
       ← {url: "https://wick.local/tools/agents/workflows/support-triage"}

AI ke user: "Done, di-simulate dengan sample 'chat widget error' —
            terdeteksi sebagai bug, akan bikin tiket Linear. Review +
            approve di <url>."
```

---

## 10. UI — canvas editor

Tab baru `Workflows` di [internal/tools/agents/](../tools/agents/),
sejajar Sessions/Workspaces/Presets/Providers.

### Files

```
internal/tools/agents/
  workflows.go              # handlers
  view/
    workflows_list_templ.go
    workflows_editor_templ.go
    workflows_runs_templ.go
  static/
    workflow-canvas.js      # Drawflow integration
    workflow-canvas.css
```

### Routes

```go
r.GET("/workflows", listPage)
r.GET("/workflows/{slug}", detailPage)
r.GET("/workflows/{slug}/edit", editorPage)
r.POST("/workflows", create)
r.POST("/workflows/{slug}", update)
r.POST("/workflows/{slug}/toggle", toggle)
r.POST("/workflows/{slug}/approve", approve)
r.POST("/workflows/{slug}/run", runNow)
r.POST("/workflows/{slug}/test", runTest)
r.DELETE("/workflows/{slug}", delete)

// Canvas API (UI + MCP canvas ops backend)
r.POST("/workflows/{slug}/nodes", addNode)
r.PATCH("/workflows/{slug}/nodes/{id}", updateNode)
r.DELETE("/workflows/{slug}/nodes/{id}", deleteNode)
r.POST("/workflows/{slug}/edges", connect)
r.DELETE("/workflows/{slug}/edges/{from}/{to}", disconnect)

// File explorer
r.GET("/workflows/{slug}/files", listFiles)
r.GET("/workflows/{slug}/files/{path...}", readFile)
r.PUT("/workflows/{slug}/files/{path...}", writeFile)

// Run history + replay
r.GET("/workflows/{slug}/runs", listRuns)
r.GET("/workflows/{slug}/runs/{id}", runDetail)
r.POST("/workflows/{slug}/runs/{id}/replay", replay)
r.POST("/workflows/{slug}/runs/{id}/resume", resume)
```

### List page

Tabel: Name, Triggers (badges), Nodes count, Last Run, Status. Filter
`unapproved` di atas. Tombol `+ New Workflow` → modal pilih template
(empty / support-triage / incident-response / daily-digest).

### Editor page — 3-pane layout

```
┌─────────────────────────────────────────────────────────────────┐
│  Header: name | type | enabled toggle | Save | Test | Approve   │
├──────────┬──────────────────────────────────┬───────────────────┤
│          │                                  │                   │
│  Node    │   Canvas (Drawflow)              │  Inspector        │
│  palette │   - drag-drop nodes              │  (selected node)  │
│          │   - draw edges between nodes     │  - id, label      │
│  classify│   - click node → inspector       │  - type-spec      │
│  agent   │   - delete edge / node           │    fields         │
│  skill   │                                  │  - schema-driven  │
│  shell   │   [trigger]                      │    form           │
│  ...     │        ↓                         │                   │
│          │   [classify]                     │  Output ref       │
│  --      │     ├─bug→ [skill:create-ticket] │  available:       │
│          │     └─...                        │  {{.Event.Payload.text}}  │
│ Triggers │                                  │  {{.Node.x.y}}    │
│  + cron  │                                  │                   │
│  + ...   │                                  │  [test fixture]   │
│          │                                  │                   │
├──────────┴──────────────────────────────────┴───────────────────┤
│  Bottom: YAML preview (read-only) | Files | Runs | Logs         │
└─────────────────────────────────────────────────────────────────┘
```

**Node palette** (left): drag node type to canvas. Categories:
- AI: classify, agent
- Action: skill, shell, python, http, db_query
- Logic: branch, parallel, merge, transform, end

**Canvas** (center): Drawflow instance. Edge labels show case names
(bug/question/...) for classify/branch. Right-click node → menu.
Double-click → open prompt/script in editor modal.

**Inspector** (right): schema-driven form. Untuk classify: prompt
textarea, `output_cases` chip list (engine derive edge case labels).
Untuk connector: module + op dropdown (autocomplete dari registry),
args form auto-render dari `Operation.Input` struct. Per-type schema →
templ partial server-rendered. **Edges editor terpisah** — separate
panel di canvas, list of `{from, to, case?}` edit-able.

**Bottom panel** (collapsible tabs):
- **YAML preview** — read-only mirror, real-time render dari canvas
- **Files** — file explorer per workflow folder
- **Runs** — recent runs, click → timeline view
- **Logs** — live log dari run yang sedang jalan

### YAML mode toggle

Switch dari canvas ke YAML editor full screen. Power user friendly. Save
parse + cycle check + re-render canvas. Round-trip lossless (canvas
positions di `_canvas:` field).

### Test panel

Tombol "Test" → modal:
- Pilih trigger (pretend event input): `channel` dgn text apa,
  `cron` (tick now), `webhook` dgn payload JSON, atau pakai fixture
  dari `__tests__/`.
- Run engine in test mode (no notify, no real skill side-effects —
  skills run dgn mock kalau punya `mock` field).
- Canvas show animation: node hijau saat completed, merah kalau fail,
  abu skip. Edge yang dilewati di-highlight.
- Per-node output panel di bawah: input/output JSON, duration, cost.

### Run timeline view

```
[10:00:01] ▶ Started (trigger: channel #support)
[10:00:01]   ├─ classify-intent
[10:00:03]   │    ├─ input: "ada bug di widget"
[10:00:03]   │    ├─ output: {verdict: "bug"}
[10:00:03]   │    └─ duration: 2.1s, tokens: 245, cost: $0.0008
[10:00:03]   └─ handle-bug
[10:00:05]        ├─ skill: create-linear-ticket
[10:00:05]        ├─ output: {ticket_id: "LINEAR-123"}
[10:00:05]        └─ duration: 2.0s
[10:00:05] ✓ Completed (4.1s, $0.0008)
```

Plus inline canvas mini-map sebelah kanan, dengan node yang dilewati
di-highlight.

### Hand-edit ↔ UI consistency

File ditulis admin via editor luar tetep dikenali UI saat refresh.
`Service.List()` baca disk tiap call, fsnotify watcher push update via
SSE (Server-Sent Events) ke browser yang lagi buka editor.

### Approval banner

`enabled=false` dan ada `shell`/`python` node + `approved=false` → list
page tampilin banner kuning: "1 workflow pending approval — created by
AI via MCP". Klik → editor dgn tombol Approve di header.

---

## 11. Environment & secrets — workflow config

Workflow butuh config: Slack channel target, GitHub PAT, max retry,
toggle feature, dst. **Schema** declared di `workflow.yaml` (developer
contract, version-controlled). **Values** di file terpisah
`<slug>/env.yaml` (UI-managed, secrets encrypted).

**Reuse vocabulary `wick:"..."` config-tag yang sudah ada** di
[docs/reference/config-tags.md](../../docs/reference/config-tags.md).
Same widget + modifier names, same form renderer, same UI behavior.
Beda cuma: untuk Go module schema di struct tag, untuk workflow schema
di YAML — keduanya consume rendering pipeline yang sama.

### Schema di `workflow.yaml`

```yaml
env:
  - name: SLACK_CHANNEL
    widget: text                    # widget dari config-tags vocab
    desc: "Where to post notifications"
    default: "#support"

  - name: GITHUB_PAT
    widget: secret                  # encrypted; UI shows ••• when set
    desc: "GitHub PAT for issue creation"
    required: true

  - name: MAX_DAILY_RUNS
    widget: number                  # auto-applied untuk int/float field
    desc: "Daily fire cap"
    default: 100

  - name: ESCALATION_MODE
    widget: dropdown
    options: [pager, slack, email]
    desc: "Where to escalate on failure"
    default: slack

  - name: GUARD_PROMPT_EXTRA
    widget: textarea
    desc: "Custom rules buat AI guard"

  - name: ENABLE_AUTO_TRIAGE
    widget: checkbox                # auto-applied untuk bool field
    desc: "Allow LLM to triage without admin approval"
    default: true

  - name: ALLOWED_SLACK_CHANNELS    # multi-row table (kvlist)
    widget: kvlist
    columns: [id, name]
    desc: "Channel allowlist"

  - name: ALLOWED_USERS             # searchable typeahead from channel
    widget: picker
    source: slack.users             # LookupProvider key
    desc: "Allowed users"
    visible_when: ENABLE_AUTO_TRIAGE:true   # hide kalau auto-triage off

  - name: GITHUB_WEBHOOK_URL
    widget: url
    desc: "Endpoint for GitHub webhook callbacks"

  - name: NOTIFY_EMAIL
    widget: email
    desc: "Where to email failure alerts"
```

### Widget vocabulary (mirror config-tags.md)

| Widget | YAML literal | UI form |
|---|---|---|
| `text` (default) | string | single-line input |
| `textarea` | string | multi-line textarea |
| `secret` | string (encrypted on disk) | password input, "Reveal" button |
| `number` | int/float | number input |
| `checkbox` | bool | toggle |
| `dropdown` | string | select dropdown (needs `options:`) |
| `email` | string | HTML `type="email"` |
| `url` | string | HTML `type="url"` |
| `color` | string `#aabbcc` | color picker |
| `date` | string ISO date | date picker |
| `datetime` | string ISO 8601 | datetime-local picker |
| `kvlist` | JSON array of objects | editable inline table (needs `columns:`) |
| `picker` | JSON array `[{id,name}]` | searchable typeahead chips (needs `source:` registered di [`LookupProvider`](../agents/channels/slack/lookup.go)) |

Field type derivable: int/float field → widget auto = `number`. bool
field → widget auto = `checkbox`. Override pakai `widget:` explicit.

### Modifiers (mirror config-tags.md)

| Modifier | Effect | YAML key |
|---|---|---|
| Help text | shown below field | `desc:` |
| Default value | seed kalau ga di-set | `default:` |
| Required | block save kalau kosong, `c.Missing()` flag | `required: true` |
| Read-only | set once at boot | `locked: true` |
| Regenerate button | UI button regen (need registered generator) | `regen: true` |
| Hide from form | seeded di DB, akses via `c.Cfg()`, ga muncul di UI form | `hidden: true` |
| Conditional visibility | tampil cuma kalau field lain == value | `visible_when: <field>:<value>` |

### Key derivation

Field `name:` auto snake-case ke env key (sama config-tags rule):

| YAML name | env key | reference |
|---|---|---|
| `SLACK_CHANNEL` | `slack_channel` | `{{.Env.SLACK_CHANNEL}}` (case-insensitive) |
| `GitHubPAT` | `git_hub_pat` | `{{.Secret.GITHUB_PAT}}` |
| `APIBaseURL` | `api_base_url` | `{{.Env.APIBaseURL}}` |

Atau eksplisit: `key: legacy_api_key` override default.

### Values di `<slug>/env.yaml`

```yaml
# env.yaml — UI-managed, hand-edit OK
# Schema authoritative dari workflow.yaml. Field di luar schema = warning.
SLACK_CHANNEL: "#support-prod"
GITHUB_PAT: wick_enc_aGVsbG8gd29ybGQ=    # encrypted, kelihatan di UI saat Reveal
MAX_DAILY_RUNS: 500
ESCALATION_MODE: pager
GUARD_PROMPT_EXTRA: |
  Reject workflow yang notify ke channel #leadership tanpa approval explicit.
ENABLE_AUTO_TRIAGE: true
ALLOWED_SLACK_CHANNELS:                  # kvlist value = JSON array
  - { id: "C123", name: "#support" }
  - { id: "C456", name: "#support-prod" }
ALLOWED_USERS:                           # picker value = same shape as kvlist=id|name
  - { id: "U100", name: "Yoga" }
GITHUB_WEBHOOK_URL: "https://hooks.example.com/gh"
NOTIFY_EMAIL: "alerts@abc.com"
```

Storage format identical dengan `configs.value` column di config-tags
pattern — JSON array of `{key: value}` objects buat kvlist/picker,
plain literal buat lainnya. Same parser bisa read di Go.

### Reference dari node

```yaml
- type: channel
  channel: slack
  op: send_dm
  args:
    channel: "{{.Env.SLACK_CHANNEL}}"     # plain field
    text: "..."

- type: http
  method: POST
  url: https://api.github.com/...
  headers:
    Authorization: "Bearer {{.Secret.GITHUB_PAT}}"  # secret, auto-decrypt
```

`{{.Env.<NAME>}}` untuk semua field non-secret; `{{.Secret.<NAME>}}`
hanya untuk `widget: secret`. Engine reject mixing — secret ga bisa
di-render via `.Env.` (prevent accidental log leak).

### UI form

Tab "Settings" di workflow editor — **reuse existing config-tags
form renderer**. Same widget components, same auto-save behavior
(800ms debounce after last keystroke), same secret reveal/regen
buttons, same kvlist Tab-add-row behavior, same picker debounced
lookup.

Save handler:
- `widget: secret` value → encrypt via existing `wick_enc_` helper
  before write
- `widget: kvlist` / `picker` → JSON serialize before write
- Required field kosong → block save, show row error
- `visible_when` field tetep seeded (just hidden from form)

Validation runtime saat workflow load:
- Schema diff (field added/removed di workflow.yaml) → migration prompt
- `required` field kosong di env.yaml → workflow ga jalan, surface
  "Missing config" di UI workflow list (badge merah)

### Hand-edit ↔ UI consistency

UI list page baca `env.yaml` saat render. fsnotify push update via SSE
buat editor yang lagi buka. Hand-edit nulis ciphertext langsung
(`wick_enc_...`) tetep valid — UI ga overwrite saat reveal/save kalau
value ga berubah.

### MCP ops

```
workflow_get_env_schema(slug)
  → [{name, type, default, description, required}]

workflow_get_env_values(slug, reveal_secrets=false)
  → {SLACK_CHANNEL: "#support", GITHUB_PAT: "wick_enc_..."}
    reveal_secrets=true → require admin token

workflow_set_env_values(slug, values)
  → atomic write env.yaml, secret auto-encrypt
```

AI bisa edit env values lewat MCP — secret encrypt server-side, AI ga
pernah lihat plaintext.

### Secret rotation

UI tombol "Rotate" per secret field — generate new placeholder, mark
old as deprecated. Workflow runs read latest. Old runs di history tetap
audit-able dgn timestamp (ga decrypt-able lagi setelah rotation).

### Default workflows (zero-config)

Workflow tanpa `env:` field = ga butuh config. UI Settings tab kosong
(atau tampilin "No config required for this workflow"). Build workflow
sederhana ga dipaksa setup env.

---

## 12. Datasets — cross-workflow data store

Beberapa state perlu hidup di luar single run:
- **Dedup**: "udah pernah handle event_id X?" — query existence
- **State machine**: tickets pending escalation, users opted-out
- **Cache**: hasil enrichment API dgn TTL (avoid re-fetch)
- **Audit beyond JobRun**: records arbitrary (mis. "tickets dibikin
  workflow X bulan Mei")

Wick punya **Datasets** sebagai first-class concept — user-defined data
tables, accessible dari workflow lewat dataset node types. Sejajar
dengan Workflow + Preset + Channel.

### Storage split — schema in file, data in wick DB

```
<BaseDir>/datasets/<slug>/
  dataset.yaml         # current schema (active)
  history/             # version snapshots
    v1.yaml
    v2.yaml
    v3.yaml
```

**Schema = file, data = DB.** Schema versioning via file snapshots
(gitops-friendly), data via shared Postgres table. No SQLite, no
per-dataset table.

**Schema version tracking — file-based:**

- `dataset.yaml` punya `version: <N>` field — matches latest history file.
- Setiap schema edit → bump `version`, snapshot YAML lama ke
  `history/v<N>.yaml`, write new schema ke `dataset.yaml`. Atomic.
- Audit "siapa ganti apa kapan" = `git log datasets/<slug>/dataset.yaml`
  (gratis dari version control).
- Diff antar version = `git diff` atau UI "Compare v2 vs v3".
- Rollback = swap `dataset.yaml` dengan `history/v<N>.yaml`, bump version
  → new history entry (`history/v<N+1>.yaml`).

**Why file-based versioning (bukan DB):**
- Schema change jarang — overhead audit table ga sebanding.
- Git already provides version control gratis.
- PR review schema change = sama dengan review code.
- Backup = folder + pg_dump.
- AI lokal (Claude Code) baca/edit pakai Read/Write native.
- AI remote (Claude Desktop, ChatGPT) butuh `dataset_read_file` +
  `dataset_write_file` MCP ops (sama pattern dgn workflow file).

**Data migration** (kalau schema change butuh sentuh existing JSONB rows
— rename JSONB key, strip key, type transformation) ≠ schema migration:
- Schema migration = update file + restart validator.
- Data migration = one-shot Job (reuse existing JobRun runner) yang
  loop rows + batch UPDATE JSONB. Progress visible di JobRun history.
- UI tombol "Apply data migration" muncul kalau schema change butuh
  sentuh rows; user trigger explicit.

---

**Data hidup di wick's Postgres** (same DB as configs/jobs/sessions),
NOT per-dataset sqlite file dan NOT per-dataset table. Reasoning:
- Wick udah pake Postgres + GORM (lihat [internal/pkg/postgres/migrate.go](../pkg/postgres/migrate.go))
- Single connection pool, no per-dataset file management
- Atomic transactions, proper indexes, JSONB native
- Multi-instance deploy aman
- Backup pattern sama dgn rest of wick (pg_dump)

**Semua dataset share satu Postgres table** `wick_datasets_rows`:

```sql
CREATE TABLE wick_datasets_rows (
  dataset_slug   TEXT     NOT NULL,
  pk             TEXT     NOT NULL,       -- primary key value (string-coerced)
  data           JSONB    NOT NULL,       -- semua kolom user, validated app-layer
  created_at     TIMESTAMPTZ DEFAULT now(),
  updated_at     TIMESTAMPTZ DEFAULT now(),
  PRIMARY KEY (dataset_slug, pk)
);

-- Indexes per kebutuhan dataset (partial, scoped per dataset):
CREATE INDEX idx_events_status ON wick_datasets_rows ((data->>'status'))
  WHERE dataset_slug = 'events';
CREATE INDEX idx_events_received ON wick_datasets_rows ((data->>'received_at'))
  WHERE dataset_slug = 'events';
```

**Why shared single table, bukan per-dataset table:**
- **No DDL per dataset** — schema change tinggal update `dataset.yaml`,
  validator app-layer cek shape pas insert/upsert. Migration ringan.
- **No table proliferation** — 100 dataset = 1 table, bukan 100 tables.
  Admin/backup tool tetep ringkas.
- **Composite PK** `(dataset_slug, pk)` jadi natural index buat hot
  path `dataset_get`/`dataset_exists`.
- **JSONB native ops** — Postgres support `data->>'col'`, `data @>` for
  containment, GIN index untuk arbitrary JSONB path.
- **Tradeoff:** column type CHECK constraint ga native (validate
  app-layer). Type-strict B-tree index = bikin partial functional
  index per kebutuhan (contoh di atas).

**Workflow ga akses table langsung lewat `db_query`** — pakai
`dataset_*` node types saja. Separation of concerns:
- `db_query` = external user-configured DB
- `dataset_*` = wick-managed table di internal DB, ada access control,
  ada schema validation, ada UI

`dataset.yaml` di folder = sumber kebenaran schema (gitops). Rows di
`wick_datasets_rows` = data. Drift handling:
- `dataset.yaml` missing, rows exist (with that dataset_slug) → orphan,
  prompt user adopt (reverse-engineer schema dari sample rows) atau
  drop rows.
- `dataset.yaml` exist, no rows yet → normal, dataset baru, akan terisi
  via insert/upsert.
- Schema diff (column added/renamed/removed) → engine re-validate
  existing rows kalau strict, atau accept gradual migration. UI prompt
  konfirmasi sebelum lock schema.

### `dataset.yaml`

```yaml
id: 0193e2b4-...
slug: processed_events
name: "Processed channel events"
description: "Dedup table for support triage workflows"

columns:
  - name: event_id
    type: string
    primary_key: true
  - name: workflow_slug
    type: string
    indexed: true
  - name: handled_at
    type: timestamp
    indexed: true
  - name: result
    type: json
  - name: trigger_source
    type: string
    default: "unknown"

indexes:
  - [workflow_slug, handled_at]      # composite index

retention:
  ttl_days: 90                       # auto cleanup
  by_column: handled_at              # which timestamp column drives TTL

access:
  workflows:                         # which workflows can write
    - support-triage
    - support-followup
  read_only_workflows:               # read but not write
    - audit-monthly
  ui_editable: true                  # allow row edit di UI
  mcp_writable: true                 # allow MCP edit via dataset_insert

created_by: yoga@abc.com
created_at: 2026-05-14T08:00:00Z
```

### Column types

Karena semua row tinggal di JSONB `data` column, "column type" =
**app-layer validation rule**, bukan native Postgres type. Engine
validate shape sebelum insert/upsert; kalau mismatch, reject dgn
error jelas.

| Type | Validation | JSONB shape | Index strategy |
|---|---|---|---|
| `string` | string, optional max_len | `"abc"` | partial `((data->>'col'))` |
| `int` | integer, optional min/max | `42` | partial `(((data->>'col')::int))` |
| `float` | number | `3.14` | partial `(((data->>'col')::float))` |
| `bool` | true/false | `true` | partial `(((data->>'col')::bool))` |
| `timestamp` | ISO 8601 | `"2026-05-14T08:00:00Z"` | partial `(((data->>'col')::timestamptz))` |
| `json` | any object/array | nested | GIN `(data->'col')` |
| `enum` | string in `options:` list | `"received"` | partial `((data->>'col'))` |

Primary key columns wajib explicit di schema — engine extract value
to `pk` column for fast lookup. Indexes wajib declare di `indexes:`
section atau `indexed: true` per column — engine bikin partial
functional index pas dataset create/migrate. Query yang hit
non-indexed JSONB path = full scan, audit log warn (ga block).

### Access via expose function only — no raw SQL

Dataset di wick DB tapi **bukan** untuk di-query bebas via `db_query`.
Akses cuma lewat:

1. **Node types** di workflow: `dataset_query`, `dataset_insert`,
   `dataset_upsert`, `dataset_delete`, `dataset_count`.
2. **MCP ops** untuk AI: `dataset_query`, `dataset_insert`, dst.
3. **UI** di Datasets tab — schema-aware form/table view.
4. **Internal Service interface** (Go) buat engine:

```go
// internal/agents/dataset/service.go
type Service interface {
    List() ([]Dataset, error)
    Get(slug string) (Dataset, error)
    Create(d Dataset) error              // create table via DDL
    UpdateSchema(slug string, patch SchemaPatch) error  // migrate

    Query(slug string, q Query) (QueryResult, error)
    Insert(slug string, row map[string]any) (any, error)
    Upsert(slug string, key []string, row map[string]any) (UpsertResult, error)
    Delete(slug string, where Where) (int64, error)
    Count(slug string, where Where) (int64, error)
}

type Query struct {
    Where     Where               // structured, parameterized
    Returning []string            // column projection
    OrderBy   []OrderClause
    Limit     int
    Offset    int
}
```

Service translate ke parameterized SQL. Kontrak: no string concat, no
raw SQL passthrough, no `OR 1=1` injection bisa lewat.

### Why ga expose raw SQL?

- **Safety**: workflow author bisa AI, ga trust raw SQL ke prod DB.
- **Schema enforcement**: query divalidasi terhadap `dataset.yaml`
  column types.
- **Access control**: per-dataset `access.workflows` allowlist
  enforceable di Service layer. Raw SQL bypass-able.
- **Index hint**: Service tau index mana yang relevan, bisa optimize.
- **Audit**: tiap query log-able dgn structured args (Where, etc).
- **Future backends**: kalau pindah ke Mongo/Redis suatu saat, Service
  abstraction hide impl.

Power user yang butuh raw SQL ad-hoc: pakai UI Query Console (audit-able)
atau `db_query` node ke external read-replica DB.

### Workflow binding

Workflow yang pake dataset declare di `datasets:` field:

```yaml
# workflow.yaml
datasets:
  - name: events                     # alias
    ref: processed_events             # dataset slug
    mode: read_write                  # read | read_write
```

Reference di node via alias `events`, ga slug langsung — biar bisa
rename dataset tanpa break workflow:

```yaml
- type: dataset_query
  dataset: events                    # alias
  where: {event_id: "{{.Event.EventID}}"}
```

### CRUD operations (node types — lihat §5)

- `dataset_query` — SELECT dgn where clause + returning + limit
- `dataset_insert` — INSERT row, fail kalau pk conflict
- `dataset_upsert` — INSERT atau UPDATE based on pk
- `dataset_delete` — DELETE rows matching where
- `dataset_count` — count rows (optional, bisa lewat query)

Engine paksa parameterized — value dari `{{.Event.X}}` di-bind, ga
di-string-concat (defense SQL injection).

### UI di tools/agents

Tab "Datasets" sejajar Workflows. Files:
- `internal/tools/agents/datasets.go` — handlers
- `view/datasets_list_templ.go`, `datasets_detail_templ.go`

Routes:
```go
r.GET("/datasets", listPage)
r.GET("/datasets/{slug}", detailPage)
r.POST("/datasets", create)
r.PATCH("/datasets/{slug}/schema", updateSchema)
r.GET("/datasets/{slug}/rows", queryRows)         // paginated table view
r.POST("/datasets/{slug}/rows", insertRow)        // manual insert
r.PATCH("/datasets/{slug}/rows/{pk}", updateRow)
r.DELETE("/datasets/{slug}/rows/{pk}", deleteRow)
r.POST("/datasets/{slug}/query", customQuery)     // ad-hoc query
```

**List page** — table-of-tables, columns: name, row count, size, last
modified, used-by-workflows.

**Detail page** — 3 panels:
1. **Schema editor** — column list, add/remove/rename. Schema change
   prompts migration confirmation modal. Old version saved di `history/`.
2. **Rows view** — paginated table, sortable, filterable per column.
   Inline edit (kalau `ui_editable: true`). Bulk delete + export CSV/JSON.
3. **Query console** — text input untuk where clause / select, execute,
   show result. Pattern mirip phpMyAdmin tapi schema-aware.

### MCP ops

```
dataset_list()                    → list datasets
dataset_get(slug)                 → schema + manifest
dataset_create(slug, schema)      → create new
dataset_update_schema(slug, patch) → migrate schema, log to history/
dataset_query(slug, where, ...)   → rows
dataset_insert(slug, row)         → insert
dataset_upsert(slug, row)         → upsert
dataset_delete(slug, where)       → delete
dataset_drop(slug)                → drop entire dataset
```

AI bisa bikin dataset on-the-fly. Use case: "Buatkan workflow yang
dedup `!support` events, simpan ke dataset baru."

```
AI → dataset_create("support-events-dedup", schema={...})
AI → workflow_create("support-triage", template="empty")
AI → workflow_set_datasets("support-triage", [{name: "events", ref: "support-events-dedup"}])
AI → workflow_add_node(... dataset_query event_id check ...)
AI → workflow_add_node(... dataset_insert mark handled ...)
```

### Schema migration

Schema version = snapshot file di `history/v<N>.yaml`. Format = full
`dataset.yaml` snapshot dgn metadata:

```yaml
# history/v3.yaml — example snapshot setelah add column
id: 0193e2b4-...
slug: processed_events
version: 3
columns:
  - { name: event_id, type: string, primary_key: true }
  - { name: status, type: enum, options: [received, processing, done, failed] }
  - { name: priority, type: enum, options: [low, medium, high], default: medium }  # NEW di v3
  - { name: handled_at, type: timestamp, indexed: true }

# Metadata about this version change (footer):
_version_meta:
  changed_from: 2
  changed_by: yoga@abc.com
  changed_at: 2026-05-14T08:00:00Z
  reason: "Added priority for incident workflow routing"
  data_migration:
    required: false               # add column = no backfill, default applies
    # OR for rename:
    # required: true
    # ops: [{op: rename_jsonb_key, from: "name", to: "event_name"}]
    # status: pending | running | done | failed
    # job_run_id: <run-uuid>      # link ke JobRun yg eksekusi
```

**Schema migration flow:**
1. User edit `dataset.yaml` via UI atau hand-edit.
2. Engine validate diff vs current `history/v<N>.yaml`.
3. Bump `version: N+1`.
4. Snapshot baru → `history/v<N+1>.yaml` (full schema + `_version_meta`).
5. New schema active di `dataset.yaml`.
6. Kalau diff butuh data migration → engine create JobRun, link ID di
   `data_migration.job_run_id`.

**Schema operations + data migration needs:**

| Op | Schema change | Data migration required? |
|---|---|---|
| Add column | append validator rule | ✗ (existing rows default to null/default) |
| Drop column (soft) | hide from schema | ✗ (key tetep di JSONB) |
| Drop column (hard) | remove + strip JSONB | ✓ — batch UPDATE strip key |
| Rename column | update validator | ✓ — batch UPDATE rename JSONB key |
| Change type | new validation rule | maybe — kalau lossy (string → int), butuh transform |
| Add index | partial functional index | ✗ (DDL only, CONCURRENTLY) |
| Drop index | DROP INDEX | ✗ |

**Data migration sebagai Job:**

Kalau migration butuh sentuh JSONB rows existing:
- Engine create JobRun dgn `Module: "dataset-migration"`, `Args: {dataset_slug, ops, target_version}`.
- Job loop rows dgn `WHERE dataset_slug = '<slug>'`, apply ops, batch
  UPDATE. Progress visible di JobRun history page.
- Sukses → `data_migration.status = done`. Fail → `failed`, schema
  version tetep aktif tapi data inconsistent — user retry button.
- Engine reject `dataset_insert`/`dataset_upsert` saat migration
  running kalau ops mutate fields yang di-touch (avoid race).

**Rollback:**
- Swap `dataset.yaml` dengan `history/v<N>.yaml` (old version).
- Bump version → new entry di `history/v<M+1>.yaml` dgn
  `reason: "rollback to v<N>"`.
- Kalau old schema butuh different data shape → trigger reverse data
  migration Job.

### Retention + cleanup

Daily job (reuse `connector-runs-purge` pattern) prune rows yang melebihi
`ttl_days`. Cleanup ke audit log. Per-dataset bisa override jadwal.

### Sharing antar workflow — norm, bukan exception

Share dataset across workflow = use case utama (dedup events, cross-workflow
state machine, shared lookup). **Safety bukan dari no-share, tapi dari
explicit contract**:

```yaml
slug: events
strictness: strict                # ← safety layer 1: schema = contract

columns:
  - { name: id, type: string, primary_key: true, required: true }
  - { name: source, type: enum, options: [slack, pagerduty, calendar] }
  - { name: status, type: enum, options: [received, processing, done] }
  - { name: handled_at, type: timestamp }

access:
  workflows:                      # ← safety layer 2: explicit allowlist
    - webhook-handler
    - calendar-poller
    - slack-monitor
  read_only_workflows:
    - audit-monthly
  row_filter: none                # ← safety layer 3: 'by_creator' kalau perlu isolation
```

```yaml
# workflow.yaml — binding dgn version pin
datasets:
  - name: events
    ref: events
    mode: read_write
    expected_version: 1           # ← safety layer 4: break loud kalau schema drift
```

**4 safety layers buat shared dataset:**

1. **`strictness: strict`** (default) — semua field declared di
   `dataset.yaml`. Typo'd key → reject. Validator enforce shape =
   contract antara workflow.
2. **`access.workflows` explicit allowlist** — workflow ga di-list ga
   bisa write. Default tanpa entry = no access. Add new workflow share
   = tambah ke list (eksplisit informed decision).
3. **`row_filter: by_creator`** (opt-in) — kalau workflow ga boleh
   sentuh row workflow lain. Engine stamp `_meta.created_by_workflow`,
   reject update/delete row dari workflow lain. Default `none` (full
   share, pattern paling umum).
4. **`expected_version` di workflow binding** — workflow declare versi
   yang dia expect. Schema bump v3→v4 → workflow ga update break dgn
   error jelas (bukan diam2 jalan dgn shape lama).

**Concurrent write semantics:**
- Postgres row-level lock — concurrent insert ke pk berbeda OK
- Upsert dgn pk conflict serialized otomatis
- UPDATE/DELETE dgn WHERE filter pakai SELECT FOR UPDATE kalau perlu
  strict ordering
- Read concurrent ke same dataset paralel, no lock

**Strictness modes:**

| Mode | Validator behavior | Use case |
|---|---|---|
| `strict` (default) | Reject insert kalau ada extra key di luar `columns`. Workflow harus declare semua fields | Production, critical data, multi-workflow share |
| `lax` | Accept extra keys, simpan ke JSONB. Warn di UI saat read kalau key tak dikenal. Query by extra key = full scan (audit warn) | Dev iteration, exploration, ad-hoc data |
| `extensible` | Per-workflow boleh tambah column. Schema = union. Engine track `_meta.added_by_workflow` per column | Federation pattern, plug-in workflows |

**Kapan separate dataset > shared dataset:**
- Data domain fundamental beda (walau nama mirip) — `events` di
  support-workflow ≠ `events` di analytics-workflow
- Privacy isolation — workflow A ga boleh tau row workflow B ada
  (pakai dataset terpisah bukan `row_filter`; row count masih leak)
- Different retention — workflow A 30d, workflow B 1y. Beda lifecycle

Default: share kalau data sama, separate kalau data beda. Schema
contract = safety mechanism.

### Adoption + import flows

**Adoption (data exists, no dataset.yaml yet):**

```
Rows exist di wick_datasets_rows (dataset_slug='events')
tapi <BaseDir>/datasets/events/ ga ada folder/yaml.

→ Engine detect orphan rows pas boot atau startup audit
→ UI list page show "Orphan dataset" badge dgn row count
→ User klik → adoption modal:
  ├─ [Adopt] → engine sample N rows, infer schema dari JSONB keys,
  │            generate dataset.yaml v1 draft. User review + edit
  │            (strictness, access.workflows, types, defaults).
  │            Save → dataset enabled.
  └─ [Drop] → DELETE FROM wick_datasets_rows WHERE dataset_slug=...
              (require typed confirmation kalau row count > 0)
```

**Export bundle:**

```bash
wick dataset export events --output events.tar.gz
```

Bundle = `dataset.yaml` + `history/v*.yaml` + `data.jsonl` (rows
ordered by pk). Portable, gitops-friendly.

**Import:**

```bash
wick dataset import events.tar.gz \
  --on-conflict abort               # abort | overwrite | skip | merge
```

Engine flow:
1. Extract bundle, parse `dataset.yaml`
2. Check target wick instance:
   - **Ga ada existing dataset dgn slug yg sama** → straight import:
     - Copy `dataset.yaml` + `history/` ke folder
     - Batch INSERT rows dari `data.jsonl`
     - Done
   - **Existing dataset, schema hash sama** → append mode:
     - Skip duplicate pk (default) atau overwrite per `--on-conflict`
   - **Existing dataset, schema diff** → prompt:
     - `[abort]` (default) — stop, user manual resolve
     - `[overwrite local]` — DROP local rows, replace dgn import
     - `[skip import]` — keep local, abort import
     - `[merge]` — engine compute schema union (kalau compatible
       dgn validator), migrate local + import (dry-run first)
3. Migration history merged (timestamp-ordered).
4. Conflict di pk per row: per `--on-conflict` policy.

**MCP ops:**

```
dataset_export(slug)                  → bundle URL (download) atau bytes
dataset_import(bundle, on_conflict)   → {merged_rows, skipped, errors[]}
dataset_infer_schema(slug)            → schema from existing rows (adoption helper)
```

### Migration safety

Setiap schema change yg sentuh existing rows = formal flow:

1. **User edit `dataset.yaml`** (UI form atau hand-edit).
2. **Engine compute diff** vs current active version.
3. **UI Preview panel:**
   - Schema diff color-coded (added cols green, dropped red, type
     change yellow)
   - Data impact estimate: "1,247 rows will be touched, ops: rename
     JSONB key 'name' → 'event_name', strip key 'legacy_id'"
   - Estimated duration based on row count + ops complexity
4. **[Dry-run] button** → engine run validation pass di-copy / in-memory
   shadow:
   - Validate each row against new schema
   - Report rows that ga fit (mis. enum value out of new options)
   - **No data touched**
5. **[Apply] button** (typed confirmation kalau destructive — "type DROP to confirm"):
   - Migration job spawn (reuse JobRun runner)
   - Snapshot ke `history/v<N+1>.yaml` first (rollback point)
   - Atomic per-batch (1000 rows default) dalam transaction
   - Throttle 100ms between batches (avoid table lock contention)
   - Progress visible di JobRun page + dataset detail page
   - Pause/resume support — state persist di JobRun
6. **Job complete**:
   - Schema active di `dataset.yaml`, version bumped
   - History snapshot final
   - Audit log entry: who applied, what changed, rows affected, duration
7. **Rollback** sampai job committed final:
   - Partial fail mid-batch → rollback last batch transaction, mark
     migration `failed` di JobRun
   - User retry atau revert via swap `dataset.yaml` ↔ `history/v<N>.yaml`

**Idempotent migration ops:**
- Engine stamp `_meta.migrated_to: v<N>` per row after touch
- Re-run job skip rows yg sudah `migrated_to == target_version`
- Crash mid-job → restart aman, lanjut dari row yg belum ke-stamp

**Critical dataset extra safety** (opt-in di `dataset.yaml`):

```yaml
critical_safety:
  append_only: false             # true = block UPDATE/DELETE
  require_review: false          # true = schema change butuh 2-person approval
  backup_before_migration: false # true = dump rows ke external storage sebelum data migration
```

Cocok untuk dataset compliance/financial/audit. Default off (most
workflow ga butuh).

### Schema mismatch — runtime behavior

Workflow A schema expects `priority: enum[low|medium|high]`, rows
existing punya `priority: 1|2|3`:

- **Read** (`dataset_query`, `dataset_get`) — engine return rows as-is,
  surface validation warning di run logs ("row pk=X has invalid priority
  '1', not in enum [low,medium,high]")
- **Write** (`dataset_insert`, `dataset_upsert`) — strict mode reject
  insert, lax mode accept + warn

Workflow author handle dgn:
- `transform` node antara query dan use — normalize old format
- Schema migration job — batch UPDATE existing rows ke new format
- Atau pakai `expected_version` pinning supaya workflow refuse jalan
  sampai dataset diadaptasi

### Differentiator vs DB query

| | `db_query` | `dataset_*` |
|---|---|---|
| Schema source | external system | wick `dataset.yaml` |
| Storage | user-configured DSN | wick's Postgres (same DB) |
| Discovery | manual (user tau struktur) | MCP `dataset_list` + UI |
| UI | none (just node config) | full table view + query console |
| Migration | user's responsibility | wick-managed dgn history log |
| Access control | DB-level | YAML `access:` field |
| TTL cleanup | external | built-in |

Use `db_query` kalau data live di external system kamu. Use `dataset_*`
kalau data baru lahir dari workflow operation.

---

## 13. State persistence + resume

### Lifecycle state file

```
runs/
  index/                       # sharded run summary index
    2026-05-15-01.jsonl        # one row per run, max ~100 rows per shard
    2026-05-15-02.jsonl
  <run-id>/                    # runID = plain UUID, listing not chronological
    state.json                 # current snapshot
    events.jsonl               # append-only log
    nodes/
      <id>.json                # output cache per node (large objects)
```

### Sharded run index (Runs panel pagination)

The Runs panel in the editor needs "newest 100 runs" answers in
constant time even after 100k+ historical runs accumulate. Scanning
`runs/<id>/state.json` for every entry doesn't scale.

Solution: append a one-line summary to a sharded JSONL file every
time a run completes. Each shard is bounded at ~100 rows so any
"page N" query reads exactly one ~10KB file regardless of total
history size.

**Shard naming:** `YYYY-MM-DD-NN.jsonl`. Per-day grouping with a
seq suffix when the day's first shard fills up. Alphabetical
descending listing = chronological newest-first natural — no need
to read state.json to sort.

**Row shape** (kept lean so 100 rows = ~10KB):
```json
{"id":"<uuid>","status":"success","at":"2026-05-15T17:32:22Z","end":"2026-05-15T17:32:33Z","ms":11000}
```

**Generic module:** [`internal/shardedlog`](../shardedlog/) is a
reusable `Store[T any]` (generics-based). Workflow runs use it via
[`state.IndexAppend`](../agents/workflow/state/index.go) +
`IndexList(slug, page, pageSize)`. Future features (agent sessions,
log mirrors, …) can reuse the same store.

**Migration:** old runs (saved before the index existed) won't be
in any shard — the Runs panel only shows indexed entries. They
remain accessible via direct URL `/runs/<id>`. Acceptable trade-off
for the scale win.

### `state.json` schema

```json
{
  "id": "01938...",
  "workflow_id": "0193e2b4-...",
  "workflow_slug": "support-triage",
  "workflow_version": 3,
  "trigger": {
    "type": "channel",
    "event": {"text": "...", "user": "U123", ...}
  },
  "status": "running",
  "started_at": "2026-05-14T10:00:01Z",
  "updated_at": "2026-05-14T10:00:03Z",
  "current": "handle-bug",
  "completed": ["classify-intent"],
  "outputs": {
    "classify-intent": {"verdict": "bug"}
  },
  "error": null,
  "cost_usd": 0.0008,
  "tokens": 245
}
```

### Write pattern

Atomic write (`tmp+rename`) setelah tiap node selesai. Pre-execution
write update `current=<node>`, `status=running`. Post-execution write
move ke `completed[]`, `outputs[id]=...`. events.jsonl append per
sub-step.

### Structured event logs (zerolog mirror)

Every engine event hits zerolog with stable correlation fields so
operators can reconstruct any run from the wick log file without
touching `runs/`:

```json
{
  "level": "info",
  "component": "wf",
  "wf_slug": "support-triage",
  "wf_run_id": "<uuid>",
  "request_id": "<from HTTP middleware>",
  "wf_node": "agent",
  "wf_event": "node_completed",
  "data": {
    "latency_ms": 12843,
    "output": { ... },              // truncated at 4KB
    "verdict": "..."
  },
  "time": "2026-05-15T17:32:22+07:00",
  "message": "workflow event"
}
```

Grep targets:
- `wf_run_id=<id>` — every event for one specific run
- `request_id=<id>` — HTTP request → engine run correlation
- `wf_slug=<slug>` — every event for one workflow

Engine carries `request_id` across the queue boundary via
`Event.Payload["request_id"]` because the queue-worker goroutine's
own ctx is the server's bootstrap ctx, not the originating HTTP
request — see `Engine.Run` in
[`engine.go`](../agents/workflow/engine/engine.go).

### Resume

Worker startup atau crash recovery:
1. Scan `<BaseDir>/workflows/*/runs/*/state.json`.
2. Filter `status=running`.
3. Per row: hitung "stale" (now - updated_at > 2*max_duration_sec) →
   mark failed dgn reason "stuck".
4. Sisanya: replay dari `state.current`. Re-execute current node
   (assumes idempotent — author's responsibility).

UI tombol "Resume" di run detail buat run yang `status=paused` (manual
pause atau infrastructure-level pause).

### Skip persistence

Workflow ringan (cron tick yang cepet, ga butuh resume) bisa flag
`persistent: false` di YAML → engine ga tulis state per-node, cuma
final result. Trade-off: ga bisa resume, tapi lebih cepet + ga ngotorin
disk.

### Cleanup

Workflow-level config `run_retention_days: 30`. Daily cleanup job
(reuse `connector-runs-purge` pattern) hapus folder `runs/*` yang
lebih lama. Per-workflow override possible.

---

## 14. Test framework — unit + integration

AI-first principle (§2 #0) mandate: workflow yang dibuat AI dari prompt
**harus testable** sebelum deploy. Test framework punya 2 layer: **unit
test** (single node dgn mock) + **integration test** (full flow dgn
scripted events + mock external).

### Folder layout

```
__tests__/
  nodes/                          # unit tests — 1 file per node
    classify-intent.test.yaml
    handle-bug.test.yaml
    summarize.test.yaml
  integration/                    # integration tests — full flow
    bug-flow.test.yaml
    feature-flow.test.yaml
    multi-stage-interactive.test.yaml
  fixtures/                       # reusable input data
    sample-events.yaml
    sample-thread-messages.yaml
  mocks/                          # reusable mock responses
    classifier-responses.yaml
    tracker-responses.yaml
```

### Unit test format (per-node, mocked)

```yaml
# __tests__/nodes/classify-intent.test.yaml
node: classify-intent             # node ID di workflow.yaml

cases:
  - name: "bug pattern → verdict bug"
    input:
      Event: { Type: channel, Text: "production error di order checkout" }
      Node: {}                    # upstream outputs (kosong = node pertama)
    mocks:                        # provider response mocked
      provider_response:
        verdict: bug
        confidence: 0.92
        reasoning: "mentions production error"
    expect:
      output.verdict: bug
      output.confidence: ">= 0.5"           # comparison ops: ==, !=, >=, <=, contains, matches
      assertions:
        - { type: case_fired, value: bug }  # asserts edge case selected

  - name: "low confidence → default"
    input:
      Event: { Type: channel, Text: "lorem ipsum" }
    mocks:
      provider_response: { verdict: "unclear", confidence: 0.3 }
    expect:
      output.verdict: "default"             # confidence_threshold defends
      runtime_warnings: ["below confidence threshold"]

  - name: "fuzzy match kicks in"
    input:
      Event: { Type: channel, Text: "got error in widget" }
    mocks:
      provider_response: { verdict: "bug_report", confidence: 0.85 }
    expect:
      output.verdict: bug                   # fuzzy_match: bug_report → bug
      assertions:
        - { type: layer_applied, layer: fuzzy_match }
```

### Integration test format (full flow)

```yaml
# __tests__/integration/bug-flow.test.yaml
name: "bug inquiry → ticket + reply"

trigger:                          # synthetic event yg fire workflow
  type: channel
  event:
    Type: channel
    Text: "production error di order checkout"
    Channel: C123
    Thread: 1234567.890
    User: U999

mocks:                            # mock setiap external call
  nodes:
    - node: classify-intent
      response: { verdict: bug, confidence: 0.9 }
  connectors:
    - module: tracker
      op: create_issue
      args_match: { title: contains "order checkout" }
      response: { number: 42, url: "https://tracker.example.com/issues/42" }
  channels:
    - channel: chat
      op: reply_thread
      capture: true               # capture call, don't actually send
  http:
    - url_pattern: "*.example.com/*"
      method: GET
      response: { status: 200, body: { ok: true } }
  datasets:
    - dataset: handled
      ops: [exists]
      response: { found: false }   # first time → process

assertions:
  - path_taken: [classify-intent, handle-bug, reply-thread]
  - node: handle-bug
    args:
      title: "production error di order checkout"
  - node: reply-thread
    args.text: contains "issues/42"
  - final_status: success
  - duration_ms: < 2000
  - cost_usd: < 0.01
  - mocks_called: [classify-intent.provider, tracker.create_issue, chat.reply_thread]
```

### Mock layer — what gets intercepted

| Type | Real call | Mocked when test mode |
|---|---|---|
| Provider (`classify`, `agent` nodes) | CLI subprocess `claude`/`codex`/`gemini` | Engine bypass, return scripted `provider_response` |
| Connector (`type: connector`) | `Operation.Execute(ctx)` HTTP/DB | Engine bypass, return scripted response from `mocks.connectors[]` |
| Channel (`type: channel`) | `Channel.Send(action, args)` | Capture call (record args), return scripted response |
| HTTP node (`type: http`) | `http.Do(req)` | Match URL pattern + method, return scripted response |
| DB query (`type: db_query`) | `db.Query(...)` | Return scripted rows |
| Dataset (`type: dataset_*`) | Postgres `wick_datasets_rows` | In-memory test DB (default) atau scripted rows kalau explicit mock |
| Shell (`type: shell`) | `exec.Cmd` | Skip exec, return scripted stdout/exit_code |
| Implicit reply-to-source | `Channel.Send` synthetic | Captured like channel mock |

### Mock interception contract (engine impl spec)

**Test mode toggle:**
- Engine spawn dgn `EngineMode = ModeTest` saat called via `workflow_test`,
  `workflow_simulate`, atau CLI `wick workflow test`.
- Normal runs (cron/channel/webhook fire) selalu `ModeProduction`.
- Mode disimpan di `EngineContext` (propagate via ctx.Value), bukan
  per-node flag.

**Interception via Service interface wrapping:**

```go
// internal/agents/workflow/test/mock.go
type MockRegistry struct {
    Provider   map[string]ProviderMock     // node ID → mock response
    Connector  map[string]ConnectorMock    // "module.op" → mock + args match
    Channel    map[string]ChannelMock      // "channel.op" → capture + response
    HTTP       []HTTPMock                  // url pattern + method match
    Dataset    DatasetMockMode             // inmemory | scripted
    Shell      map[string]ShellMock        // node ID → stdout/exit
}

type EngineContext struct {
    Mode    EngineMode                     // ModeProduction | ModeTest
    Mocks   *MockRegistry                  // nil kalau Production
    Captures *CaptureLog                   // record outbound calls for assertion
}
```

**Service wrapping:** engine resolve service via `EngineContext.Mocks`:

```go
func (e *Engine) resolveProvider(ctx EngineContext, nodeID string) Provider {
    if ctx.Mode == ModeTest {
        if mock, ok := ctx.Mocks.Provider[nodeID]; ok {
            return &MockProvider{response: mock}
        }
        return &MockProvider{response: defaultMock}  // fallback: error or default
    }
    return e.providerRegistry.Get(...)             // real provider
}

// Sama pattern untuk Connector, Channel, HTTP, Dataset, Shell.
```

**Mock fallback policy** (no mock declared untuk a node):
- **strict mode** (default): test fail dgn "no mock for node X"
- **permissive mode** (`--allow-unmocked`): use real call (kalau ada
  credentials), atau return zero value (kalau external)

**Capture log** untuk channel/connector outbound:
```go
type CaptureLog struct {
    Channels  []ChannelCapture     // {channel, op, args, ts}
    Connectors []ConnectorCapture
}
// Tersedia di test result, assertion `node: X, args: {...}` baca dari sini
```

**Layer 2-5 di 6-layer reliability TETAP RUN saat mocked provider:**
- Mock provider return raw `provider_response` (schema-valid JSON).
- Engine apply normalize (layer 2), exact match (layer 3), fuzzy_match
  (layer 4), retry (layer 5), confidence threshold (layer 6) ke mock
  output. Sama path kayak real provider.
- This way test verify layer behavior end-to-end with deterministic
  inputs.

**Determinism guarantee:**
- All clock-dependent ops (`{{.Run.StartedAt}}`, `{{.Event.At}}`) use
  fixed time `2026-05-14T10:00:00Z` (configurable per test).
- UUIDs frozen sequence (`test-uuid-1`, `test-uuid-2`, ...).
- Random sampling seeded.
- Result: same test → same output every run, snapshottable.

### Assertion vocab

```yaml
expect:
  output.<field>: <value>              # equality
  output.<field>: ">= <number>"        # numeric comparison
  output.<field>: contains "<text>"    # substring
  output.<field>: matches "/regex/"    # regex
  output.<field>: in [a, b, c]         # set membership
  output.<field>: typeof string        # type check

assertions:
  - { type: case_fired, value: <case> }            # classify/branch picked this case
  - { type: edge_traversed, from: A, to: B }       # specific edge used
  - { type: layer_applied, layer: fuzzy_match }    # 6-layer reliability
  - { type: mock_called, target: <node>.<op> }     # mock was invoked
  - { type: node_skipped, node: <id> }             # on_failure: skip path

path_taken: [<node-id>, ...]                       # exact ordered path through graph
final_status: success | failed
duration_ms: <comparison>
cost_usd: <comparison>
```

### Running tests

```bash
wick workflow test <slug>                          # all tests in __tests__/
wick workflow test <slug> --filter node:classify   # unit tests filtered
wick workflow test <slug> --integration            # only integration/
wick workflow test <slug> --watch                  # rerun on file change
wick workflow test <slug> --coverage               # which nodes hit
wick workflow test <slug> --record <run-id>        # capture real run sebagai fixture
```

UI:
- Per-workflow "Tests" tab — list test results dgn pass/fail/skipped per case
- Click test → preview canvas dgn path_taken highlighted
- Click failing case → see expected vs got diff
- Coverage map → grey nodes = belum di-test

### Mock generation from run history

`wick workflow test <slug> --record <run-id>`:
- Take existing JobRun (real or simulated)
- Extract trigger event + per-node outputs + connector responses
- Generate `__tests__/integration/auto-<timestamp>.test.yaml` dgn captured data
- User review + edit + commit

MCP equivalent: `workflow_record_test(slug, run_id)`.

### AI-first test workflow

AI compose workflow → AI compose tests untuk verifikasi sebelum
request_review. Pattern:

```
AI prompt: "Buat workflow: ..."
  ↓
AI compose workflow.yaml + edges
  ↓
AI compose __tests__/nodes/*.test.yaml (1 per node)
  ↓
AI compose __tests__/integration/main-flow.test.yaml
  ↓
AI panggil workflow_test(slug)
  ↓
Engine return: 5 pass, 1 fail "expected case bug got case other"
  ↓
AI debug — adjust prompt classify-intent, re-test
  ↓
All pass → AI panggil workflow_request_review
```

Tests = AI's verification loop sebelum manusia review. Reduce
back-and-forth admin approval.

### Fixture generation

Tombol "Capture as fixture" di run detail page → ambil event +
per-node output dari run yang baru jalan, simpan ke `__tests__/`. AI
bisa juga pake `workflow_capture_fixture(run_id)` MCP op.

### MCP ops untuk testing

```
workflow_test(slug, filter?)           → run tests, return [{case, pass, error?, diff?}]
workflow_record_test(slug, run_id)     → generate test YAML dari JobRun
workflow_test_coverage(slug)           → {nodes_hit: [...], nodes_uncovered: [...]}
workflow_simulate(slug, event, mocks)  → run with synthetic event + inline mocks, no persist
```

---

## 15. Bootstrap & hot-reload

### Boot

`internal/jobs/workflow/registry.go` punya `RegisterAll(svc)`:
- Loop `svc.List()`, register tiap workflow ke `jobs.Register(job.Module{
  Meta.Key: "workflow:<slug>:<trigger-idx>", DefaultCron: ..., Run: ...
  })`.
- Idempotent on Key.

Dipanggil dari:
- [internal/pkg/worker/server.go](../pkg/worker/server.go) sebelum
  `configsSvc.Bootstrap`.
- [internal/pkg/api/server.go](../pkg/api/server.go).

### Reload setelah CRUD

CRUD (UI canvas / MCP / hand-edit + fsnotify) → handler panggil
`RegisterAll(svc)` lagi. Worker tick berikutnya pakai schedule baru.

### Delete

Hapus folder + `jobs.Unregister("workflow:<slug>:*")` (perlu tambah
method `UnregisterPrefix` di
[internal/jobs/registry.go](../jobs/registry.go) — sekarang cuma ada
`Register`).

### File watcher

fsnotify watcher di `<BaseDir>/workflows/` — kalau ada file berubah:
- Invalidate hash cache.
- Re-validate workflow (cycle check, schema).
- Push update event via SSE ke UI clients yang lagi buka detail page.

Recommended untuk mendukung gitops + manual edit workflow.

---

## 16. Implicit reply-to-source

Workflow ga punya dedicated `notify:` field. **Notification = action node**
— user/AI compose explicit channel/connector node di graph untuk handle
success/failure/intermediate updates. Lebih transparan, visible di
canvas, ga ada hidden behavior.

Pattern explicit:

```yaml
graph:
  entry: process
  nodes:
    - id: process
      type: agent
      ...
      on_failure: fallback
      fallback: notify-fail        # node ID — used kalau on_failure: fallback

    - id: notify-success
      type: channel
      channel: slack
      op: send_message
      args:
        channel: "{{.Env.LEADERSHIP_CHANNEL}}"
        text: "✓ done: {{.Run.final_result}}"

    - id: notify-fail
      type: channel
      channel: slack
      op: send_message
      args:
        channel: "{{.Env.ONCALL_CHANNEL}}"
        text: "✗ FAIL: {{.Run.error}}"
```

**Tetep ada satu engine convenience: implicit reply-to-source.**

Kalau trigger dari channel (`type: channel`), dan workflow **ga punya**
explicit `type: channel` node yang reply ke event source thread —
engine inject synthetic node di akhir flow (plus synthetic edge dari
node terakhir):

```yaml
# Synthetic, auto-injected (NOT di workflow.yaml)
- type: channel
  channel: <event.channel>
  op: reply_thread
  args:
    channel: "{{.Event.Payload.channel_id}}"
    thread:  "{{.Event.Payload.thread}}"
    text:    "{{.Run.final_result}}"
```

Override: set `reply_source: false` di trigger spec (default `true`).
Atau user inject explicit `type: channel` reply node — engine detect
itu dan skip synthetic.

Untuk skenario complex (post ke #leadership + reply ke source +
email admin), user tulis 3 action nodes terpisah. Engine ga magic-merge.

---

## 17. AI guard / publish-time review

Sebelum workflow dipindah dari `enabled=false` ke `enabled=true`, optional
panggil AI reviewer. Tujuan: catch hal yang manual approval kelewat —
prompt injection, destructive shell, secret leak, classify prompt yang
manipulable, dst.

### Konfigurasi

```go
// internal/jobs/workflow/config.go
type Config struct {
    GuardEnabled    bool   `wick:"bool;default=true;desc=Run AI reviewer before publishing"`
    GuardPreset     string `wick:"text;default=guard;desc=Preset name buat reviewer agent"`
    GuardRules      string `wick:"textarea;desc=Custom rules — appended to default"`
    GuardMode       string `wick:"select:warn,block,off;default=block"`
    GuardTimeoutSec int    `wick:"int;default=60"`
}
```

### Default rules

```
- No destructive shell commands (rm -rf, dd, mkfs).
- No network call to non-allowlisted domain.
- No plaintext secret in YAML/script.
- No prompt that explicit instruct agent to bypass approval / disable gate.
- Cron tidak lebih sering dari 1 menit.
- Notify target valid.
- classify node prompt tidak passthrough {{.Event.Payload.text}} ke shell
  exec tanpa sanitize (prompt-injection vector).
- branch node expr tidak include user-controlled string raw (eval
  injection).
- output_schema declared untuk node yang feed ke shell/python/db_query.
```

### Flow

```
User klik "Enable" / "Save & Enable"
       │
       ▼
Guard enabled? ──no──► commit
       │
       yes
       ▼
Spawn ephemeral agent (preset = GuardPreset)
       │
       ▼
Kirim prompt:
  "Review workflow. Rules: <default + custom>.
   Folder contents: <semua file, secret di-redact>.
   Graph: <yaml.graph>.
   Return JSON: {ok, violations: [{rule, node_id, severity, evidence}]}"
       │
       ▼
Parse → commit/warn/block per mode.
```

Hasil cached selama YAML+script gak berubah (hash content).

### Override

Tombol "Override Guard" require konfirmasi + reasoning text → commit +
audit log entry.

---

## 18. Manager & governance

### Folder ↔ state split

| Sisi | Tinggal di | Alasan |
|---|---|---|
| **Definisi** (graph, triggers, nodes, scripts, prompts) | File `<BaseDir>/workflows/<slug>/` | Gitops, manual-edit |
| **Governance** (approved, approved_by, last_guard_result) | DB `workflow_state` atau file `<slug>/.governance.json` | Tamper-resistant, audit-able |
| **Run history** (state.json, events.jsonl) | File `<slug>/runs/<id>/` | Append-only, large, off-DB |

Pilihan **DB vs file** untuk governance: DB lebih atomic + queryable
(filter "unapproved workflows" cepat). File lebih simple + gitops.
Recommend **DB** karena query freq tinggi (list page selalu join state).

### Tabel `workflow_state` (DB)

| Kolom | Tipe | Catatan |
|---|---|---|
| `id` | UUID PK | = workflow.id |
| `slug` | TEXT | folder name |
| `approved_version` | INT NULL | last approved |
| `approved_hash` | TEXT NULL | snapshot hash |
| `approved_by` | TEXT | user id |
| `approved_at` | TIMESTAMPTZ | |
| `last_guard_at` | TIMESTAMPTZ | |
| `last_guard_result` | JSONB | cached |
| `override_reason` | TEXT NULL | force-approve reason |
| `created_at`, `updated_at` | TIMESTAMPTZ | |

### Approval flow + stale detection

3 state mirip routine doc:

| State | Kondisi | Action |
|---|---|---|
| **Fresh approved** | `approved_version == yaml.version && approved_hash == current_hash` | Jalan normal |
| **Edited (same version)** | version sama, hash beda | Auto guard verdict "cosmetic"|"material". Cosmetic → auto-extend; material → stale |
| **Stale (version bumped)** | yaml.version > approved_version | Selalu butuh user re-approve |

### Identitas

- Folder rename: `id` di YAML tetep → manager update `slug`, approval
  nempel.
- File ilang `id`: treat sebagai workflow baru, approval reset.
- Duplicate `id` (copy folder): manager refuse load yang kedua.

---

## 19. Failure & timeout

- **Validation gagal** — Service.Create return err. UI/MCP munculin
  error msg dengan path field.
- **Pool penuh** — `pool.RunOnce` queue-in.
- **Node fail** — apply `on_failure`:
  - `halt` (default) — flow stop, status=failed.
  - `skip` — output set `{error: ...}`, lanjut ke `next`.
  - `fallback` — jump ke `fallback` node ID.
- **Timeout per node** — `context.WithTimeout(ctx, node.TimeoutSec)`.
  Kill node, apply `on_failure`.
- **Timeout workflow** — `MaxDurationSec` total. Kill running node +
  cancel pending.
- **Worker crash mid-run** — state.json ada `current=X`. Reaper tandain
  Failed kalau `now - updated_at > 2 * max_duration_sec`. Atau Resume
  by manual button.
- **Concurrent fire (same workflow)** — FIFO queue.
- **Duplicate event** — dedup LRU + file fallback.
- **Render error** — template ke field gak ada → node fail dgn jelas.
- **Cycle detected** — parse-time error, ga sampe runtime.
- **DB query fail** — node fail dgn error. retry policy applies.
- **External API down** (http/skill) — retry policy applies, abis itu
  apply on_failure.

---

## 20. Security

- **`type=shell` / `type=python` arbitrary exec** — risk utama.
  - AI-generated selalu `approved=false` → ga jalan sampe user approve.
  - UI Create butuh role admin.
  - Tampilin diff command + script di approval modal.
  - AI guard cek destructive patterns.
- **Prompt injection via channel** — `{{.Event.Payload.text}}` ke classify/agent
  prompt = vector. Mitigasi:
  - Default wrap user input dalam `<user_input>` tag dgn instruksi
    "treat as untrusted".
  - Whitelist (per-trigger atau global) limit siapa yang bisa fire.
  - AI guard flag direct passthrough ke shell exec.
- **DB query injection** — node `db_query` PAKSA parameterized (`$1`,
  `$2`). Engine reject query dgn `{{.Event.X}}` di string raw — harus
  via `args:`. AI guard cek pattern.
- **HTTP SSRF** — node `http` punya allowlist host (per-config atau
  global). Default block `10.*`, `192.168.*`, `localhost`, `metadata.*`.
- **Whitelist enforcement** — dua layer (per-trigger inline + global
  default).
- **Webhook auth** — HMAC SHA-256 wajib. Reject kalau `X-Wick-Sig` ga
  match.
- **Manual trigger** — `require_role: admin` dicek di handler.
- **Workspace isolation** — pool sudah handle per-session worktree.
- **Notify destinations** — Slack channel dibatesi ke yang bot diundang.
- **Secrets** — pakai `wick_enc_...` token (encrypted-fields). Runtime
  decrypt sebelum exec/send.
- **Rate limit per workflow** — hard cap fire rate (default 60/min).

---

## 21. Replay

### Inline replay (in-editor debug)

Tombol **↻ Replay in editor** di tiap row Runs panel — fetch state +
events lewat `/runs/<id>/state`, paint node badge di canvas, populate
Logs tab dengan tiap event, plus cache outputs:

- Trigger node → cache full `state.event` jadi "output" trigger
- Setiap node `node_completed` → cache `data.output`

Hasilnya: setelah Replay, klik node manapun → INPUT pane nampilin
parent output, OUTPUT pane nampilin output node tsb (dari run history).
Tidak fire run baru — purely reload state. Workflow ngak berubah.

Plus: tombol **Export JSON** di tiap row → copy full state+events ke
clipboard buat paste ke bug report / chat.

### Re-run via manual trigger

Untuk fire run baru dengan event yang sama (test fix, audit, regression
check): pakai `?prefill=<runID>` link ke manual runner — UI replay nav,
not auto-execute (memory `replay-navigate-not-autoexecute`). Replay
skip dedup (event_id stamp baru `replay-<uuid>`), tetep lewat whitelist +
queue + guard.

### Per-node replay (debug, future)

Klik node di run timeline → "Re-run from here" → bikin run baru yg state
di-restore sampai node tersebut, lalu lanjut dari node tsb. Berguna pas
debug "node X kasih output beda dari ekspektasi". **Belum diimplementasi.**

### Resume vs Replay

- **Resume** — workflow paused atau crash. Lanjut dari state terakhir.
  Run ID sama.
- **Inline Replay** — workflow selesai (success/fail). State reload ke
  editor, tidak fire run baru. Run ID + state tetap.
- **Manual Replay** — fire run baru dgn event yg sama. Run ID baru.

---

## 22. Pertanyaan terbuka

> **Status legend:** [open] = belum diputuskan · [decided] = ada keputusan,
> tunggu validasi waktu impl · [deferred] = tunda sampai use-case nyata.

1. [**decided**] **Cost tracking granularity** — per-node + aggregate per-run.
   - Storage: tiap node simpan `tokens`, `cost_usd`, `duration_ms` di
     `runs/<run-id>/nodes/<id>.json`.
   - `state.json` aggregate `cost_usd_total` + `tokens_total` per run.
   - UI run timeline tampilin per-node cost (small text di bawah node
     status), run header tampilin total.
   - `cost_usd` hitung dari token count × provider pricing table (config).
     Provider yang ga ekspose token count (CLI without usage report) →
     null, UI tampilin "—".
   - Audit page: query JobRun by date range + filter by workflow, tampilin
     daily/weekly cost rollup.
2. **Output verbosity di run detail** — full output object atau truncated?
   Threshold size?
3. [**decided**] **Sub-workflow (call workflow from workflow)** —
   - Action node `type: workflow` invokes another workflow as a step.
     Same engine, same audit, blocking call:
     ```yaml
     - type: workflow
       slug: other-workflow
       args: {event: {Type: "manual", Payload: {...}}}
       timeout_sec: 60
     ```
   - Sub-workflow runs in current run's lineage — events.jsonl logs
     `subworkflow_started` / `subworkflow_completed` with child run_id.
   - Output: `{result, error?}` dari sub-workflow's final state.
   - Cycle detection: parser validate sub-workflow doesn't recursively
     reference parent (DAG-of-workflows). Engine error at save time.
   - **Async fire-and-forget pattern**: gunakan `connector: webhook` ke
     wick's own webhook trigger URL — non-blocking, ga butuh node type
     khusus.
4. **Loop node** — `while`/`for_each` di tengah graph? Hindari karena
   bikin engine kompleks. Kalau perlu iterasi list, satu node `agent`
   atau `python` handle internally.
5. [**deferred**] **Time-window event aggregation** — "kalau 5 pesan dgn keyword X
   dalam 10 menit, fire workflow"? Bukan single-event trigger. Mungkin
   trigger type baru `event_window` dengan dedupe + buffer. Tunda
   sampai use case muncul.

5a. [**decided**] **History retention (JobRun + state.json bloat)** —
    channel-rame workflow bisa 1000s of runs/day. Cleanup strategy:
    - Default retention: **30 hari** success runs, **90 hari** failed runs.
    - Per-workflow override via `retention:` field di `workflow.yaml`:
      ```yaml
      retention:
        success_days: 30
        failure_days: 90
        keep_max: 10000           # absolute cap per workflow
      ```
    - Daily cleanup job (reuse `connector-runs-purge` pattern) prune
      `runs/<run-id>/` folder + JobRun row.
    - **Hot path optimization**: workflow yg cuma butuh "did this event
      get handled?" → simpan ke dataset (small, queryable) bukan rely on
      JobRun history. JobRun cuma audit/debug.
    - Engine emit warning kalau workflow accumulate > 50% of `keep_max`
      → suggest tighter retention atau dataset approach.

6. [**decided**] **Workflow versioning** — rely on git, no wick layer.
   - Workflow folder = git-tracked. `git log workflows/<slug>/` =
     audit history. `git diff` between commits = compare versions.
   - `version:` field di workflow.yaml = signal user-driven (manual
     bump untuk material change). Drives re-approval trigger.
   - `approved_hash` di state = SHA of folder content. Diff → stale,
     re-approve.
   - Rollback = `git revert` + re-run wick reload. Wick ga tracking
     history beyond `approved_version`/`approved_hash`.
   - **Dataset versioning beda** (lihat §12) — file `history/v<N>.yaml`
     karena dataset schema diff lebih sering + butuh in-process diff
     untuk migration check. Workflow change ga butuh granularity itu.
7. **Multi-tenant** — workflow yang sama jalan dgn config beda per
   tenant? Mungkin via `Env` per-trigger atau parameter passing. Tunda
   sampai use case muncul.
8. **Visualization library final pick** — Drawflow vs Rete vs custom
   SVG? Decision sebelum coding canvas page.
9. **State storage**: file (per-run folder) vs DB (per-run row)? File
   = bisa di-grep + gitops; DB = atomic + queryable. Recommend file
   for now, DB kalau ratusan ribu run/hari (jauh banget).
10. [**decided**] **Skill discovery** — dynamic via `Provider.ListSkills()`
    per provider (Claude Code: read `~/.claude/skills/` directory atau
    `claude --list-skills`; Codex/Gemini TBD). Cached di provider's
    status_cache, refresh manual via MCP/UI. Security: skill execution
    inherit agent's gate command-approval (existing). Skill listing =
    discovery only, no exec privilege. Wick admin tag-based visibility
    apply ke workflows yg pakai skill (lihat §20 Security).
11. [**decided**] **Provider CLI capabilities matrix** —
    - **Universal baseline**: prompt-based JSON shape + parser (layer 1
      reliability). Bekerja di semua CLI termasuk yang ga support
      structured output mode.
    - **Per-provider optimization** (opt-in, verify saat impl):
      - **Claude Code CLI**: `--output-format json|stream-json` —
        document tested support, pakai kalau available.
      - **Codex CLI**: cek di impl time, fallback prompt-only kalau ga
        ada flag.
      - **Gemini CLI**: cek di impl time.
    - Provider impl di `internal/agents/provider/<name>/` declare
      `Capabilities()` returning `{structured_output: bool}`.
    - Engine pilih path optimal: structured mode kalau supported,
      fallback prompt-only kalau ga.
    - Layer 4-5 (fuzzy + retry) tetep aktif sebagai safety net regardless
      of provider support.

12. [**decided**] **Classify cost cap per workflow** —
    - **Rate limit**: hard cap 60/min per workflow (existing §20 Security).
    - **Daily budget (tokens)**: configurable via workflow env field
      `MAX_DAILY_CLASSIFY_TOKENS` (widget: number, default 0=unlimited).
      Engine track running sum di-state per workflow per UTC day. Hit cap
      → reject `classify` execution dengan error "daily budget exceeded".
      `default` case fire kalau ada (graceful).
    - **Input cache (opt-in)**: per `classify` node field
      `cache_ttl_sec: 3600` — hash `Event.Text` + node prompt template,
      cache verdict. Reset on prompt change. Saves N calls untuk pesan
      identik berulang.
    - **Spam mitigation**: dedup TTL trigger sudah handle duplicate event
      (existing §7). Cost cap = defense terhadap legitimate-but-expensive
      spam.

13. [**decided**] **MCP token scoping defaults** —
    Token scopes per use case (set saat token issuance):

    | Use case | Read | File CRUD | Canvas ops | Toggle enable | Approve |
    |---|---|---|---|---|---|
    | **AI assistant** (default for MCP-created tokens) | ✓ | ✓ | ✓ | ✗ | ✗ |
    | **Admin user** | ✓ | ✓ | ✓ | ✓ | ✓ |
    | **Read-only viewer** | ✓ | ✗ | ✗ | ✗ | ✗ |
    | **CI/CD push** | ✓ | ✓ | ✗ | ✓ (post-CI) | ✗ |

    - AI tokens default = write-without-enable, wajib panggil
      `workflow_request_review` buat ngumumin perubahan ke admin.
    - Admin approve via UI (lihat AI guard §17).
    - Token allowlist per workflow slug (`workflow_allowlist: ["*"]` atau
      specific slugs).
    - Audit log catat tiap MCP call (token ID, op, args hash, timestamp,
      result), reuse infra audit yang sudah ada.
14. [**deferred**] **`workflow_grep` MCP op** — tunda. AI di remote env
    cuma sekarang-sekarang ini cari "workflow mana pakai connector X"
    via N round-trip `workflow_list` + `workflow_get`. Workflow biasanya
    <100 per repo, ga ada urgency optimize. Add kalau use case nyata
    muncul (mis. AI butuh refactor batch).
15. **Env widget extensibility** — workflow env reuse config-tags
    widget vocab (text/textarea/secret/number/checkbox/dropdown/email/
    url/color/date/datetime/kvlist/picker). Cukup buat workflow,
    atau butuh widget khusus workflow (`workflow_ref` cross-flow,
    `dataset_ref` dropdown ke datasets, `node_ref` reference ke node
    di graph)? Saran: pakai `picker` dgn LookupProvider khusus
    (`workflow.workflows`, `workflow.datasets`) — reuse infrastructure,
    no new widget.
16. **Dataset storage shape** — single shared table
    `wick_datasets_rows (dataset_slug, pk, data JSONB, ...)` vs
    per-dataset native table `wick_dataset_<slug>` dgn real columns.
    Single shared = simpler (no DDL per dataset, JSONB flexible),
    per-table = native B-tree indexes (faster filter, type CHECK
    constraints, JSONB GIN-only). Saran: single shared dgn partial
    GIN index per dataset (`CREATE INDEX ... ON wick_datasets_rows
    ((data->>'status')) WHERE dataset_slug='events'`) — best of
    both. Need benchmark di prod scale (>1M rows per dataset).
17. [**decided**] **Dataset row-level access** —
    - V1 = `access.workflows` allowlist di `dataset.yaml` (read /
      read_write per workflow slug).
    - **Stamping**: tiap row di-insert otomatis ada `_meta.created_by_workflow`
      di JSONB. Used buat audit + future RBAC.
    - **Filter by workflow** (opt-in di dataset.yaml):
      ```yaml
      access:
        workflows: [support-triage, incident-resp]
        row_filter: by_creator    # by_creator | none
      ```
      `by_creator` = workflow cuma boleh `dataset_*` ke row yang
      `_meta.created_by_workflow == own_slug`. Cross-workflow read
      di-allow, write/delete reject.
    - Multi-tenant deeper (per-user, per-team) tunda sampai use case.
18. **Dataset query DSL** — pakai WHERE clause string (SQL-flavored)
    atau structured object? Saran: structured object di YAML (lebih
    safe, lebih readable), tapi engine translate ke SQL underneath.
    Ad-hoc query console di UI pakai raw SQL.
19. [**decided**] **Env schema rename / migrate** —
    - Rename via `key:` modifier (mirror config-tags pattern):
      ```yaml
      env:
        - name: NOTIFY_CHANNEL      # new name
          key: slack_channel         # legacy key stays in env.yaml
          widget: text
      ```
      `name:` = display, `key:` = underlying storage. Rename `name:`
      without touching `key:` = no data migration needed.
    - Hard rename (storage key change): UI prompt "Migrate stored
      value?" → engine rewrite env.yaml atomic.
    - Schema diff at load time: field di env.yaml yg ga ada di schema
      → UI warn "orphan field, will be dropped on next save". Field
      di schema tapi ga di env.yaml → use `default:` atau prompt user.
    - No history file untuk env (use git: `git log env.yaml` =
      sufficient audit).
20. **Datasets vs configs (existing wick concept)** — wick udah punya
    `internal/configs/` tabel `configs`. Boleh merge atau pisah?
    Saran: pisah — `configs` = singleton per-job per-module config
    (current), `datasets` = arbitrary multi-row data tables (new).
    Beda use case.

Jawab sebelum implementasi biar scope ga melar.
