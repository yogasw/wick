---
name: plugin-module
description: Use when building, packaging, or releasing a connector (or later a tool/job) as an EXTERNAL wick PLUGIN — a separate binary in the wick-plugins monorepo that wick downloads and runs over gRPC, instead of an in-tree module compiled into wick. Covers the wick-plugins repo layout (one folder per plugin under connector/<key>/), the key==folder one-identity rule, `wick plugin build` (zip output, --kind, --all, signing), the marketplace catalog (plugins.json), install/enable/disable via the app, and the PR→release CI flow. The MODULE contract itself (Meta, Configs, Operations, Ctx, wick:"..." tags) is identical to in-tree modules — defer to the connector-module / tool-module skills for that; this skill is ONLY the plugin packaging + shipping layer on top.
allowed-tools: Read, Grep, Glob, Edit, Write, Bash
paths:
  - "wick-plugins/**"
  - "cmd/cli/plugin.go"
  - "cmd/cli/plugin_manifest.go"
  - "cmd/cli/plugin_cosign.go"
  - "pkg/plugin/**"
  - "internal/connectors/plugin/**"
  - "app/plugin_cmd.go"
  - ".github/workflows/release-plugins.yml"
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
| Lives in | `internal/connectors/<name>/` | `wick-plugins/connector/<key>/` (separate repo/module) |
| Registered by | `RegisterBuiltins()` at compile time | downloaded → scanned from `~/.wick/plugins/connectors/<key>/` at runtime |
| Runs | in the wick process (function call) | own subprocess, gRPC over UDS (`hashicorp/go-plugin`) |
| `main()` | none — it's a package | `package main` → `wickplugin.Serve(Module())` |
| Ships as | part of the wick binary | `<key>-<ver>-<os>-<arch>.zip` (binary + `plugin.json`) |

The **module value is identical** — a plugin's `connector.Module` is built the same
way. The only code difference is the `main.go` wrapper and that it's a separate Go
module. So: write the connector exactly as connector-module teaches, then wrap +
package per below.

## The wick-plugins repo

```
wick-plugins/
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
install dir (`~/.wick/plugins/connectors/<key>`), the runtime registry key, and the
catalog match. They must all be the same string, so:

- **`Meta.Key` MUST equal the folder name.** `wick plugin build` fails loudly if they
  differ (enforced in `cmd/cli/plugin_manifest.go`).
- **`key` must be a slug:** lowercase `a-z`, digits, `_` only. **No `-`** (it would
  break the `<key>-<ver>-<os>-<arch>.zip` split), no spaces/slashes/dots (it's a dir
  name). Enforced by `pkg/plugin.ValidateKey` at build AND install AND load time.
  Multi-word → `google_workspace`, never `google-workspace`.
- `Meta.Name` is the free display string (spaces/caps OK) — shown in the UI.

## Authoring a new plugin connector

1. `cp -r wick-plugins/connector/_template wick-plugins/connector/<key>`
2. Edit `connector.go` — set `Meta.Key` to `<key>` (must match the folder), write the
   `Module` exactly as the **connector-module** skill describes (Configs, Operations,
   per-op typed Input, `http.NewRequestWithContext(c.Context(), ...)`, etc.).
3. `main.go` stays a one-liner: `wickplugin.Serve(Module())`.
4. `echo 0.1.0 > wick-plugins/connector/<key>/VERSION`
5. Build + smoke-test locally (below).

`main.go`/`Module()` split is convention; `wickplugin.Serve` is the whole runtime —
it serves the gRPC plugin, and answers `--dump-manifest` at build time.

## Building: `wick plugin build`

Production side, run from the `wick` dev CLI (NOT the app). Output is a **zip**, not a
native installer.

```bash
# one plugin, host target → one zip in wick-plugins/bin
cd wick-plugins && wick plugin build <key> --target $(go env GOOS)/$(go env GOARCH)

# one plugin, every os/arch → N zips (linux/arm64 first — Termux)
wick plugin build <key> --all

# every plugin under connector/ (skips _template)
wick plugin build --all-plugins

# only plugins whose folder changed since a ref (CI uses this)
wick plugin build --changed --since origin/main

# other kinds (later)
wick plugin build --kind tool <key>

# sign: ed25519 manifest sig and/or cosign binary sig (external cosign CLI)
wick plugin build <key> --all --sign-key key.ed25519 --cosign-key cosign.key
```

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

In the manager UI, available plugins appear in the **same connector list** under
"Available to install" (not a separate page) — built-in + installed render as normal
cards; catalog entries not yet installed get a Download button.

## The marketplace catalog (`plugins.json`)

`wick-plugins/plugins.json` on the default branch is the catalog. wick fetches it RAW
(`raw.githubusercontent.com/.../master/plugins.json`) — **not** the GitHub Releases
API, so no rate limit / no token. Listing is metadata-only; the binary is pulled from
the per-os/arch release URL in `assets` only on install.

```json
[{ "key": "httpbin", "name": "HTTPBin", "description": "...", "version": "0.1.0",
   "assets": { "linux/arm64": "https://github.com/.../httpbin-0.1.0-linux-arm64.zip" } }]
```

`key` is the identity; `name`/`description` are display. Override the catalog URL with
`WICK_PLUGIN_CATALOG`. On release, CI regenerates this file automatically (below).

## Releasing: PR → `release` (CI does the rest)

You do steps 1–3; CI does 4–8. See `wick-plugins/RELEASE.md` for the full flow.

```
1. author + set Meta.Key == folder
2. bump VERSION
3. open PR  master → release  touching wick-plugins/connector/<key>/**
   ── .github/workflows/release-plugins.yml ──
4. guard      reject fork / non-master / non-admin  (ADMIN_TOKEN)
5. detect     diff PR base..head → changed plugins only
6. test       go test pkg/plugin + internal/connectors/plugin + cmd/cli  (hard gate)
7. build      wick plugin build <key> --all → gh release "<key>/v<ver>"
              (skipped if that tag already exists — bump VERSION for a new one)
8. update-catalog  regenerate plugins.json from live releases + commit to master
```

Two pipelines, same gate: a PR that only touches `wick-plugins/**` runs
`release-plugins.yml`; a core-only PR runs the core `release.yml` (which has
`paths-ignore: wick-plugins/**`). A mixed PR runs both.

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

## Verify a plugin end-to-end (local, no CI)

```bash
# build the dev CLI from source (go.work resolves local pkg/plugin)
go build -o /tmp/wick .
cd wick-plugins && /tmp/wick plugin build <key> --target $(go env GOOS)/$(go env GOARCH)
# unzip the result and confirm the manifest verifies against the binary:
#   unzip bin/<key>-*.zip -d /tmp/x  &&  the binary's plugin.json must pass VerifyManifest
go test ./pkg/plugin/... ./internal/connectors/plugin/... ./cmd/cli/...
```
