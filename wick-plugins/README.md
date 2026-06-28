# wick-plugins

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
wick-plugins/
├── go.mod                 # ONE module — every plugin shares deps + the wick version
├── connector/
│   └── _template/         # scaffold: copy this to start a new connector
│       ├── main.go        #   package main → wickplugin.Serve(mod)
│       ├── connector.go   #   the connector.Module (Meta + Operations + Configs)
│       └── VERSION        #   source of truth for this plugin's version
├── tool/                  # (later) kind=tool — same build flow
└── job/                   # (later) kind=job — same build flow
```

`_template` is a complete, working connector (HTTP GET + DELETE against a
configurable API) — copy it, don't start from scratch.

Each `<kind>/<name>/` is its own `package main` that calls `wickplugin.Serve`.
One `go.mod` at the root keeps every plugin on the same wick version and one
`go.sum` — add a plugin by copying a folder, not by `go mod init`.

> **While this lives inside the wick repo** (not yet extracted), the repo-root
> `go.work` resolves `github.com/yogasw/wick` to the local checkout, so builds
> use the in-tree `pkg/plugin` even though it isn't in a published wick release
> yet. That's why `go.mod` has **no `replace`** — it's already in its
> extract-ready shape. When `pkg/plugin` ships in a wick release, bump the
> `require` to that version; nothing else changes.

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

# other kinds (later): pick the source folder with --kind
wick plugin build --kind tool mytool
```

Each build produces `bin/<name>-<version>-<goos>-<goarch>.zip` containing the
binary plus a `plugin.json` generated **from the binary** (`--dump-manifest`),
so the manifest can never drift from the code. Pass `--sign-key <path>` to sign
each manifest (ed25519; `cmd/plugin-keygen` in the wick repo mints a key).

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

Push to `main`; the CI workflow (`.github/workflows/release.yml`) builds **only
the connectors whose folder changed** (one zip per os/arch) and attaches them to
a GitHub Release tagged `<name>/v<version>`. It does NOT rebuild everything on
every push.

## Marketplace catalog (`plugins.json`)

`plugins.json` at the repo root is the **marketplace catalog** — the list wick
shows under "Available to install" in the connector list. It is a plain JSON file
on the default branch, fetched raw:

```
https://raw.githubusercontent.com/yogasw/wick-plugins/master/plugins.json
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
