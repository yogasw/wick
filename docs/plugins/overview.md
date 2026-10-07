# Plugins overview

A wick plugin is a separate binary that wick installs, starts, and stops at
runtime. Shipping a connector, tool, job, or service as a plugin means a new
version is a plugin release — the wick binary is never rebuilt for it.

## Four kinds, one engine

| Kind | Lifetime | Who reaches it | Auth | Typical use |
|---|---|---|---|---|
| **connector** | on demand, idle-killed | the LLM via `wick_execute` | encrypted credentials from the host | API wrappers (GitHub, Loki, Notion…) |
| **job** | spawned when the job fires (cron or *Run now*), killed as soon as `Run` returns | the wick scheduler | config from the host | periodic syncs, fetchers |
| **tool** | spawned on the first request (the request waits until it is ready), idle-killed | wick users in the browser at `/tools/{key}` | wick session (checked by the host) | the cards on the home grid |
| **service** | always on, restarted with backoff (1s → 30s) | outside systems at `/x/{key}/*` | per route: `public`, `token`, `session` | A2A repeaters, webhook relays |

Which one to pick:

- The LLM should call it as an operation → **connector**.
- It runs on a schedule and ends → **job**.
- A person opens it in the browser → **tool**.
- Something outside wick must reach it at any time, or it must keep state
  between requests → **service**.

## Protocol per kind

- **connector / job** — gRPC over go-plugin. A job's `Run` streams progress
  chunks into the run history and returns the final string or error, like a
  built-in `job.RunFunc`.
- **tool / service** — the plugin serves plain HTTP on a unix socket
  (`$WICK_PLUGIN_SOCKET`); the host reverse-proxies to it. go-plugin is still
  used for the handshake and `Health`. SSE and streaming responses pass
  through (the proxy flushes per write).
- Every plugin binary answers `--dump-manifest` with its `plugin.json`
  (`kind`, version, key, configs, routes…). `wick plugin build` generates the
  manifest from the built binary, so the two never drift.

## Lifecycle

1. **Install** — upload a zip, paste a link, or add a GitHub source
   (see [Sources](./sources.md)). Every path runs the same checks: `zip_sha256`
   from the index, signature when required, then the binary `sha256` and
   `kind`/`os_arch`/`proto_version` from the manifest.
2. **Load** — one reloader watches the four kind folders (every 5 s) and
   registers what it finds; `kind` in `plugin.json` must match the folder.
3. **Run** — per kind, as in the table above. At most
   `WICK_PLUGIN_MAX_PROCS` (default 8) on-demand plugin processes run at
   once; extra requests queue for `WICK_PLUGIN_QUEUE_TIMEOUT` (default 10s).
   A tool request waits up to 10 s for a cold plugin, then gets `503`; an
   idle tool stops after `WICK_TOOL_PLUGIN_IDLE` (default 10m). A job run is
   bounded by `WICK_JOB_PLUGIN_TIMEOUT` (default 30m).
4. **Update** — a source that publishes a newer version sets
   *Update available*; an admin updates from the kebab on the plugin's detail
   page. Services restart on the new binary; tools and jobs pick it up on the
   next spawn.

## Where files live

```
<data dir>/plugins/
├── connectors/<key>/{<binary>, plugin.json}
├── jobs/<key>/…
├── tools/<key>/…
└── services/<key>/…
```

Plugin sockets live under `<data dir>/run` (override with
`WICK_PLUGIN_SOCKET_DIR`). Keep that path short: a unix socket path is capped
at 108 bytes.

## Rules that bite

- **Keys use `_`, never `-`.** The release zip is
  `<key>-<version>-<os>-<arch>.zip` and is split on `-`. A built-in tool
  `text-counter` becomes the plugin `text_counter` — its URL changes.
- The folder name under `<kind>/` must equal `Meta.Key`.
- Tools: the host wraps `c.HTML` pages in the wick layout using the
  manifest name as the title (a per-request title override is not
  supported yet).
- Services: config set on the admin page is pushed to the plugin, but there
  is no MCP/`wick_manager` operation for service config yet — use the admin
  page.

## Next

- Authoring: [connector](./authoring-connector.md) ·
  [tool](./authoring-tool.md) · [job](./authoring-job.md) ·
  [service](./authoring-service.md)
- Distribution: [index format](./index-format.md) · [sources](./sources.md) ·
  [release](./release.md)
- [Security](./security.md) · [A2A repeater walkthrough](./service-a2a.md)
