---
outline: deep
---

# Built-in Connectors

Wick ships a set of **built-in connectors** that every downstream app inherits at boot. They use the same module shape as user-registered connectors ([Connector Module](/guide/connector-module)) and surface through MCP via the meta-tool pattern ([MCP for LLMs](/guide/mcp)).

A built-in is just a regular connector that calls `connectors.Register(...)` (or sits inside `connectors.RegisterBuiltins()` / `RegisterLabSamples()`) instead of waiting for `app.RegisterConnector` in your `main.go`. Once registered, it follows the same lifecycle:

- Auto-seeded as one empty row on first boot.
- Listed at `/manager/connectors/<key>` for credentials + per-op enable.
- Reachable from an LLM via `wick_list` / `wick_get` / `wick_execute`.

## Index

| Connector | Key | Purpose | Default tier |
|---|---|---|---|
| [HTTP / REST](./httprest) | `httprest` | Generic JSON REST client — GET / POST / PUT / PATCH / DELETE any path. Useful when you want an LLM to call an API you haven't wrapped in a typed connector yet. | builtin |
| [GitHub](./github) | `github` | List repos / issues / PRs, read file contents, create issues, post comments. | builtin |
| [Slack](./slack) | `slack` | Read channels, threads, users; send / edit / delete messages; manage reactions. OAuth credentials supported on the row. | builtin |
| [Wick Manager](./wickmanager) | `wickmanager` | Read and edit wick's own apps / jobs / tools / connectors / tray lifecycle. For asking the LLM to inspect or tweak wick itself, not third-party APIs. | runtime |
| [Workflow](./workflow) | `workflow` | Create, edit, test, simulate, and run workflows over MCP — the LLM-facing surface for the [Workflows](/workflow/) feature. | runtime |
| [CRUD CRUD](./crudcrud) | `crudcrud` | Demo connector wrapping the public crudcrud.com sandbox. Ships with [`cmd/lab`](https://github.com/yogasw/wick/tree/master/cmd/lab) only — useful as a copy-paste starting point. | lab sample |

**Tiers:**

- **builtin** — registered by [`connectors.RegisterBuiltins()`](https://github.com/yogasw/wick/blob/master/internal/connectors/registry.go); every downstream wick app gets it for free.
- **runtime** — registered inline at boot in [`internal/pkg/api/server.go`](https://github.com/yogasw/wick/blob/master/internal/pkg/api/server.go) because the operations need runtime services (configsSvc, jobsSvc, workflow engine, …) that only exist mid-boot.
- **lab sample** — `connectors.RegisterLabSamples()` in `cmd/lab` only. Not present in production binaries.

## Tag visibility

Every built-in is seeded with `tags.Connector` so it appears under the **Connector** group on the home page. Visibility within authenticated users follows the same tag-filter rule as user-registered connectors — see [Connector Module ▶ Sharing connectors with tags](/guide/connector-module#sharing-connectors-with-tags).

The Wick Manager and Workflow connectors operate against in-process wick state (configs, jobs, workflow folders) and respect each subject's own access control on top of the connector-level tag filter:

- **Wick Manager** — every `app_*` and `system_*` op is admin-only. `job_*` / `tool_*` / `connector_*` ops are per-resource-tag-filtered (admin sees all; non-admin sees only resources whose tags grant access).
- **Workflow** — every `workflow_*` mutation requires admin; read ops are visible to any authenticated caller.

## See also

- [Connector Module](/guide/connector-module) — module contract, file layout, registration, `wick:"..."` tag grammar.
- [MCP for LLMs](/guide/mcp) — transport, `wick_list` / `wick_get` / `wick_execute` flow.
- [Connector API reference](/reference/connector-api) — `pkg/connector` exported types.
- [Encrypted Fields](/reference/encrypted-fields) — how `secret`-tagged credentials are stored and round-tripped.
