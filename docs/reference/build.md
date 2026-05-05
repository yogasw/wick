---
outline: deep
---

# `wick build`

`wick build` compiles your project to a single binary with version metadata and (optionally) self-updater credentials baked in via Go ldflags. It replaces the hand-rolled `go build -ldflags ...` task that older `wick.yml` templates shipped.

The default behavior reads `wick.yml` and produces `bin/<name>[.exe]`:

```bash
wick build
```

That covers local development. For CI and cross-compile, mix flags and env vars:

```bash
GOOS=linux GOARCH=arm64 \
WICK_APP_VERSION=1.2.0 \
GITHUB_PAT=$RELEASE_PAT \
GITHUB_REPOSITORY=acme/myapp-releases \
wick build -o myapp-linux-arm64
```

## Flags

| Flag | Env fallback | Effect |
|---|---|---|
| `--app-name` | `WICK_APP_NAME` | Sets `app.BuildAppName`. Used to namespace config / DB / log paths and as the default MCP server name. |
| `--app-version` | `WICK_APP_VERSION` | Sets `app.BuildAppVersion`. Shown in the tray title and About menu, advertised by MCP. |
| `--github-pat` | `GITHUB_PAT` | Sets `app.GitHubPAT`. Empty = self-updater disabled. |
| `--github-repo` | `GITHUB_REPOSITORY` | Sets `app.GitHubRepo` (`owner/repo`). Empty = self-updater disabled. |
| `-o`, `--output` | — | Output path. Default `bin/<app-name>[.exe]`. |
| `--headless` | — | Adds `-tags headless`. Drops the tray UI; keeps `server`, `worker`, `mcp` subcommands. |

`GOOS` / `GOARCH` are inherited from the environment — no flag needed.

## Resolution order

Each value is resolved independently, picking the first non-empty source:

| Value | Order |
|---|---|
| App name | `--app-name` → `$WICK_APP_NAME` → `name:` in `wick.yml` → `"app"` |
| App version | `--app-version` → `$WICK_APP_VERSION` → `version:` in `wick.yml` → `"dev"` |
| GitHub PAT | `--github-pat` → `$GITHUB_PAT` |
| GitHub repo | `--github-repo` → `$GITHUB_REPOSITORY` (auto-set by GitHub Actions) |

The wick framework version (`BuildWickVersion`) is auto-filled from `debug.ReadBuildInfo()` — no flag.

## ldflags injection

`wick build` calls `go build` with:

```
-X github.com/yogasw/wick/app.BuildAppName=<name>
-X github.com/yogasw/wick/app.BuildAppVersion=<version>
-X github.com/yogasw/wick/app.GitHubPAT=<pat>          (if non-empty)
-X github.com/yogasw/wick/app.GitHubRepo=<owner/repo>  (if non-empty)
```

`BuildCommit` and `BuildTime` are populated by `debug.ReadBuildInfo()` — VCS metadata baked in by the Go toolchain when the build happens inside a git checkout.

## CI/CD with GitHub Actions

`wick init` copies a single workflow into `template/.github/workflows/release.yml`. It implements push-to-tag-to-release as three sequential jobs in one run:

### `release.yml`

Trigger: `on: push` to `main` / `master`. Three jobs:

1. **`tag`** — read `version:` from `wick.yml`. If `v<version>` is not yet a tag, push it; output `created=true`. If it already exists, output `created=false` and skip downstream jobs.
2. **`build`** (`needs: tag`, runs only if `created=true`) — checkout `ref: <new-tag>`, then build 6 binaries via the matrix below; upload artifacts.
3. **`release`** (`needs: [tag, build]`) — download artifacts and `gh release create <tag>` against your releases repo.

**Why one workflow instead of two:** GitHub blocks tag pushes made with the default `GITHUB_TOKEN` from triggering other workflows (anti-loop guard). A split design (`auto-tag.yml` → `release.yml`) needs a user PAT to push the tag, otherwise `release.yml` never fires. The single-flow design uses job dependencies (`needs:`) instead of an event-trigger handoff, so it works with `github.token` alone — no `PAT_BUILD` required for same-repo setups.

Build matrix:

| OS | Arch | Output |
|---|---|---|
| windows | amd64 | `<app>-windows-amd64.exe` |
| windows | arm64 | `<app>-windows-arm64.exe` |
| darwin | amd64 | `<app>-darwin-amd64` |
| darwin | arm64 | `<app>-darwin-arm64` |
| linux | amd64 | `<app>-linux-amd64` |
| linux | arm64 | `<app>-linux-arm64` |

Each binary ships with a `.sha256` sibling that the self-updater verifies before swap.

## PAT setup

The self-updater needs a token that the **shipped binary** can use to read GitHub releases. Treat it as embedded credential, not a build-system secret.

### Two repos (recommended for private apps)

| Repo | Visibility | Holds |
|---|---|---|
| `<owner>/<app>` | private (or public) | Source code |
| `<owner>/<app>-releases` | private | Compiled binaries + `.sha256` files |

If the embedded PAT leaks, the attacker can download binaries — they cannot read source.

| Setting | Where | Value |
|---|---|---|
| `vars.RELEASES_REPO` | Source repo Actions variables | `<owner>/<app>-releases` |
| `secrets.PAT_DOWNLOAD` | Source repo Actions secrets | Fine-grained PAT scoped to `<app>-releases`, **Contents: read** — gets baked into every binary. |
| `secrets.PAT_BUILD` | Source repo Actions secrets | Fine-grained PAT scoped to `<app>-releases`, **Contents: read + write** — only used by the workflow to upload assets. |

### Single repo (source = releases)

| Setting | Value |
|---|---|
| `vars.RELEASES_REPO` | _(empty — falls back to `github.repository`)_ |
| `secrets.PAT_DOWNLOAD` | Fine-grained PAT scoped to this repo, Contents read — baked into every binary. |
| `secrets.PAT_BUILD` | _(not needed — `github.token` has write access to the same repo, and the single-flow design avoids the anti-loop trigger problem.)_ |

The exact step-by-step walkthrough — including links to GitHub's PAT and Actions Secrets pages — lives in the header comments of `template/.github/workflows/release.yml`. Open the workflow file in your generated project; the comments are kept current with the workflow logic.

### Rotating the PAT

GitHub fine-grained PATs cannot be rotated via API. The flow is manual but self-healing:

1. Generate a new PAT with the same scope.
2. Update `secrets.PAT_DOWNLOAD` in the source repo.
3. Bump `version:` in `wick.yml` and push to `main`.
4. `release.yml` tags and builds new binaries with the new PAT embedded.
5. Existing installs auto-update — and the new binaries can keep checking for releases.

When a PAT expires, the tray menu surfaces it as `Update check failed — PAT expired (see logs)`. As long as you ship a new release before the expiry hits every install, no one notices.

## Trigger flow

```
bump version: in wick.yml → push main
    ↓
release.yml job 1 (tag): tag exists? created=false (skip) : git tag + push, created=true
    ↓
release.yml job 2 (build): build 6 binaries → upload artifacts
    ↓
release.yml job 3 (release): gh release create
    ↓
new binary in <app>-releases
    ↓
existing install → self-updater downloads → "Restart to apply" appears
```

A manual `git tag v1.2.3 && git push origin v1.2.3` does **not** trigger this workflow — the trigger is `on: push branches`, not `on: push tags`. To cut a release, bump `version:` in `wick.yml` and push to `main`; that's the single source of truth.

## Cross-compilation notes

`fyne.io/systray` keeps the tray cgo-light:

- **Windows**: pure syscall, no cgo.
- **Linux**: pure DBus, no cgo, no WebKit dependencies.
- **macOS**: cgo (Cocoa). Builds must run on a `macos-latest` runner.

Cross-compiling Windows / Linux variants from `ubuntu-latest` works. macOS amd64 → arm64 is fine on the same `macos-latest` runner.

## See also

- [Desktop Tray](/guide/desktop-tray) — what users get when they run a binary built with these flags
- [`wick.yml` reference](./wick-yml) — top-level `name:` and `version:` fields
- [Environment Variables](./env-vars) — build-time env (`WICK_APP_NAME`, `GITHUB_PAT`, ...)
