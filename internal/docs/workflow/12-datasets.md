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

