---
outline: deep
---

# `db_query`

Parameterized SQL query against a configured DSN.

| | |
|---|---|
| **Source** | [`internal/agents/workflow/nodes/db_query.go`](https://github.com/yogasw/wick/blob/master/internal/agents/workflow/nodes/db_query.go) |
| **When to use** | Reading from an external user database — Postgres, SQLite. |
| **Supported drivers** | `postgres://`, `sqlite:`, `file:` |

## Schema

| Field | Type | Required | Notes |
|---|---|---|---|
| `database` | string | ✅ | Env key whose value is the DSN. Stored in the workflow's `env:` block. |
| `query` | textarea (template) | ✅ | SQL with `$1` / `$2` / … placeholders (Postgres style — sqlite driver normalises). |
| `params` | YAML list (templated) | | Positional params for the placeholders. Each value rendered as a Go template. |
| `timeout_sec` | int | | Per-call timeout. |

## Output

| Field | Type | What |
|---|---|---|
| `rows` | `[]map[string]any` | One entry per row, column name → cell value. |
| `row_count` | int | Same as `len(rows)`. |
| `columns` | `[]string` | Column names in order. |

## Example

```yaml
env:
  - key: USERS_DB
    desc: Read-only DSN to the users database
    secret: true

graph:
  nodes:
    lookup_user:
      type: db_query
      database: USERS_DB
      query: |
        SELECT id, email, plan, created_at
        FROM users
        WHERE id = $1
      params:
        - '{{index .Event.Payload "user_id"}}'
```

Downstream nodes reach <code v-pre>{{index (index .Node.lookup_user.rows 0) "plan"}}</code> (or <code v-pre>{{.Node.lookup_user.row_count}}</code> for a quick existence check).

## Read-only, by convention

The connector framework's `OpDestructive` annotation has no analogue here — wick doesn't parse the SQL. Use a **read-only DSN** when wiring up the env var; if you genuinely need writes, scope the credential and confirm it's intentional.

## Pair with

- [`branch`](./branch) — route on `row_count` for "found vs not found" flows.
- [`transform`](./transform) — reshape `rows` before downstream nodes.
