---
outline: deep
---

# HTTP / REST

`httprest` is the generic REST client. One instance wraps a single API base URL and auth header; operations cover the five standard HTTP verbs (`GET`, `POST`, `PUT`, `PATCH`, `DELETE`).

Reach for it when an LLM needs to call a JSON API you have not wrapped in a typed connector yet. The flip side: input/output are untyped string blobs, so the LLM has to know the API's shape from the prompt or its own knowledge. For anything you call repeatedly, write a typed connector instead — see [Connector Module](/guide/connector-module).

| | |
|---|---|
| **Source** | [`internal/connectors/httprest/`](https://github.com/yogasw/wick/tree/master/internal/connectors/httprest) |
| **Key** | `httprest` |
| **Icon** | 🌐 |
| **Tier** | builtin (every wick app) |

## Configs

| Field | Type | Required | Notes |
|---|---|---|---|
| `BaseURL` | URL | ✅ | Base URL of the target API. Example: `https://api.example.com/v1`. |
| `AuthHeader` | string | | Header name used for authentication (e.g. `Authorization`, `X-API-Key`). Empty = skip auth. |
| `AuthValue` | secret | | Value for the auth header (e.g. `Bearer mytoken`). |
| `TimeoutSecs` | int | | Per-request timeout in seconds. Default 30. |

`AuthValue` is marked `secret` so the encrypted-fields layer round-trips it as a `wick_enc_` token whenever it leaves wick — see [Encrypted Fields](/reference/encrypted-fields).

## Operations

| Op | Destructive | Input | Description |
|---|---|---|---|
| `get` | no | `path`, `query` | GET `{base_url}/{path}` with optional query string. |
| `post` | yes | `path`, `body`, `content_type` | POST a JSON (or text) body. Default `Content-Type: application/json`. |
| `put` | yes | `path`, `body`, `content_type` | Full-replacement PUT. |
| `patch` | yes | `path`, `body`, `content_type` | Partial-update PATCH. |
| `delete` | yes | `path` | DELETE `{base_url}/{path}`. |

Every non-GET op is registered with `connector.OpDestructive` so it is **disabled by default** on every new row — the admin opts in explicitly per (row, op) at `/manager/connectors/httprest/{id}`.

The handlers are deliberately thin (see [`connector.go`](https://github.com/yogasw/wick/blob/master/internal/connectors/httprest/connector.go)): build the URL via `service.go`, dispatch via `repo.go` which always uses `http.NewRequestWithContext` so cancellation propagates correctly.

## See also

- [Connector Module](/guide/connector-module) — module contract, file layout, the `wick:"..."` tag grammar.
- [MCP for LLMs](/guide/mcp) — how a remote LLM discovers and calls these ops.
