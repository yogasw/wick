---
outline: deep
---

# `dataset_*`

Datasets are wick's in-process state store for workflows. One dataset = one named rowset with a typed key/value or rowset shape, bound to the workflow's `datasets:` block. Seven node types share one executor:

| Type | Operation |
|---|---|
| `dataset_get` | Load one row by primary key. Branches on `found` / `not_found`. |
| `dataset_exists` | Check whether any row matches. Branches on `true` / `false`. |
| `dataset_query` | Multi-row search with `where` / `order_by` / `limit`. |
| `dataset_count` | Count rows matching `where` without loading them. |
| `dataset_insert` | Insert a new row; fails on PK conflict. |
| `dataset_upsert` | Insert or update by primary key. Returns `action: insert\|update`. |
| `dataset_delete` | Delete rows matching `where`. |

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/dataset.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/dataset.go) |
| **Engine** | In-memory by default — see the [datasets design doc](https://github.com/yogasw/wick/blob/master/internal/docs/workflow/12-datasets.md) for the Postgres-backed roadmap. |

## Schema (per type)

### `dataset_get`

| Field | Required | Notes |
|---|---|---|
| `dataset` | ✅ | Dataset name from the workflow's `datasets:` block. |
| `key` | ✅ | Primary key value (templated). |

### `dataset_exists`, `dataset_delete`

| Field | Required | Notes |
|---|---|---|
| `dataset` | ✅ | |
| `where` | ✅ | YAML map of field equality filters (templated). |

### `dataset_query`

| Field | Required | Notes |
|---|---|---|
| `dataset` | ✅ | |
| `where` | | Equality filters. |
| `order_by` | | Column name (prefix `-` for DESC). |
| `limit` | | Row cap. |
| `offset` | | Skip rows. |

### `dataset_count`

| Field | Required | Notes |
|---|---|---|
| `dataset` | ✅ | |
| `where` | | Same shape as `dataset_query`. |

### `dataset_insert`, `dataset_upsert`

| Field | Required | Notes |
|---|---|---|
| `dataset` | ✅ | |
| `key` | ✅ | PK value(s) (templated). |
| `row_values` | ✅ | Column → value map (templated). |

## Output

| Op | Fields |
|---|---|
| `dataset_get` | `row: map[string]any` — nil if `not_found` branch taken. |
| `dataset_exists` | Branches on `true` / `false`. |
| `dataset_query` | `rows: []map[string]any`, `count: int`. |
| `dataset_count` | `count: int`. |
| `dataset_insert` | `key: string` — inserted primary key. |
| `dataset_upsert` | `action: string` — `insert` or `update`. |
| `dataset_delete` | (no output fields beyond status). |

## Example: idempotency by primary key

```yaml
datasets:
  - name: processed_events
    key: event_id
    columns: [event_id, processed_at]

graph:
  entry: check_seen
  nodes:
    check_seen:
      type: dataset_exists
      dataset: processed_events
      where:
        event_id: '{{index .Event.Payload "id"}}'

    skip:
      type: end
      result: already_processed

    handle:
      type: agent
      prompt_file: nodes/handle.md

    mark_done:
      type: dataset_insert
      dataset: processed_events
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

- [`branch`](./branch) — route on `dataset_exists` verdict or `dataset_get` found / not_found.
- [`schedule_at`](../triggers#schedule_at) — write a "fire later" row, then a later trigger reads it.
