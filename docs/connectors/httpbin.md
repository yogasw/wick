---
outline: deep
---

# HTTPBin

`httpbin` is a **sample plugin connector** that hits [httpbin.org](https://httpbin.org) — GET with a query string, POST with a body, and arbitrary status-code echo. No credentials needed. It exists as a minimal, working end-to-end example of the plugin path: copy it as a starting point when building your own connector plugin.

| | |
|---|---|
| **Source** | [`plugins/connector/httpbin/`](https://github.com/yogasw/wick/tree/master/plugins/connector/httpbin) |
| **Key** | `httpbin` |
| **Icon** | 🧪 |
| **Tier** | plugin (sample) — install with `<app> plugin install httpbin` |

> Ships as a plugin, not compiled into the wick binary:
>
> ```bash
> <app> plugin install httpbin
> ```
>
> See [Connector Plugins](/guide/connector-plugins) for the install flow, and [Connector Module](/guide/connector-module) for the module contract this sample demonstrates.

## Configs

| Field | Type | Required | Notes |
|---|---|---|---|
| `BaseURL` | URL | — | httpbin base URL. Default `https://httpbin.org`. Point it at a self-hosted httpbin if you have one. |

The default means the connector works with **zero setup** — install, enable, run.

## Operations

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `get` | no | `query` | `GET {base_url}/get?{query}` — returns httpbin's echo of the request. `query` is an optional raw query string like `foo=bar&x=1`. |
| `post` | no | `body` | `POST {base_url}/post` with a raw body — returns httpbin's echo. JSON bodies come back parsed under `json`. |
| `status` | no | `code` | `GET {base_url}/status/{code}` — asks httpbin to return a specific HTTP status code. Returns `{ requested, status }`. |

Together the three exercise GET (with query), POST (with body), and arbitrary status codes — enough to validate the whole plugin path end to end.

## Using it as a template

httpbin is the smallest complete connector in the repo, so it's the fastest thing to copy when scaffolding your own:

- **One `Config` field** with a `default=` so the connector needs no setup.
- **Three ops in one `connector.Cat`** covering the common HTTP verbs.
- **Response handling** that returns parsed JSON when the body is JSON and the raw string otherwise (`doJSON`).
- Every request built with `c.Context()` so it's cancellable.

Read `plugins/connector/httpbin/connector.go` alongside the [Connector Module](/guide/connector-module) guide to see how `Meta`, `Configs`, and `Operations` map onto the module contract.

## See also

- [CRUD CRUD](./crudcrud) — the other sample connector (in-tree `cmd/lab` only), wrapping the crudcrud.com sandbox.
- [Connector Module](/guide/connector-module) — module contract, `wick:"..."` tag grammar.
- [Connector Plugins](/guide/connector-plugins) — install / enable / disable flow.
