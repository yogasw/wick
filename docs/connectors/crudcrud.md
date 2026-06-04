---
outline: deep
---

# CRUD CRUD

`crudcrud` wraps the [crudcrud.com](https://crudcrud.com) REST sandbox — a free, throwaway JSON store that exposes a generic `/<resource>/<id>` shape. One instance = one sandbox endpoint.

The point of shipping it is **not** the sandbox itself. It's the canonical three-file example for building your own connector: `connector.go` + `service.go` + `repo.go`, five operations, one destructive. When the LLM (or you) asks "where do I look for a connector template?", point at this one.

| | |
|---|---|
| **Source** | [`internal/connectors/crudcrud/`](https://github.com/yogasw/wick/tree/master/internal/connectors/crudcrud) |
| **Key** | `crudcrud` |
| **Icon** | 🧪 |
| **Tier** | lab sample — registered only by [`cmd/lab`](https://github.com/yogasw/wick/tree/master/cmd/lab), not by production binaries |

## When you'd touch it

| Want | Do this |
|---|---|
| Build a new connector | Copy `internal/connectors/crudcrud/` → rename → swap the upstream API. See [Connector Module ▶ File structure](/guide/connector-module#file-structure). |
| Smoke-test the connector framework end-to-end | Boot `cmd/lab`, claim a free endpoint at crudcrud.com, paste it into the row's `BaseURL`, run all five ops from the test panel. |
| Demo connector + workflow integration without a real API | Same — `crudcrud` is happy to hold any shape you invent. |

## Configs

| Field | Type | Required | Notes |
|---|---|---|---|
| `BaseURL` | URL | ✅ | Unique crudcrud endpoint. Example: `https://crudcrud.com/api/abcdef0123456789`. |
| `SecretWord` | secret | | Reserved sensitive field — kept around as a `secret` example, currently unused. |
| `SecretWords` | kvlist | | One keyword per row. Any keyword that appears in a `create` / `get` / `update` response is replaced with a `wick_enc_` token before the response leaves wick. |
| `SecretWordsIgnoreCase` | bool | | When checked, keyword matching folds case (`Admin == admin`) and all variants share one token. |

The `SecretWords` machinery is an example of the dynamic-masking pattern — `c.Mask` / `c.MaskIgnoreCase` ([Encrypted Fields](/reference/encrypted-fields)) applied to upstream response data the static schema can't know about up front.

## Operations

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `create` | no | `resource`, `body` | POST a JSON document under `{resource}`. crudcrud auto-generates the `_id`. |
| `list` | no | `resource` | GET every document in `{resource}`. Empty array if the collection is fresh. |
| `get` | no | `resource`, `id` | Fetch one document by `_id`. |
| `update` | no | `resource`, `id`, `body` | PUT — **full replacement**, not patch. |
| `delete` | yes | `resource`, `id` | DELETE the document. Cannot be undone (no soft-delete in crudcrud). |

`create` / `update` are idempotent in shape but mutate state — they're still `connector.Op`, not destructive. Only `delete` is marked destructive so the admin opts in explicitly.

## See also

- [Connector Module](/guide/connector-module) — the contract `crudcrud` is the canonical example of.
- [HTTP / REST](./httprest) — generic untyped variant when you don't want to wrap an API at all.
- [Encrypted Fields](/reference/encrypted-fields) — the layer behind `SecretWords` dynamic masking.
