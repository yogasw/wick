---
name: wick-connectors
description: Use when the user asks to look something up or act on an external system through wick — check a ticket, query a database, read logs, search Slack, call an API — or asks which connectors are available. Covers the list → drill → schema → execute discovery cycle over MCP, session workspaces for throwaway credentials, and what to do when a connector reports needs_setup.
---

# Using wick connectors

A **connector** is wick's bridge to an external system (Slack, Notion, a database, an HTTP API). Each connector instance holds its own credentials and exposes a set of **operations**. You reach them over MCP.

Never guess a `tool_id` or a parameter name. Discovery is cheap; a wrong call is not.

## The discovery cycle

Four tools, used in order:

| Tool | Purpose |
|---|---|
| `wick_list` | Every connector instance visible to you. Returns `id`, `connector`, `description`, `total_tools`, `status`, `kind`. |
| `wick_search` | Substring search over label, name, description. Faster when you know roughly what you want. |
| `wick_get` | Operations for one connector, then the input schema for one operation. |
| `wick_execute` | Run an operation by `tool_id` + `params`. |

`wick_list` deliberately does **not** return per-operation input schemas — you only pay that token cost when you commit to a specific operation via `wick_get`.

```
wick_list()                          → find the connector, note its id
wick_get({id})                       → see its operations
wick_get({tool_id})                  → input schema for the one you want
wick_execute({tool_id, params})      → run it
```

Shortcut when you already know roughly what you need:

```
wick_search({query: "loki query"})   → matched tool_id
wick_execute({tool_id, params})
```

## Reading the list

Entries come in two kinds, and the difference is *whose identity you act as*:

- `kind: "connector"` — shared or bot credentials.
- `kind: "account"` — a specific user's connected OAuth account.

When several users have connected personal accounts to one instance, you see one `connector` entry plus one `account` entry per user. Pass a composite `connectorID/accountID` to `wick_get` and the returned `tool_id`s carry an `@accountID` suffix; `wick_execute` then injects that account's token automatically.

Pick `account` when the action should be attributable to that person, `connector` when it is the system acting.

## Status: needs_setup

A connector whose required config fields are empty reports `needs_setup`. It appears in the list but cannot execute.

Do not try to work around this by guessing credentials or calling the op anyway. Tell the user which connector needs configuring and let them fill it in — the config UI is the only place credentials are entered.

## Session workspaces

`wick_session_workspace` creates throwaway connector instances scoped to one session — useful to point at staging, or to use a different key without touching the shared instance.

The division of labour matters: **you create the blank instance, the user fills in the config through a UI form.** You never see the values. The instance shows up in `wick_list` for that session only and dies with the session.

## Batch execution

`wick_execute` accepts a `calls` array instead of a single `tool_id`/`params`, running several operations in one round-trip. Use it when the calls are independent — it saves latency, not correctness. Sequential work that depends on earlier results still needs separate calls.

## Access is re-checked every call

`wick_execute` and `wick_get` re-validate visibility and per-operation enable state on every call; they never trust a list-time cache. If a user's tag is removed or an op is disabled, the very next call fails. Treat an authorization error as current truth, not a glitch to retry.

## The Wick Manager exception

The `wickmanager` connector is surfaced differently: its operations appear directly in `tools/list` as `wick_manager_<op>` (e.g. `wick_manager_app_list`, `wick_manager_job_run_now`). Call them without the discovery cycle. It is excluded from `wick_list` / `wick_search` to avoid double-exposure.

## Encrypted values

Some connector responses carry `wick_enc_<...>` tokens. These are ciphertext handles — you can pass them to another operation, but you cannot read them and must not try. That is the point: credentials flow between operations without ever entering the conversation.
