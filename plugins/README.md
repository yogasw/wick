# plugins

External plugins for [wick](https://github.com/yogasw/wick), built and released
independently of the core binary. One folder per plugin, grouped by kind.

## TODO / quickstart

- [ ] Copy `connector/_template/` → `connector/<your-name>/`
- [ ] Edit `Meta.Key` / `Meta.Name` + the operations in `connector.go`
- [ ] Set `connector/<your-name>/VERSION` (e.g. `0.1.0`)
- [ ] Build a zip: `wick plugin build <your-name> --target linux/arm64`
- [ ] Install into a running app: `<your-app> plugin install ./bin/<name>-<ver>-linux-arm64.zip`

## Layout

```
plugins/
├── go.mod                 # ONE module — every plugin shares deps + the wick version
├── connector/
│   └── _template/         # scaffold: copy this to start a new connector
│       ├── main.go        #   package main → wickplugin.Serve(mod)
│       ├── connector.go   #   the connector.Module (Meta + Operations + Configs)
│       └── VERSION        #   source of truth for this plugin's version
├── tool/                  # kind=tool — toolplugin.ServeTool; _template + example_counter
├── job/                   # kind=job — wickplugin.ServeJob; _template + example_heartbeat
└── service/               # kind=service — service.ServeService; _template + example_a2a_repeater
```

`_template` is a complete, working connector (HTTP GET + DELETE against a
configurable API) — copy it, don't start from scratch.

Each `<kind>/<name>/` is its own `package main` that calls `wickplugin.Serve`.
One `go.mod` at the root keeps every plugin on the same wick version and one
`go.sum` — add a plugin by copying a folder, not by `go mod init`.

> This is a **nested Go module inside the wick repo** (it stays here — not a
> separate repo). The repo-root `go.work` resolves `github.com/yogasw/wick` to
> the local checkout, so builds use the in-tree `pkg/plugin` even before it's in
> a published wick release. That's why `go.mod` needs **no `replace`**.

> `connector/_template` starts with `_` so Go tooling (`go build ./...`) and
> `wick plugin build --all-plugins` skip it — it's a scaffold, not a shippable
> connector. Build it directly with its explicit path when you want to smoke-test
> it: `go build ./connector/_template/`.

## Building

Build is the **production** side and runs from the `wick` dev CLI:

```bash
# one plugin, one target → one zip in ./bin
wick plugin build myconnector --target linux/arm64

# one plugin, every supported os/arch → N zips (linux/arm64 first — Termux)
wick plugin build myconnector --all

# every connector under connector/
wick plugin build --all-plugins

# only connectors whose folder changed since a ref (used by CI)
wick plugin build --changed --since origin/main

# job plugins: pick the source folder with --kind
wick plugin build --kind job example_heartbeat
```

Each build produces `bin/<name>-<version>-<goos>-<goarch>.zip` containing the
binary plus a `plugin.json` generated **from the binary** (`--dump-manifest`),
so the manifest can never drift from the code. Pass `--sign-key <path>` to sign
each manifest (ed25519; `cmd/plugin-keygen` in the wick repo mints a key).

### Job plugins

`job/<name>/` calls `wickplugin.ServeJob(job.Module{...})` instead of `Serve`.
The host installs it under `plugins/jobs/<key>/` and registers it like a
built-in job: it shows on the Jobs page, runs on its cron and from Run now, and
each run spawns the binary, calls `Run` once, then kills the process. Lines
logged with `job.Logf(ctx, ...)` and the returned markdown land in the run
history. A run is bounded by `WICK_JOB_PLUGIN_TIMEOUT` (default 30m). Start from
`job/_template/`.

### Tool plugins

`tool/<name>/` calls `toolplugin.ServeTool(tool.Module{...})` — the same
`tool.Module` (meta, `Configs`, `Register(r tool.Router)`) a built-in tool
uses, so moving a tool out of the binary only changes its `main.go`. The host
installs it under `plugins/tools/<key>/`, lists it on the home grid with a
"plugin" badge, and reverse-proxies `/tools/<key>/*` to the plugin's HTTP
server on a unix socket:

- `c.HTML(...)` pages come back as fragments and wick wraps them in its
  layout; `c.JSON` and HTMX requests pass through as-is.
- `c.User()` and `c.Cfg(...)` work as usual: the host injects the signed-in
  user as `X-Wick-User-*` headers (client-sent ones are dropped) and pushes
  config at spawn and whenever it changes.
- Only routes declared on `r.WebhookGroup(...)` answer without a login, and
  only the exact ones the manifest lists.
- The process starts on the first request (held up to 10 s, then 503), and
  stops after `WICK_TOOL_PLUGIN_IDLE` (default 10m) without traffic — never
  while a request or stream is open. `toolplugin.ServeTool(mod,
  toolplugin.KeepWarm())` keeps it running for webhooks that must answer
  fast.

Start from `tool/_template/`; `tool/example_counter/` shows a page, a JSON
endpoint, and a webhook.

### Service plugins

`service/<name>/` calls `service.ServeService(service.Module{...})`: an
always-on HTTP server exposed at `/x/<key>/*`, restarted with backoff
(1s→30s) when it dies. Each `Route` picks its auth — `service.Public`,
`service.Token` (Bearer token generated on the plugin's admin page) or
`service.Session` (signed-in wick user). `env.Callback()` gives a scoped
`WICK_PLUGIN_TOKEN` + `WICK_BASE_URL` for calling wick back, and
`Module.RemoteSource` makes the plugin a Team remote-agent source. Start from
`service/_template/`; `service/example_a2a_repeater/` is a full A2A adapter.

Full docs: `docs/plugins/` (overview, authoring per kind, index format,
sources, release, security).

## Installing (consumption side)

Installing/enabling/disabling is done by the **app** that uses the plugin, not
this dev CLI — the plugins dir and enable/disable state belong to the running
app:

```bash
<your-app> plugin install ./bin/myconnector-0.1.0-linux-arm64.zip
<your-app> plugin list
<your-app> plugin disable myconnector
<your-app> plugin enable myconnector
<your-app> plugin remove myconnector
```

A running app picks up an install/enable within a few seconds (the plugin
reloader polls the plugins dir) — no restart needed.

## Releasing

See [RELEASE.md](./RELEASE.md). Every release `<name>/v<version>` carries the
zips plus a per-release `plugins.json` (`wick plugin index`: relative urls,
`zip_sha256`, optional signature), so a wick GitHub source can install from it
directly. Plugins kept in a separate repo use the reusable workflow
`yogasw/wick/.github/workflows/plugin-release.yml@master`.

## Marketplace catalog (`plugins.json`)

`plugins.json` at the repo root is the **marketplace catalog** — the list wick
shows under "Available to install" in the connector list. It is a plain JSON file
on the default branch, fetched raw:

```
https://raw.githubusercontent.com/yogasw/wick/master/plugins/plugins.json
```

Each entry carries the connector's name/description/version and a direct
**release download URL per os/arch**:

```json
[
  { "name": "httpbin", "description": "...", "version": "0.1.0",
    "assets": {
      "linux/arm64":  "https://github.com/.../releases/download/httpbin/v0.1.0/httpbin-0.1.0-linux-arm64.zip",
      "windows/amd64": "https://github.com/.../releases/download/httpbin/v0.1.0/httpbin-0.1.0-windows-amd64.zip"
    } }
]
```

Why a raw JSON file and not the GitHub Releases API:

- **No rate limit / no token** — a raw file fetch isn't the API. The API caps
  unauthenticated calls at 60/hr per IP; the catalog is just a file.
- **Listing is cheap** — wick reads metadata only. The binary is pulled from the
  `assets` URL **only when the user clicks Download**.
- **Curated** — the file is the source of truth for what's offered, editable by
  hand or generated by CI on release.

Wick caches the catalog (15 min, ETag-aware) and merges it into the connector
list: built-in connectors and already-downloaded plugins render as normal cards;
catalog entries that aren't installed yet show under "Available to install" with
a Download button. Override the catalog URL with `WICK_PLUGIN_CATALOG`.

**On release**, add or bump the connector's entry in `plugins.json` (point
`version` + `assets` at the new release) so it appears/updates in the
marketplace. (Automating this bump from the release workflow is a TODO.)
