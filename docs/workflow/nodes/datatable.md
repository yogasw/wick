---
outline: deep
---

# `datatable_*`

Data tables are wick's shared key/value store. One data table = one named rowset with a typed schema, declared in the **Data Tables** tab and bound to a workflow via its `data_tables:` block. Seven node types share one executor:

| Type | Operation |
|---|---|
| `datatable_get` | Load one row by primary key. Branches on `found` / `not_found`. |
| `datatable_exists` | Check whether any row matches. Branches on `true` / `false`. |
| `datatable_query` | Multi-row search with `where` / `order_by` / `limit`. |
| `datatable_count` | Count rows matching `where` without loading them. |
| `datatable_insert` | Insert a new row; fails on PK conflict. |
| `datatable_upsert` | Insert or update by primary key. Returns `action: insert\|update`. |
| `datatable_delete` | Delete rows matching `where`. |

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/datatable.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/datatable.go) |
| **Engine** | In-memory by default — see the [data tables design doc](https://github.com/yogasw/wick/blob/master/internal/docs/workflow/12-data-tables.md) for the Postgres-backed roadmap. |

## Schema (per type)

### `datatable_get`

| Field | Required | Notes |
|---|---|---|
| `table` | ✅ | Data table alias from the workflow's `data_tables:` block. |
| `key` | ✅ | Primary key value (templated). |

### `datatable_exists`, `datatable_delete`

| Field | Required | Notes |
|---|---|---|
| `table` | ✅ | |
| `where` | ✅ | YAML map of field equality filters (templated). |

### `datatable_query`

| Field | Required | Notes |
|---|---|---|
| `table` | ✅ | |
| `where` | | Equality filters. |
| `order_by` | | Column name (prefix `-` for DESC). |
| `limit` | | Row cap. |
| `offset` | | Skip rows. |

### `datatable_count`

| Field | Required | Notes |
|---|---|---|
| `table` | ✅ | |
| `where` | | Same shape as `datatable_query`. |

### `datatable_insert`, `datatable_upsert`

| Field | Required | Notes |
|---|---|---|
| `table` | ✅ | |
| `key` | ✅ | PK value(s) (templated). |
| `row_values` | ✅ | Column → value map (templated). |

## Output

| Op | Fields |
|---|---|
| `datatable_get` | `row: map[string]any` — nil if `not_found` branch taken. |
| `datatable_exists` | Branches on `true` / `false`. |
| `datatable_query` | `rows: []map[string]any`, `count: int`. |
| `datatable_count` | `count: int`. |
| `datatable_insert` | `key: string` — inserted primary key. |
| `datatable_upsert` | `action: string` — `insert` or `update`. |
| `datatable_delete` | (no output fields beyond status). |

## Example: idempotency by primary key

```yaml
data_tables:
  - name: processed_events
    ref: processed_events
    mode: read_write

graph:
  entry: check_seen
  nodes:
    check_seen:
      type: datatable_exists
      table: processed_events
      where:
        event_id: '{{index .Event.Payload "id"}}'

    skip:
      type: end
      result: already_processed

    handle:
      type: agent
      prompt_file: nodes/handle.md

    mark_done:
      type: datatable_insert
      table: processed_events
      key: '{{index .Event.Payload "id"}}'
      row_values:
        event_id: '{{index .Event.Payload "id"}}'
        processed_at: '{{now}}'

  edges:
    - {from: check_seen, to: skip,    case: "true"}
    - {from: check_seen, to: handle,  case: "false"}
    - {from: handle,    to: mark_done}
```

## Pair with

- [`branch`](./branch) — route on `datatable_exists` verdict or `datatable_get` found / not_found.
- [`schedule_at`](../triggers#schedule_at) — write a "fire later" row, then a later trigger reads it.
