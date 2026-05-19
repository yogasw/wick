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
       id: other-workflow
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
   - Workflow folder = git-tracked. `git log workflows/<id>/` =
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
    - Token allowlist per workflow id (`workflow_allowlist: ["*"]` atau
      specific ids).
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
      read_write per workflow id).
    - **Stamping**: tiap row di-insert otomatis ada `_meta.created_by_workflow`
      di JSONB. Used buat audit + future RBAC.
    - **Filter by workflow** (opt-in di dataset.yaml):
      ```yaml
      access:
        workflows: [support-triage, incident-resp]
        row_filter: by_creator    # by_creator | none
      ```
      `by_creator` = workflow cuma boleh `dataset_*` ke row yang
      `_meta.created_by_workflow == own_id`. Cross-workflow read
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
