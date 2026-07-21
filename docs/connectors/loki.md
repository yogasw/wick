---
outline: deep
---

# Loki

`loki` queries logs and discovers labels in a [Grafana Loki](https://grafana.com/oss/loki/) instance via **LogQL** — the server-log side of an investigation. It pairs with the [Phoenix](./phoenix) connector (LLM spans) for full-stack debugging: Phoenix tells you *what the model did*, Loki tells you *what the server logged around it*.

One instance = one Grafana base URL + auth + a chosen org and Loki datasource. All traffic goes through Grafana (not Loki directly), so the same credentials that log you into Grafana drive the connector.

| | |
|---|---|
| **Source** | [`plugins/connector/loki/`](https://github.com/yogasw/wick/tree/master/plugins/connector/loki) |
| **Key** | `loki` |
| **Icon** | 🪵 |
| **Tier** | plugin — install with `<app> plugin install loki` |

> This connector ships as a plugin, not compiled into the wick binary:
>
> ```bash
> <app> plugin install loki
> ```
>
> See [Connector Plugins](/guide/connector-plugins) for the full install flow.

## Configs

Fields are grouped in the manager form. Fill **Connection** and **Authentication** first — the org and datasource pickers below read live from Grafana using those.

| Field | Type | Required | Notes |
|---|---|---|---|
| `BaseURL` | URL | ✅ | Grafana base URL, e.g. `https://loki.abc.com`. Scheme + host only. |
| `Status` | html widget | — | Live connection status + Grafana version, probed from `GET /api/health`. Read-only; fill the base URL first. |
| `AuthMode` | dropdown `basic` \| `token` | ✅ | `basic` = Grafana username + password. `token` = Bearer API key (Service Account). Default `basic`. |
| `Username` | text | when `basic` | Grafana username. |
| `Password` | secret | when `basic` | Grafana password. |
| `Token` | secret | when `token` | Grafana Service Account token. |
| `OrgID` | html picker | ✅ | Pick the Grafana org — stored as the numeric id and sent as the `X-Grafana-Org-Id` header. Fill Connection + Authentication first, then open the list. |
| `DatasourceUID` | html picker | ✅ | Pick a Loki datasource (scoped to the chosen org). Pick the org first, then reopen this list. |

The org / datasource fields are **pickers**, not free text: the manager calls a backing op live and you click the row you want. `OrgID` lists what the auth can reach (`GET /api/user/orgs`); `DatasourceUID` lists the Loki datasources in that org (`GET /api/datasources`, filtered to `type=loki`).

## Operations

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `query` | no | `query`, `start`, `end`, `limit`, `direction` | Run a LogQL query over a time range. Returns a `count` and a flat `entries` list — each entry has an RFC3339 `timestamp`, a `labels` map, and the `line` text. Empty `entries` means no matches. |
| `labels` | no | `start`, `end` | List all label names indexed by Loki. Use it to discover labels before building a stream selector. |
| `label_values` | no | `label`, `start`, `end` | List all values for one label name. Combine with `labels` to build precise selectors like `{app="api", env="prod"}`. |

### `query` inputs

| Input | Required | Notes |
|---|---|---|
| `query` | ✅ | LogQL query, e.g. `{app="api"} |= "error"`. |
| `start` | — | RFC3339 or Unix nanoseconds. Default: 1 hour ago. |
| `end` | — | RFC3339 or Unix nanoseconds. Default: now. |
| `limit` | — | Max log lines. Default `100`, capped at `5000`. |
| `direction` | — | `backward` = newest first (default), `forward` = oldest first. |

`query` runs through Grafana's **datasource query API** (`POST /api/ds/query`), the same call the Grafana Explore UI makes — not the Loki resource proxy, which 500s on range queries. The response comes back as Grafana's DataFrame format and is flattened into the `entries` list for you.

### Label discovery inputs

`labels` and `label_values` both take an optional `start` / `end` window (default: **last 6 hours** → now). The window matters: some Loki versions reject a range-less label query with a 500, and labels that only appear intermittently won't surface without a wide enough lookback. Widen `start` to inspect a past period or catch rare labels.

## Typical flow

1. **Discover.** Call `labels` to see what's indexed, then `label_values("app")` (etc.) to see the values you can select on.
2. **Query.** Build a LogQL selector from those and call `query` — e.g. `{app="api", env="prod"} |= "timeout"` over the last hour.
3. **Cross-reference.** Pair a Loki `query` with a [Phoenix](./phoenix) span lookup for the same `room_id` / `app_id` to line up server logs against the model's decisions.

## Quirks worth knowing

- All calls go through **Grafana**, not Loki directly — the base URL is Grafana's, and requests carry the `X-Grafana-Org-Id` header for the selected org.
- `query` uses `POST /api/ds/query` (the DataFrame API); `labels` / `label_values` use the Grafana datasource **resource** proxy. They parse differently under the hood but return the same clean shapes.
- Label queries always send a time window (6h default) because a range-less query 500s on some Loki versions — this makes discovery behave the same across versions.
- Timestamps in inputs accept RFC3339 *or* a raw Unix-nanosecond string; a value that's all digits and ≥15 chars is treated as nanoseconds.
- The org picker does **not** send the org header — the list itself is what selects the org. Pick the org before opening the datasource list, since datasources are org-scoped.

## See also

- [Phoenix](./phoenix) — LLM-span side of an investigation; pairs with Loki for full-stack debugging.
- [Connector Module](/guide/connector-module) — module contract, `wick:"..."` tag grammar.
- [Connector Plugins](/guide/connector-plugins) — install / enable / disable flow.
