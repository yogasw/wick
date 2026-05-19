---
outline: deep
---

# Wick Manager

`wickmanager` exposes wick's **own management plane** — apps / jobs / tools / connectors / tray lifecycle — as a connector. Reach for it when the LLM should be talking *about wick*, not third-party APIs: list jobs, toggle a connector config, restart the worker, regenerate the session secret.

| | |
|---|---|
| **Source** | [`internal/connectors/wickmanager/`](https://github.com/yogasw/wick/tree/master/internal/connectors/wickmanager) |
| **Key** | `wickmanager` |
| **Icon** | 🛠 |
| **Tier** | runtime (registered inline at boot once `configsSvc` / `jobsSvc` / … are ready) |
| **Fixed** | ✅ — single row, auto-seeded by `Service.Bootstrap` |
| **Default tags** | `tags.Connector`, `tags.System` — hidden from non-admin users by default |

::: warning Why a connector, not a bespoke MCP surface
Every other connector already gets discovery (`wick_list`, `wick_get`, `wick_search`), per-instance admin pages, tag-based access control, encrypted-fields support, and `connector_runs` audit. Reusing the contract here avoids a parallel MCP code path. The trade-off: it shares the destructive-opt-in model, so high-impact ops (`app_regenerate_config`, `system_server_stop`, …) need explicit per-op enable on the row.
:::

## Configs

Intentionally empty (`type Configs struct{}`). Wick Manager talks to in-process services, not an external API — the empty struct exists so the admin form renders **"no config required"** rather than nothing.

## Access control

Single source of truth for **who-can-call-what** is the "Akses control — full per-op rule" table in `internal/docs/plan_wickmanager.md`. The handlers mirror it through dedicated gate helpers in [`access.go`](https://github.com/yogasw/wick/blob/master/internal/connectors/wickmanager/access.go):

- `requireAdmin` — every `app_*` op + every `system_*` op.
- `requireJobAccess` / `requireToolAccess` / `requireConnectorAccess` — `*_get`, `*_set_config`, `*_list_runs`, `*_run_now` operate per-resource. Admin sees all; non-admin sees only the resources their tags grant access to.
- `requireTray` — every `system_*` op also gates on "wick was launched via the system tray" because those ops manipulate the local process lifecycle.

Adding a new op without the matching gate helper is a security hole — the table is the contract.

## Operations

### `app_*` — app variables (admin only)

| Op | Destructive | Notes |
|---|---|---|
| `app_list` | no | All app-level variables — `app_url`, `session_secret`, `encryption_key`, … Secret values masked. |
| `app_get_config` | no | One row by key. |
| `app_set_config` | no | Update one. Rejects rows with `is_locked=true`. |
| `app_regenerate_config` | **yes** | Regenerate a regenerate-able row (e.g. `session_secret`). High-impact — regenerating `session_secret` logs out other admins. |

### `job_*` — background jobs (per-job tag-filtered)

| Op | Destructive | Notes |
|---|---|---|
| `job_list` | no | Tag-filtered list. |
| `job_get` | no | Meta + configs, secrets masked. |
| `job_set_config` | no | Update one config value. **More permissive than the UI** — UI restricts edit to admin; MCP lets any caller with tag access edit. |
| `job_set_schedule` | no | Cron expression + enabled + max_runs cap. |
| `job_run_now` | no | Out-of-cycle run. Errors if already running or `max_runs` reached. Returns the new run id immediately. |
| `job_get_run` | no | Status + result of one run. |
| `job_list_runs` | no | Recent runs, newest first. |

### `tool_*` — UI modules (per-tool tag-filtered)

| Op | Destructive | Notes |
|---|---|---|
| `tool_list` | no | Tag-filtered. |
| `tool_get` | no | Meta + configs. |
| `tool_set_config` | no | Update one config value. Same MCP-permissive vs UI difference as `job_set_config`. |

### `connector_*` — connector instances (per-connector tag-filtered)

| Op | Destructive | Notes |
|---|---|---|
| `connector_list` | no | Tag-filtered list with `status` = `ready` / `needs_setup`. |
| `connector_get` | no | Meta + configs + operations. |
| `connector_set_config` | no | Update one config field. Same MCP-permissive vs UI difference. |

### `system_*` — process lifecycle (admin + tray-only)

| Op | Destructive | Notes |
|---|---|---|
| `system_status` | no | `{server_running, server_port, worker_running, run_mode}`. |
| `system_server_start` | no | Start the HTTP server in this tray process. |
| `system_server_stop` | **yes** | Stop the HTTP server. |
| `system_worker_start` | no | Start the background worker. |
| `system_worker_stop` | **yes** | Stop the background worker. |
| `system_prefs_get` | no | Read `~/.<appName>/config.json` (auto-start flags, port, retention, …). |
| `system_prefs_set` | no | PATCH-merge tray preferences — only fields present in the input are updated. |

`system_*` ops are unavailable when wick is launched standalone (`wick server`). They only work when the binary is running under the tray supervisor that owns the server + worker child processes.

## Audit

Every op routes through `audit.go`'s deferred-elapsed logger which emits a structured line to `mcp.log` via `processctl.MCPLogger()`. Combined with `connector_runs` (every connector op writes one row), the manager surface is fully traceable — admin can replay "who toggled this job last week" without grep.

## See also

- [Connector Module](/guide/connector-module) — the contract Wick Manager reuses.
- [MCP for LLMs](/guide/mcp) — meta-tool pattern Wick Manager's ops surface through.
- [Background Job](/guide/job-module) — what `job_*` operates on.
- [Tool Module](/guide/tool-module) — what `tool_*` operates on.
- [Desktop Tray](/guide/desktop-tray) — what `system_*` operates on.
