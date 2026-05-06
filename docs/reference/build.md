---
outline: deep
---

# `wick build`

`wick build` compiles your project to a Go binary with version metadata and (optionally) self-updater credentials baked in via Go ldflags, then wraps it into the platform-native distributable: `.exe` with embedded icon + version metadata on Windows, `.app` bundle plus `.dmg` disk image on macOS, `.deb` package on Linux. Replaces the hand-rolled `go build -ldflags ...` task that older `wick.yml` templates shipped.

The default behavior reads `wick.yml` and produces in `bin/`:

```bash
wick build
# bin/<name>-<goos>-<goarch>[.exe]   raw binary
# bin/<name>.app/                    macOS bundle (darwin only)
# bin/<name>-darwin-<arch>.dmg       macOS disk image (darwin host only — needs hdiutil)
# bin/<name>-linux-<arch>.deb        Debian package (linux only)
```

That covers local development. For cross-compile pick one of the flags below (env vars still work for CI compatibility):

```bash
wick build --target linux/arm64       # shorthand
wick build --goos linux --goarch arm64 # explicit (mirrors env vars)
GOOS=linux GOARCH=arm64 wick build     # env vars (CI flow)
```

Multi-target in one shot — best-effort, skips targets that can't build on the current host (e.g. darwin/* when not running on macOS):

```bash
wick build --all
# > windows/amd64   ✓ bin/myapp-windows-amd64.exe
# > linux/amd64     ✓ bin/myapp-linux-amd64
# > darwin/amd64    ✗ skipped (darwin needs macOS host)
# Summary: 4/6 succeeded (2 skipped/failed)
```

Embed PAT + version for a release build:

```bash
WICK_APP_VERSION=1.2.0 \
GITHUB_PAT=$RELEASE_PAT \
GITHUB_REPOSITORY=acme/myapp-releases \
wick build --target linux/arm64
```

## Flags

| Flag | Env fallback | Effect |
|---|---|---|
| `--app-name` | `WICK_APP_NAME` | Sets `app.BuildAppName`. Used to namespace config / DB / log paths and as the default MCP server name. |
| `--app-version` | `WICK_APP_VERSION` | Sets `app.BuildAppVersion`. Shown in the tray title and About menu, advertised by MCP. |
| `--github-pat` | `GITHUB_PAT` | Sets `app.GitHubPAT`. Empty = self-updater disabled. |
| `--github-repo` | `GITHUB_REPOSITORY` | Sets `app.GitHubRepo` (`owner/repo`). Empty = self-updater disabled. |
| `-o`, `--output` | — | Raw binary output path. Default `bin/<app-name>-<goos>-<goarch>[.exe]`. The platform-native distributable (.dmg/.deb) is always written next to it; this flag only renames the raw binary. |
| `-t`, `--target` | — | Target shorthand `<os>/<arch>` (e.g. `linux/arm64`). Mutually exclusive with `--goos`/`--goarch`. |
| `--goos` | `GOOS` | Target GOOS. Mutually exclusive with `--target`. |
| `--goarch` | `GOARCH` | Target GOARCH. Mutually exclusive with `--target`. |
| `--all` | — | Best-effort build for every supported OS/arch. Skips darwin/* on non-darwin hosts. Mutually exclusive with `--target`/`--goos`/`--goarch`/`--output`. |
| `--headless` | — | Adds `-tags headless`. Drops the tray UI; keeps `server`, `worker`, `mcp` subcommands. |

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

1. **`prepare`** — read `version:` from `wick.yml`. If `v<version>` is not yet a tag on origin, output `created=true` plus the commit SHA. If it already exists, output `created=false` and skip downstream jobs. **The tag is not pushed yet.**
2. **`build`** (`needs: prepare`, runs only if `created=true`, `fail-fast: false`) — checkout the SHA, build 6 binaries via the matrix below, upload artifacts. A failed matrix entry does not cancel the others.
3. **`release`** (`needs: [prepare, build]`, runs even if some matrix entries failed) — download artifacts, fail with a clear error if **none** were uploaded, otherwise `gh release create <tag>` against your releases repo and **then** push the tag to the source repo.

**Tag-after-release semantics.** The tag only lands on origin when at least one binary is published. If every build fails, no tag is pushed and a re-run starts from the same SHA. For the same-repo setup, `gh release create --target <sha>` creates the tag atomically with the release; for the separate-releases-repo setup, the tag is pushed via `git push origin <tag>` after `gh release create` succeeds.

**Why one workflow instead of two:** GitHub blocks tag pushes made with the default `GITHUB_TOKEN` from triggering other workflows (anti-loop guard). A split design (`auto-tag.yml` → `release.yml`) needs a user PAT to push the tag, otherwise `release.yml` never fires. The single-flow design uses job dependencies (`needs:`) instead of an event-trigger handoff, so it works with `github.token` alone — no `PAT_BUILD` required for same-repo setups.

Build matrix:

| OS | Arch | Released asset |
|---|---|---|
| windows | amd64 | `<app>-windows-amd64.exe` |
| windows | arm64 | `<app>-windows-arm64.exe` |
| darwin | amd64 | `<app>-darwin-amd64.dmg` |
| darwin | arm64 | `<app>-darwin-arm64.dmg` |
| linux | amd64 | `<app>-linux-amd64.deb` |
| linux | arm64 | `<app>-linux-arm64.deb` |

Each asset ships with a `.sha256` sibling that the self-updater verifies before extracting the inner binary and swapping in place.

User install flow per OS:

| OS | Action |
|---|---|
| Windows | Double-click the `.exe` — runs directly with embedded icon + version metadata. |
| macOS | Double-click the `.dmg`, drag `<app>.app` into `/Applications`. |
| Linux | `sudo apt install ./<app>-linux-<arch>.deb` (or `dpkg -i`). Installs to `/usr/bin/<app>` with `.desktop` entry + icon. |

### Limiting the build matrix

Set the optional `BUILD_TARGETS` Actions variable (Settings → Secrets and variables → Actions → Variables) to a comma-separated list of `<os>/<arch>` pairs. Anything not listed is skipped at the start of its runner — no checkout, no build cost. Leave it unset to build everything (the default).

| `BUILD_TARGETS` value | Effect |
|---|---|
| _(unset)_ | Build all six targets. |
| `linux/amd64` | Linux x64 only — useful for Docker-only deployments. |
| `darwin/arm64,linux/amd64` | Mac silicon + Linux x64 — common dev/server combo. |
| `windows/amd64,windows/arm64` | Windows desktop only. |
| `linux/amd64,linux/arm64` | Both Linux arches, skip mac/windows. |

Valid values: `windows/amd64`, `windows/arm64`, `darwin/amd64`, `darwin/arm64`, `linux/amd64`, `linux/arm64`.

The release job ships whatever artifacts made it through, so you can also use this to drop a flaky target temporarily without editing the workflow.

### Auto-bumping the version

Set the optional `AUTO_VERSION` Actions variable to `true` to make every push to `main`/`master` cut a new release automatically. The flow:

1. **`prepare`** runs `wick version next`, which reads `version:` from `wick.yml`, bumps the **last numeric segment** by one, writes the new value back, and prints it.
2. **`build`** bakes that value into the binary via `WICK_APP_VERSION`.
3. **`release`** publishes `vX.Y.Z`, pushes the tag, then re-runs `wick version next` on a fresh checkout (idempotent — same baseline, same bump) and commits the wick.yml diff back to the branch with `[skip ci]`.

The bump format follows whatever is already in `wick.yml:version`:

| Current `version:` | Next tag |
|---|---|
| `1` | `v2` |
| `0.1` | `v0.2` |
| `0.6.4` | `v0.6.5` |
| `1.2.3.4` | `v1.2.3.5` |

| `AUTO_VERSION` | Behavior |
|---|---|
| _(unset)_ or `false` | Existing flow — read `version:` as-is, skip if the tag already exists. Bump `wick.yml` manually before each release. |
| `true` | `wick version next` bumps `wick.yml` last segment +1, every push releases, commit-back keeps `wick.yml` in sync with the latest tag. |

#### Why this is safe

- **No infinite loop.** The commit-back step pushes via `github.token`. GitHub explicitly does not re-trigger workflows on commits pushed by `GITHUB_TOKEN` ([anti-loop guard](https://docs.github.com/en/actions/security-guides/automatic-token-authentication#using-the-github_token-in-a-workflow)). The `[skip ci]` marker is belt-and-suspenders.
- **No race.** A workflow-level <code v-pre>concurrency: { group: release-${{ github.ref }}, cancel-in-progress: false }</code> serializes pushes on the same branch, so two pushes can't both try to bump `0.6.4 → 0.6.5`.
- **Atomic enough.** If the release succeeds but the commit-back fails (e.g. branch protection blocks the bot push), the next run will read the still-old `wick.yml`, compute the same tag, and skip with "tag exists." The release isn't lost; the wick.yml diff is what's missing — recoverable manually.

#### Manual jump (cut a minor/major release)

Edit `wick.yml:version` to a new base (e.g. `0.7.0`), push:
- That push releases `v0.7.0`.
- Commit-back bumps to `0.7.1`.
- Auto-bump continues `v0.7.2`, `v0.7.3`, …

#### Branch protection

If `main`/`master` requires PRs or status checks, allow `github-actions[bot]` to bypass — otherwise the commit-back fails. The release itself still publishes; only the wick.yml diff is missing.

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
release.yml job 1 (prepare): tag exists on origin?
                                yes → created=false, stop
                                no  → created=true, sha=<HEAD>, no push yet
    ↓
release.yml job 2 (build, fail-fast=false): matrix build N binaries → upload artifacts
                                            (failed entries don't cancel the others)
    ↓
release.yml job 3 (release): any artifacts uploaded?
                                no  → error, no tag pushed, re-run starts clean
                                yes → gh release create + push tag to origin
    ↓
new binary in <app>-releases
    ↓
existing install → self-updater downloads bundle → extracts inner binary → "Restart to apply" appears
```

A manual `git tag v1.2.3 && git push origin v1.2.3` does **not** trigger this workflow — the trigger is `on: push branches`, not `on: push tags`. To cut a release, bump `version:` in `wick.yml` and push to `main`; that's the single source of truth — unless `AUTO_VERSION=true`, in which case every push cuts the next tag automatically (see [Auto-bumping the version](#auto-bumping-the-version)).

## Cross-compilation notes

`fyne.io/systray` keeps the tray cgo-light:

- **Windows**: pure syscall, no cgo. Cross-compile from any host.
- **Linux**: pure DBus, no cgo, no WebKit dependencies. Cross-compile from any host.
- **macOS**: cgo (Cocoa). Must run on a macOS runner.

Cross-compiling Windows / Linux variants from `ubuntu-latest` works because they don't link cgo. macOS arm64 → amd64 (and vice versa) on the same `macos-latest` runner needs `CGO_ENABLED=1` set explicitly — Go disables cgo by default whenever `GOARCH` differs from the host arch, which would skip the `.m` files and fail with `undefined: setInternalLoop` errors. The shipped `release.yml` sets `CGO_ENABLED: 1` only for `darwin/amd64` (the cross combo on Apple Silicon runners) via a `cgo: 1` matrix flag; clang's native `-arch` support handles the rest. See [golang/go#44112](https://github.com/golang/go/issues/44112).

## See also

- [Desktop Tray](/guide/desktop-tray) — what users get when they run a binary built with these flags
- [`wick.yml` reference](./wick-yml) — top-level `name:` and `version:` fields
- [Environment Variables](./env-vars) — build-time env (`WICK_APP_NAME`, `GITHUB_PAT`, ...)
