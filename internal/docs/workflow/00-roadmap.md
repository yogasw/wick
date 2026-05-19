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
| 19. Canvas UX polish | ✅ done | Lock toggle (drawflow `fixed` mode + manual selection rewire so inspector still opens); Figma-style scroll/trackpad pan (replaces drag-pan); marquee box-select + multi-drag + multi-delete; fit-to-view on load with `[zoom_min..1.0x]` clamp + double-RAF measure + `.wf-fitting` opacity gate to kill the corner→centre flash; native `confirm()` / `alert()` replaced by reusable `ui.Dialog` + `wickConfirm` / `wickAlert` Promise helpers mounted globally in `ui.Layout` |

Deferred from above (out-of-scope for the package, wire when concrete UIs land):
- ~~fsnotify watcher~~ ✅ wired as 3s poll-based watcher in
  [`setup/watcher.go`](../../agents/workflow/setup/watcher.go) (no
  new dep) — calls `HotReload` on mtime change, unregisters ids
  whose folder disappears. See §15.
- Postgres-backed DatasetService + Postgres `wick_workflow_state` table
  (in-memory + per-workflow `state.json` shim sudah jalan)
- Per-provider impls (Claude Code / Codex / Gemini) — abstraction
  Provider tinggal di-implement di `internal/agents/provider/`
- ~~CLI `wick workflow test <id>`~~ ✅ wired via cobra subcommand
  in [`cmd/cli/workflow.go`](../../../cmd/cli/workflow.go) (RunAll
  + `--filter`; `--integration`/`--watch`/`--coverage`/`--record`
  not yet)
- ~~Webhook HMAC enforcement~~ ✅ wired in
  [`trigger/webhook.go`](../../agents/workflow/trigger/webhook.go) +
  `Router.WebhookSecretFor`. Rejects invalid `X-Wick-Sig` when
  `secret_ref` declared. See §7.
- Loki push for the structured log mirror — payload shape is already
  Loki-compatible (label dimensions = `wf_id`/`wf_run_id`/`wf_event`),
  just need the HTTP sink wired
- Run history import from Loki — reverse direction, rebuild
  `runs/<id>/state.json` from log entries when local files purged
- Per-trigger fan-out in engine — today's engine fires one chain
  per trigger event; fan-out (one trigger → many parallel chains)
  needs Router/Engine support for multi-EntryNode per Trigger
- ~~db_query node executor~~ ✅ wired in
  [`nodes/db_query.go`](../../agents/workflow/nodes/db_query.go) —
  parameterised SQL, DSN from env key, returns `rows`/`row_count`/`columns`
- ~~Test result UI panel + Test case manager~~ ✅ Tests tab in
  bottom panel with RunAll/RunOne, coverage summary, modal-based
  fixture CRUD. See §10 + §14.
- ~~SSE event ts invariant + FE state backfill~~ ✅ `ev.TS` stamped
  once at emit, FE dedups SSE ↔ `/runs/<id>/state` by
  `(ts|event|node|case)`. See §6 + §10.

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
- [ ] Cron path: register sebagai `jobs.Module` Key `workflow:<id>:cron-<idx>`, reuse existing scheduler
- [ ] Manual trigger: UI button → enqueue
- [ ] Webhook handler: mount `/hooks/<id>/<path>`, HMAC verify, path templating
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
- [ ] `session_test.go` — `persistent` mode persist cross-run via session ID `workflow:<id>:persistent`
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
- [ ] CLI: `wick workflow test <id> --filter --integration --watch --coverage --record`
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

