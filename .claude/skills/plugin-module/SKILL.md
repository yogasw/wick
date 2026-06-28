---
name: plugin-module
description: Use when building, packaging, or releasing a connector (or later a tool/job) as an EXTERNAL wick PLUGIN — a separate binary in the plugins monorepo that wick downloads and runs over gRPC, instead of an in-tree module compiled into wick. Covers the plugins repo layout (one folder per plugin under connector/<key>/), the key==folder one-identity rule, `wick plugin build` (zip output, --kind, --target list / --all, BUILD_TARGETS, signing), `wick plugin catalog` (regenerate plugins.json from releases), DefaultTags categorization via plugins/tags, install/enable/disable via the app, the install dir resolved from appname.Resolve(), and the dispatch-driven release CI flow. The MODULE contract itself (Meta, Configs, Operations, Ctx, wick:"..." tags) is identical to in-tree modules — defer to the connector-module / tool-module skills for that; this skill is ONLY the plugin packaging + shipping layer on top.
allowed-tools: Read, Grep, Glob, Edit, Write, Bash
paths:
  - "plugins/**"
  - "cmd/cli/plugin.go"
  - "cmd/cli/plugin_manifest.go"
  - "cmd/cli/plugin_cosign.go"
  - "pkg/plugin/**"
  - "internal/connectors/plugin/**"
  - "internal/manager/plugins_api.go"
  - "app/plugin_cmd.go"
  - ".github/workflows/release-plugins.yml"
  - ".vscode/tasks.json"
---

# Plugin Module — connectors (and later tools/jobs) shipped as external plugins

> **Scope:** this skill is the **packaging + shipping** layer. A plugin's *module*
> — `Meta`, `Configs`, `Operations`, `Ctx`, the `wick:"..."` tag grammar,
> destructive-opt-in, `http.NewRequestWithContext`, typed responses — is **exactly
> the same** as an in-tree connector. For all of that, use the **`connector-module`**
> skill (or `tool-module` for tools). This skill covers only what's different when
> the module is a separate binary wick downloads and runs, not code compiled into
> wick.

## Mental model: in-tree module vs plugin

| | In-tree (connector-module skill) | Plugin (this skill) |
|---|---|---|
| Lives in | `internal/connectors/<name>/` | `plugins/connector/<key>/` (nested module in the wick repo) |
| Registered by | `RegisterBuiltins()` at compile time | downloaded → scanned from `~/.<appName>/plugins/connectors/<key>/` at runtime |
| Runs | in the wick process (function call) | own subprocess, gRPC over UDS (`hashicorp/go-plugin`) |
| `main()` | none — it's a package | `package main` → `wickplugin.Serve(Module())` |
| Ships as | part of the wick binary | `<key>-<ver>-<os>-<arch>.zip` (binary + `plugin.json`) |

The **module value is identical** — a plugin's `connector.Module` is built the same
way. The only code difference is the `main.go` wrapper and that it's a separate Go
module. So: write the connector exactly as connector-module teaches, then wrap +
package per below.

## The plugins repo

```
plugins/
├── go.mod                 # ONE module; repo-root go.work wires it to local wick
│                          # (pkg/plugin isn't in a published wick release yet, so
│                          #  there is NO `replace` in go.mod — go.work handles it)
├── plugins.json           # the marketplace CATALOG (see below)
├── connector/
│   ├── _template/         # scaffold — copy this; skipped by tooling (leading _)
│   │   ├── main.go        #   wickplugin.Serve(Module())
│   │   ├── connector.go   #   the connector.Module (this is the connector-module part)
│   │   └── VERSION        #   source of truth for the plugin version
│   └── httpbin/           # a real, working sample
├── tool/                  # (later) kind=tool, same flow
├── job/                   # (later) kind=job, same flow
├── README.md  RELEASE.md  POLYGLOT.md
```

## THE ONE RULE: `Meta.Key` == folder name == slug

`key` is the single identity used for the source folder, the zip name, the on-disk
install dir (`~/.<appName>/plugins/connectors/<key>`), the runtime registry key, and
the catalog match. They must all be the same string, so:

- **`Meta.Key` MUST equal the folder name.** `wick plugin build` fails loudly if they
  differ (enforced in `cmd/cli/plugin_manifest.go`).
- **`key` must be a slug:** lowercase `a-z`, digits, `_` only. **No `-`** (it would
  break the `<key>-<ver>-<os>-<arch>.zip` split), no spaces/slashes/dots (it's a dir
  name). Enforced by `pkg/plugin.ValidateKey` at build AND install AND load time.
  Multi-word → `google_workspace`, never `google-workspace`.
- `Meta.Name` is the free display string (spaces/caps OK) — shown in the UI.

## Authoring a new plugin connector

1. `cp -r plugins/connector/_template plugins/connector/<key>`
2. Edit `connector.go` — set `Meta.Key` to `<key>` (must match the folder), write the
   `Module` exactly as the **connector-module** skill describes (Configs, Operations,
   per-op typed Input, `http.NewRequestWithContext(c.Context(), ...)`, etc.).
3. Set `Meta.DefaultTags` for categorization — works EXACTLY like a built-in
   connector. Use the shared catalog `github.com/yogasw/wick/plugins/tags` so your
   plugin lands in the same section as built-ins (don't hand-write tag structs):
   ```go
   import (
       "github.com/yogasw/wick/pkg/entity"
       "github.com/yogasw/wick/plugins/tags"
   )
   Meta.DefaultTags = []entity.DefaultTag{tags.Connector, tags.API}
   ```
   `tags.Connector` is the umbrella; add one category (`tags.API`, `.Communication`,
   `.Development`, `.Observability`). The app derives the connector-list category from
   these — no category? falls under "Other". A category not in the catalog: declare it
   inline as `entity.DefaultTag{Name: "...", IsGroup: true, SortOrder: …}`.
4. `main.go` stays a one-liner: `wickplugin.Serve(Module())`.
5. `echo 0.1.0 > plugins/connector/<key>/VERSION`
6. Build + smoke-test locally (below).

`main.go`/`Module()` split is convention; `wickplugin.Serve` is the whole runtime —
it serves the gRPC plugin, and answers `--dump-manifest` at build time.

## Building: `wick plugin build`

Production side, run from the `wick` dev CLI (NOT the app). Output is a **zip**, not a
native installer.

```bash
# one plugin, host target → one zip in plugins/bin
cd plugins && wick plugin build <key> --target $(go env GOOS)/$(go env GOARCH)

# --target also takes a comma-separated LIST (CI threads BUILD_TARGETS through this)
wick plugin build <key> --target darwin/amd64,darwin/arm64,windows/amd64

# one plugin, every supported os/arch → N zips (linux/arm64 first — Termux)
wick plugin build <key> --all

# every plugin under connector/ (skips _template)
wick plugin build --all-plugins

# only plugins whose folder changed since a ref (local dev; CI rebuilds all)
wick plugin build --changed --since origin/main

# other kinds (later)
wick plugin build --kind tool <key>

# sign: ed25519 manifest sig and/or cosign binary sig (external cosign CLI)
wick plugin build <key> --all --sign-key key.ed25519 --cosign-key cosign.key
```

CI builds plugins for the os/arch set in the `BUILD_TARGETS` Actions variable — the
SAME knob the wick binary release uses (default `darwin/amd64,darwin/arm64,windows/amd64`;
`all` = every target). One variable controls both binary and plugin targets.

Each zip = `{<key>[.exe], plugin.json}`. `plugin.json` is generated FROM the binary
(`--dump-manifest`) so it can never drift. sha256 + signature live INSIDE plugin.json.
Plugins are pure Go (no cgo) so darwin/windows cross-build from a linux runner works.

## Installing / enabling: `<app> plugin ...`

Consumption side — run from the **app binary** (the built downstream app), not the dev
CLI. The plugins dir + enable/disable DB belong to the running app.

```bash
<app> plugin search [query]            # marketplace catalog (Available)
<app> plugin install <key>             # resolve from catalog → download → verify → install
<app> plugin install ./x.zip           # or a local zip / dir / url
<app> plugin list                      # installed + version + arch + signature + enabled
<app> plugin enable|disable <key>      # toggle (reloader reconciles ~5s, no restart)
<app> plugin remove <key>
```

Install ALWAYS verifies (`VerifyManifest`: os/arch, proto_version range, sha256, and
signature when a trusted key is configured) before writing into the plugins dir. A
running app's reloader poller picks up an install/enable within ~5s.

In the manager UI, plugins live in the **same connector list** as built-ins, grouped
by their DefaultTags category (NOT a separate "Available to install" page). Filter
chips are `All · Installed · <category…>`; "Installed" = built-ins + downloaded
plugins (excludes not-yet-downloaded ones). A catalog entry not yet installed renders
in its category with a **Download** button; if it has no build for the server's
os/arch the button is disabled with a "No build for <os/arch>" note instead of being
hidden. Install/enable/disable/remove trigger an immediate reload (no ~5s wait).

## The marketplace catalog (`plugins.json`)

`plugins/plugins.json` on the default branch is the catalog. wick fetches it RAW
(`raw.githubusercontent.com/.../master/plugins.json`) — **not** the GitHub Releases
API, so no rate limit / no token. Listing is metadata-only; the binary is pulled from
the per-os/arch release URL in `assets` only on install.

```json
[{ "key": "httpbin", "name": "HTTPBin", "description": "...", "version": "0.1.0",
   "default_tags": [{ "Name": "Connector", "IsGroup": true, "SortOrder": 50 },
                    { "Name": "API", "IsGroup": true, "SortOrder": 30 }],
   "assets": { "linux/arm64": "https://github.com/.../httpbin-0.1.0-linux-arm64.zip" } }]
```

`key` is the identity; `name`/`description`/`default_tags` are display + categorization
(the SAME `[]entity.DefaultTag` the manifest carries — the app categorizes a plugin
identically to a built-in). Override the catalog URL with `WICK_PLUGIN_CATALOG`.

The catalog is **generated by `wick plugin catalog`** (Go, struct-typed — the shape is
`connplugin.Available`, shared with the reader so the JSON can't drift), NOT a jq
pipeline. It lists the repo's GitHub releases, keeps the highest version per plugin,
maps each zip asset to its os/arch URL, and lifts name/description/default_tags from
the released manifest:

```bash
wick plugin catalog --repo yogasw/wick --out plugins/plugins.json   # --token raises rate limit
```

On release, CI runs this automatically and commits the result (below).

## Releasing: PR → `release` (CI does the rest)

You do steps 1–3; CI does 4–8. See `plugins/RELEASE.md` for the full flow.

```
1. author + set Meta.Key == folder
2. bump VERSION
3. open PR  master → release
   ── single entry: .github/workflows/release.yml ──
   check-source-branch decides core-vs-plugin from the VERSION tag, then fires
   release-plugins.yml via repository_dispatch (event_type=core-released) in
   exactly one of two cases:
     • core VERSION bumped → core jobs run, THEN dispatch (plugins rebuild against
       the new wick)
     • core VERSION unchanged → core jobs skip, dispatch immediately
   ── .github/workflows/release-plugins.yml (dispatch-only, never a bare PR) ──
4. detect     rebuild EVERY plugin (the build's tag-exists check is idempotent, so
              only plugins whose VERSION changed actually cut a release)
5. test       go test pkg/plugin + internal/connectors/plugin + cmd/cli  (hard gate)
6. build      wick plugin build <key> --target <BUILD_TARGETS> → gh release "<key>/v<ver>"
              with make_latest:false (so a plugin never steals "Latest" from core)
              (skipped if that tag already exists — bump VERSION for a new one)
7. update-catalog  `wick plugin catalog` regenerates plugins.json + commits to master
```

Single entry point: `release.yml` is the only PR-triggered release workflow; the
plugin pipeline is reached ONLY via `repository_dispatch`, so the two never run at the
same time on one PR. `release-plugins.yml` uses its own concurrency group
(`wick-release-plugins`, cancel-in-progress: true) so a re-dispatched run supersedes
the in-flight one.

## Constraints / gotchas

- **Never let `Meta.Key` ≠ folder name**, and keep `key` a `[a-z0-9_]` slug — both are
  enforced; a `-` or mismatch fails the build.
- **`_`-prefixed folders are invisible to `go ./...`** and to `--all-plugins` (that's
  intentional — `_template` is a scaffold). Build it explicitly by path to smoke-test.
- **Don't add a sigstore-go dependency** for cosign — it would bloat every plugin
  binary. cosign is invoked as an external CLI at build time only.
- **Don't make the path-filtered release workflows required status checks** — a skipped
  run never reports and would block the PR forever.
- Plugin tags are `<key>/v<ver>` (start with the key) so they never collide with the
  core `v*` release tags.
- **The install dir resolves from `appname.Resolve()`** (ldflag → `wick.yml` `name:` →
  "wick") in `internal/connectors/plugin/paths.go` — the SAME source as `wick.db`, so
  plugins always sit in the same `~/.<appName>/` tree as the DB. Don't derive it from
  the binary basename: a debug build / MCP subprocess named differently would scan a
  different dir than where the DB (and installs) live, and plugins would silently
  vanish from the list.

## Verify a plugin end-to-end (local, no CI)

Fastest path in this repo: VSCode. The **"plugin: build all → lab"** task (and the
**"wicklab + plugins"** launch config) build every plugin from current source and drop
the unzipped `{binary, plugin.json}` into wick-lab's plugin dir, so a running lab
hot-reloads them within ~5s — manifests reflect the latest source (e.g. new
DefaultTags) WITHOUT cutting a release. **"plugin: clear lab"** wipes them.

By hand:

```bash
# build the dev CLI from source (go.work resolves local pkg/plugin)
go build -o /tmp/wick .
cd plugins && /tmp/wick plugin build <key> --target $(go env GOOS)/$(go env GOARCH)
# unzip the result and confirm the manifest verifies against the binary:
#   unzip bin/<key>-*.zip -d /tmp/x  &&  the binary's plugin.json must pass VerifyManifest
go test ./pkg/plugin/... ./internal/connectors/plugin/... ./cmd/cli/...
```
