## 12. Data Tables — shared key/value tables, UI-first

Beberapa state perlu hidup di luar single run:
- **Dedup**: "udah pernah handle event_id X?" — query existence
- **State machine**: tickets pending escalation, users opted-out
- **Cache**: hasil enrichment API dgn TTL (avoid re-fetch)
- **Lookup**: tabel referensi (SLA per priority, routing rules, dst)

Wick punya **Data Tables** sebagai tool terpisah (`internal/tools/data-tables/`)
— inspirasi dari [n8n Data Tables](https://docs.n8n.io/data/data-tables/).
Standalone module, own DB layer, own MCP namespace. Agent workflow consume
lewat node types; non-agent tool lain juga boleh pakai.

Sidebar menu sejajar Workflows / Workspaces / Presets / Providers / Channels:

```
Wick AGENTS
├─ New session
├─ Overview
├─ Workflows
├─ Workspaces
├─ Presets
├─ Providers
├─ Channels
└─ Data Tables          ← new
```

---

### Storage — DB only, no YAML

**Schema = DB row, data = DB row.** UI is source of truth. No file, no git
history, no `dataset.yaml`. Schema change = atomic DB transaction. Versioning
via audit table kalau perlu (opt-in per table).

**Why DB-only (drop YAML + history snapshots):**
- UI-first = file edit bukan flow utama. Drift file↔DB cuma bikin pusing.
- Schema change rare-but-trivial — single Postgres TX, no migration job
  unless data shape berubah.
- Backup uniform via `pg_dump`.
- Export/import via bundle (lihat §Export/Import) buat gitops-style portability
  kalau user mau.
- AI manage lewat MCP — sama-sama DB call.

**Tables:**

```sql
-- Table metadata (one row per data table)
CREATE TABLE wick_data_tables (
  slug         TEXT     PRIMARY KEY,
  name         TEXT     NOT NULL,
  description  TEXT,
  schema       JSONB    NOT NULL,            -- columns + indexes + options
  access       JSONB    NOT NULL DEFAULT '{}', -- allowlist + flags
  retention    JSONB,                         -- ttl_days, by_column
  created_by   TEXT,
  created_at   TIMESTAMPTZ DEFAULT now(),
  updated_at   TIMESTAMPTZ DEFAULT now()
);

-- Rows (shared single table for all data tables)
CREATE TABLE wick_data_table_rows (
  table_slug   TEXT     NOT NULL REFERENCES wick_data_tables(slug) ON DELETE CASCADE,
  pk           TEXT     NOT NULL,
  data         JSONB    NOT NULL,
  created_at   TIMESTAMPTZ DEFAULT now(),
  updated_at   TIMESTAMPTZ DEFAULT now(),
  PRIMARY KEY (table_slug, pk)
);

-- Optional audit (opt-in via access.audit_changes)
CREATE TABLE wick_data_table_audit (
  id           BIGSERIAL PRIMARY KEY,
  table_slug   TEXT     NOT NULL,
  op           TEXT     NOT NULL,             -- schema_change | insert | update | delete
  actor        TEXT,                          -- user email | workflow slug | mcp client
  diff         JSONB,                         -- prev/next snapshot
  at           TIMESTAMPTZ DEFAULT now()
);

-- Partial indexes per table needs (created by engine on schema save):
CREATE INDEX idx_events_status ON wick_data_table_rows ((data->>'status'))
  WHERE table_slug = 'events';
```

**Why shared single rows table:**
- No DDL per data table — schema change tinggal update `wick_data_tables.schema`,
  validator app-layer cek shape pas insert/upsert.
- 100 tables = 1 table, bukan 100 tables. Admin/backup ringkas.
- Composite PK `(table_slug, pk)` natural index buat hot path.
- JSONB native ops, GIN index untuk arbitrary path.

**Tradeoff:** column type CHECK constraint ga native (validate app-layer).
Type-strict B-tree = bikin partial functional index per kebutuhan.

---

### Column types — app-layer validation

Semua row tinggal di JSONB `data` column. "Column type" = app-layer rule.
Engine validate shape sebelum insert/upsert; mismatch → reject dgn error jelas.

| Type | Validation | JSONB shape | Index strategy |
|---|---|---|---|
| `string` | string, optional max_len | `"abc"` | partial `((data->>'col'))` |
| `int` | integer, optional min/max | `42` | partial `(((data->>'col')::int))` |
| `float` | number | `3.14` | partial `(((data->>'col')::float))` |
| `bool` | true/false | `true` | partial `(((data->>'col')::bool))` |
| `timestamp` | ISO 8601 | `"2026-05-19T08:00:00Z"` | partial `(((data->>'col')::timestamptz))` |
| `json` | any object/array | nested | GIN `(data->'col')` |
| `enum` | string in `options:` list | `"received"` | partial `((data->>'col'))` |

Primary key column wajib explicit — engine extract value ke `pk` column untuk
fast lookup. Indexes declare per kolom (`indexed: true`) atau composite di
`indexes:` section. Query non-indexed JSONB path = full scan, audit warn (ga block).

---

### Schema shape (DB JSONB, not file)

`wick_data_tables.schema` JSONB body:

```json
{
  "columns": [
    { "name": "event_id", "type": "string", "primary_key": true },
    { "name": "workflow_slug", "type": "string", "indexed": true },
    { "name": "handled_at", "type": "timestamp", "indexed": true },
    { "name": "result", "type": "json" },
    { "name": "trigger_source", "type": "string", "default": "unknown" }
  ],
  "indexes": [
    ["workflow_slug", "handled_at"]
  ],
  "strictness": "strict"
}
```

`wick_data_tables.access` JSONB body:

```json
{
  "workflows": ["support-triage", "support-followup"],
  "read_only_workflows": ["audit-monthly"],
  "ui_editable": true,
  "mcp_writable": true,
  "audit_changes": false
}
```

`wick_data_tables.retention` JSONB body:

```json
{ "ttl_days": 90, "by_column": "handled_at" }
```

---

### Access — node + MCP + UI + Service, no raw SQL

Data Tables bukan untuk di-query bebas via `db_query`. Akses cuma lewat:

1. **Node types** di workflow: `datatable_query`, `datatable_insert`,
   `datatable_upsert`, `datatable_delete`, `datatable_count`.
2. **MCP ops** untuk AI.
3. **UI** di Data Tables tab — schema-aware form/table view.
4. **Internal Service interface** (Go):

```go
// internal/tools/data-tables/service.go
type Service interface {
    List() ([]Table, error)
    Get(slug string) (Table, error)
    Create(t Table) error
    UpdateSchema(slug string, patch SchemaPatch) error
    Drop(slug string) error

    Query(slug string, q Query) (QueryResult, error)
    Insert(slug string, row map[string]any) (any, error)
    Upsert(slug string, key []string, row map[string]any) (UpsertResult, error)
    Delete(slug string, where Where) (int64, error)
    Count(slug string, where Where) (int64, error)
}

type Query struct {
    Where     Where
    Returning []string
    OrderBy   []OrderClause
    Limit     int
    Offset    int
}
```

Service translate ke parameterized SQL. Kontrak: no string concat, no raw
SQL passthrough.

**Why no raw SQL:**
- Safety — workflow author bisa AI, ga trust raw SQL ke prod DB.
- Schema enforcement — query divalidasi terhadap declared columns.
- Access control — `access.workflows` allowlist enforceable di Service.
- Audit — tiap query log-able dgn structured args.
- Future backends — Mongo/Redis swap-able tanpa break node contract.

Power user butuh raw SQL ad-hoc: pakai UI Query Console (audit-able) atau
`db_query` node ke external read-replica.

---

### Workflow binding

Workflow declare data tables yang dia pakai di `data_tables:` field:

```yaml
# workflow.yaml
data_tables:
  - name: events                     # alias
    ref: processed_events            # data table slug
    mode: read_write                 # read | read_write
```

Reference di node via alias `events`, ga slug langsung — biar rename safe:

```yaml
- type: datatable_query
  table: events
  where: {event_id: "{{.Event.EventID}}"}
```

---

### CRUD operations (node types — lihat §5)

- `datatable_query` — SELECT dgn where + returning + limit
- `datatable_insert` — INSERT row, fail kalau pk conflict
- `datatable_upsert` — INSERT atau UPDATE based on pk
- `datatable_delete` — DELETE rows matching where
- `datatable_count` — count rows

Engine paksa parameterized — value dari `{{.Event.X}}` di-bind, ga string-concat.

**Where clause conditions** (match n8n surface):

`equals`, `not_equals`, `gt`, `gte`, `lt`, `lte`, `is_empty`, `is_not_empty`,
`contains`, `in`.

---

### UI di tools/data-tables

Tab "Data Tables" sejajar Workflows. Files:
- `internal/tools/data-tables/handlers.go`
- `view/datatables_list_templ.go`, `datatables_detail_templ.go`

Routes:
```go
r.GET("/data-tables", listPage)
r.GET("/data-tables/{slug}", detailPage)
r.POST("/data-tables", create)
r.PATCH("/data-tables/{slug}/schema", updateSchema)
r.DELETE("/data-tables/{slug}", dropTable)

r.GET("/data-tables/{slug}/rows", queryRows)         // paginated table view
r.POST("/data-tables/{slug}/rows", insertRow)
r.PATCH("/data-tables/{slug}/rows/{pk}", updateRow)
r.DELETE("/data-tables/{slug}/rows/{pk}", deleteRow)
r.POST("/data-tables/{slug}/query", customQuery)     // ad-hoc query

r.POST("/data-tables/{slug}/import", importCSV)      // CSV upload (n8n parity)
r.GET("/data-tables/{slug}/export.csv", exportCSV)
r.GET("/data-tables/{slug}/export.json", exportJSON)
```

**List page** — table-of-tables, columns: name, row count, size, last modified,
used-by-workflows. Top-right button: **New data table** (manual define columns
| upload CSV).

**Detail page** — 3 panels (mirip n8n):
1. **Schema editor** — column list, add/remove/rename, type picker, default,
   indexed toggle, primary key flag. Save → atomic DB TX, audit entry kalau
   `audit_changes: true`. Destructive change (drop col, rename) → typed
   confirmation modal + dry-run preview.
2. **Rows view** — paginated table, sortable, filterable per column. Inline
   edit kalau `ui_editable: true`. Bulk delete + export CSV/JSON.
3. **Query console** — text input untuk where clause, execute, show result.
   Schema-aware autocomplete.

**Empty state** — onboarding card: "Create your first data table" with two
CTAs: *Define columns manually* / *Upload CSV*.

---

### MCP ops

```
datatable_list()                          → list data tables
datatable_get(slug)                       → schema + manifest
datatable_create(slug, schema)            → create new
datatable_update_schema(slug, patch)      → migrate schema
datatable_drop(slug)                      → drop entire table
datatable_query(slug, where, ...)         → rows
datatable_insert(slug, row)               → insert
datatable_upsert(slug, row)               → upsert
datatable_delete(slug, where)             → delete
datatable_count(slug, where)              → count
datatable_export(slug, format)            → bundle bytes (csv | json | bundle)
datatable_import(bundle, on_conflict)     → {merged_rows, skipped, errors[]}
datatable_infer_schema(slug)              → schema dari sample rows (adoption)
```

AI bisa bikin data table on-the-fly:

```
AI → datatable_create("support-events-dedup", schema={...})
AI → workflow_create("support-triage", template="empty")
AI → workflow_set_data_tables("support-triage", [{name: "events", ref: "support-events-dedup"}])
AI → workflow_add_node(... datatable_query event_id check ...)
AI → workflow_add_node(... datatable_insert mark handled ...)
```

---

### Schema migration

Schema = JSONB di `wick_data_tables.schema`. Migration = `UPDATE wick_data_tables`
+ optional data migration job kalau JSONB rows perlu disentuh.

**Schema operations + data migration needs:**

| Op | Schema change | Data migration required? |
|---|---|---|
| Add column | append validator rule | ✗ (existing rows default null/default) |
| Drop column (soft) | hide from schema | ✗ (key tetep di JSONB) |
| Drop column (hard) | remove + strip JSONB | ✓ — batch UPDATE strip key |
| Rename column | update validator | ✓ — batch UPDATE rename JSONB key |
| Change type | new validation rule | maybe — kalau lossy (string → int), butuh transform |
| Add index | partial functional index | ✗ (DDL only, CONCURRENTLY) |
| Drop index | DROP INDEX | ✗ |

**Data migration sebagai Job:**

Kalau migration butuh sentuh JSONB rows existing:
- Engine create JobRun dgn `Module: "datatable-migration"`, `Args: {table_slug, ops}`.
- Job loop rows dgn `WHERE table_slug = '<slug>'`, apply ops, batch UPDATE.
  Progress visible di JobRun history.
- Engine reject `datatable_insert`/`datatable_upsert` saat migration running
  kalau ops mutate fields yang di-touch (avoid race).

**Audit (opt-in via `access.audit_changes: true`):**

Tiap schema change tulis row di `wick_data_table_audit` (`op = schema_change`,
`diff = {before, after}`, `actor = user_email | mcp_client_id`). Cocok buat
compliance. Default off.

---

### Retention + cleanup

Daily job (reuse `connector-runs-purge` pattern) prune rows yang melebihi
`retention.ttl_days`. Cleanup log entry. Per-table jadwal override-able.

---

### Sharing antar workflow — first-class

Share data table across workflow = use case utama (dedup events, cross-workflow
state machine, shared lookup). **Safety dari explicit contract**:

```json
// wick_data_tables.schema
{
  "strictness": "strict",
  "columns": [
    { "name": "id", "type": "string", "primary_key": true, "required": true },
    { "name": "source", "type": "enum", "options": ["slack", "pagerduty", "calendar"] },
    { "name": "status", "type": "enum", "options": ["received", "processing", "done"] },
    { "name": "handled_at", "type": "timestamp" }
  ]
}
```

```json
// wick_data_tables.access
{
  "workflows": ["webhook-handler", "calendar-poller", "slack-monitor"],
  "read_only_workflows": ["audit-monthly"],
  "row_filter": "none"
}
```

**3 safety layers:**

1. **`strictness: strict`** (default) — semua field declared. Typo'd key →
   reject. Validator enforce shape = contract antara workflow.
2. **`access.workflows` allowlist** — workflow ga di-list ga bisa write.
   Default empty = no access. Add new workflow share = eksplisit informed decision.
3. **`row_filter: by_creator`** (opt-in) — workflow ga boleh sentuh row workflow
   lain. Engine stamp `_meta.created_by_workflow`, reject update/delete dari
   workflow lain. Default `none` (full share).

**Concurrent write semantics:**
- Postgres row-level lock — concurrent insert ke pk berbeda OK.
- Upsert dgn pk conflict serialized otomatis.
- UPDATE/DELETE dgn WHERE filter pakai SELECT FOR UPDATE kalau perlu strict ordering.
- Read concurrent paralel, no lock.

**Strictness modes:**

| Mode | Validator behavior | Use case |
|---|---|---|
| `strict` (default) | Reject insert kalau ada extra key di luar `columns` | Production, shared |
| `lax` | Accept extra keys, simpan ke JSONB. Warn saat read. Query extra key = full scan | Dev iteration |

**Kapan separate table > shared:**
- Data domain fundamental beda — `events` di support ≠ `events` di analytics.
- Privacy isolation — workflow A ga boleh tau row workflow B ada.
- Different retention — workflow A 30d, workflow B 1y.

Default: share kalau data sama, separate kalau data beda. Schema contract =
safety mechanism.

---

### CSV import (n8n parity)

UI **New data table** → tab **Upload CSV**:
1. User upload `.csv`. Engine sniff header + first 100 rows.
2. Infer column types (`string` default; `int`/`float` kalau semua sample
   numeric; `timestamp` kalau parseable ISO/RFC). User can override per column.
3. User pick primary key column.
4. Preview generated schema + first 10 rows.
5. Click **Create & import** → atomic: insert schema row, batch INSERT data
   rows, build indexes.

Limit (initial): 10MB CSV upload, 100k rows per import. Larger via MCP
`datatable_import` bundle.

---

### Export/Import bundle (gitops-style, opt-in)

UI button **Export** atau MCP `datatable_export(slug)`:

```
events.tar.gz
├─ schema.json          # schema + access + retention
└─ rows.jsonl           # ordered by pk
```

Import via UI upload atau MCP `datatable_import(bundle, on_conflict)`:

```
on_conflict: abort | overwrite | skip | merge
```

Flow:
1. Extract bundle, parse `schema.json`.
2. Check target wick instance:
   - **Ga ada existing table slug sama** → straight import (create schema row,
     batch INSERT data rows).
   - **Existing table, schema hash sama** → append mode (skip duplicate pk
     default, atau per `--on-conflict`).
   - **Existing table, schema diff** → prompt:
     - `[abort]` (default), `[overwrite local]`, `[skip import]`, `[merge]`.
3. Conflict per row: per `--on-conflict`.

---

### Adoption — orphan rows tanpa schema

```
Rows exist di wick_data_table_rows (table_slug='events')
tapi wick_data_tables.slug='events' ga ada.

→ Engine detect orphan pas boot atau startup audit
→ UI list page show "Orphan table" badge dgn row count
→ User klik → adoption modal:
  ├─ [Adopt] → sample N rows, infer schema dari JSONB keys,
  │            generate schema draft. User review + edit. Save.
  └─ [Drop] → DELETE FROM wick_data_table_rows WHERE table_slug=...
              (typed confirmation kalau row count > 0)
```

---

### Migration safety

Setiap schema change yg sentuh existing rows = formal flow:

1. User edit schema (UI form atau MCP `datatable_update_schema`).
2. Engine compute diff vs current schema.
3. **UI Preview panel:**
   - Schema diff color-coded (added green, dropped red, type yellow).
   - Data impact estimate: "1,247 rows touched, ops: rename JSONB key
     'name' → 'event_name', strip 'legacy_id'".
   - Estimated duration based on row count + ops complexity.
4. **[Dry-run]** → validate each row against new schema in-memory shadow,
   report rows that ga fit (mis. enum out of new options). No data touched.
5. **[Apply]** (typed confirmation kalau destructive):
   - Engine start migration JobRun.
   - Atomic per-batch (1000 rows default) dalam transaction.
   - Throttle 100ms between batches.
   - Progress di JobRun + table detail page.
   - Pause/resume — state persist di JobRun.
6. **Job complete:**
   - New schema active di `wick_data_tables.schema`.
   - Audit entry kalau `audit_changes: true`.
7. **Rollback:**
   - Partial fail mid-batch → rollback last batch transaction, mark migration
     `failed`. User retry atau revert via re-PATCH old schema.

**Idempotent migration ops:**
- Engine stamp `_meta.migrated_at: <ts>` per row after touch.
- Re-run job skip rows yang sudah stamped after `migration.started_at`.
- Crash mid-job → restart aman, lanjut dari row yang belum ke-stamp.

---

### Schema mismatch — runtime behavior

Workflow A schema expect `priority: enum[low|medium|high]`, rows existing
`priority: 1|2|3`:

- **Read** (`datatable_query`) — engine return rows as-is, surface validation
  warning di run logs ("row pk=X has invalid priority '1', not in enum").
- **Write** (`datatable_insert`, `datatable_upsert`) — strict mode reject,
  lax mode accept + warn.

Workflow author handle dgn:
- `transform` node antara query dan use — normalize old format.
- Schema migration job — batch UPDATE existing rows ke new format.

---

### Differentiator vs DB query

| | `db_query` | `datatable_*` |
|---|---|---|
| Schema source | external system | `wick_data_tables.schema` (DB JSONB) |
| Storage | user-configured DSN | wick's Postgres (same DB) |
| Discovery | manual (user tau struktur) | MCP `datatable_list` + UI |
| UI | none (just node config) | full table view + query console |
| Migration | user's responsibility | wick-managed (audit-able) |
| Access control | DB-level | `access` JSONB allowlist |
| TTL cleanup | external | built-in |

Use `db_query` kalau data live di external system. Use `datatable_*` kalau
data baru lahir dari wick (workflow operation, AI, manual UI entry).

---

### Limits (initial)

- Total rows per instance: soft cap 1M (config-able), warn at 80%.
- Row size: 64KB JSONB hard cap.
- Tables per instance: no hard cap.
- CSV upload: 10MB, 100k rows per import.
- Audit retention: 90d (config-able).

Sejajar n8n's 50MB default tapi lebih longgar (wick assume self-hosted Postgres).

---
