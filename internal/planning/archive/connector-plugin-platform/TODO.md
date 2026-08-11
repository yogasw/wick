# Connector Plugin Platform — Progress Tracker

Status of the [PLAN.md](./PLAN.md) roadmap (§14) and follow-on items. Legend:
**✅ done** · **🟡 partial** · **⬜ not started**.

> Implementation lives on branch `feat/connector-plugin-platform`
> (engine Phases 0–4). Host execution envelope (`internal/connectors/service.go`,
> `registry.go`, `enc.go`, `pkg/connector/connector.go`) is intentionally untouched —
> connectors plug in via the existing `op.Execute` seam (adapter pattern).

## ✅ SHIPPED — control surface (production + consumption CLIs)

The engine was always wired in `internal/connectors/plugin/`; what was missing
was a way to *drive* it. Both sides now exist:

**Production (build → release zip)** — `cmd/cli/plugin.go`, on the `wick` dev CLI:

- `wick plugin build [name...]` — compiles `<kind>/<name>/main.go`, runs the
  binary's `--dump-manifest`, packs `{binary, plugin.json}` into
  `bin/<name>-<version>-<goos>-<goarch>.zip`.
- Generic over `--kind connector|tool|job` (default `connector`) — folder per
  kind, forward-compatible with §18.
- Selectors: explicit names · `--all-plugins` · `--changed --since <ref>` (CI).
- Targets: `--target <os>/<arch>` · split `--goos/--goarch` · `--all` (cross-build
  matrix, linux/arm64 first for Termux). Cross-arch manifests are produced via a
  host-arch helper build, then sha256/os_arch/signature rewritten for the real
  target. `--sign-key` signs each manifest (ed25519).
- Round-trip test: `cmd/cli/plugin_test.go` builds a sample connector → zip →
  unzip → `VerifyManifest` passes.

**Consumption (install / enable / disable)** — `app/plugin_cmd.go`, on the **app**
binary (`<your-app> plugin ...`), because the plugins dir + enable/disable DB
belong to the running app, not the dev CLI:

- `<app> plugin install <path|url|.zip|.tar.gz>` — resolves source, verifies via
  `VerifyManifest`, copies into `DefaultDir()/<key>/`. The running app's reloader
  poller picks it up within ~5s — no restart.
- `<app> plugin list` — installed plugins + version + arch-match + signature
  status + enabled/disabled.
- `<app> plugin enable|disable <key>` — flips `StateStore.SetEnabled`; reloader
  reconciles (disabled → removed from `wick_list`, enabled → re-registered).
- `<app> plugin remove <key>` — deletes the plugin dir.
- Tests: `app/plugin_cmd_test.go` (`TestInstallFromDir`, `TestInstallRejectsWrongArch`).

**Starter repo:** `plugins/` (in-tree for now; extract to its own repo
later) — 1 `go.mod`, `connector/_template/` (a complete working connector:
HTTP GET + DELETE), README, and `.github/workflows/release.yml` (changed-only,
per-kind, `<name>/v<version>` releases).

> ⚠️ Correction to history: an earlier version of this tracker said the
> `wick plugin` CLI did not exist. The **consumption** CLI was in fact already
> present in `app/plugin_cmd.go` (just under `app/`, not `cmd/`); the
> **production** side (`wick plugin build`) is the part that was genuinely added
> in this pass. Command name is `wick plugin build` (generic, `--kind`), NOT
> `wick build connector`.

**Marketplace catalog (NEW — raw JSON, not GitHub API):** `internal/connectors/plugin/registry.go`
— `Catalog.List/Resolve` fetches one curated `plugins.json` from the plugins default
branch via **raw.githubusercontent.com** (no API → no rate limit, no token). Each entry
carries per-os/arch release download URLs; the binary is pulled only on install. ETag +
15-min cache. Override with `WICK_PLUGIN_CATALOG`. Drives `<app> plugin search`/`install
<name>` AND the in-UI "Available to install" section.

> Decision (changed from §6.2 design): listing via a **single raw JSON file on master**,
> NOT the GitHub Releases API. The API caps unauthenticated calls at 60/hr per IP; a raw
> file fetch doesn't. Download still comes from the GitHub Release (URLs live in the JSON).

**Marketplace UI (NEW — one list, not a separate page):** merged into `ConnectorsIndex.svelte`.
Built-in + downloaded plugins are normal cards; catalog entries not yet installed show under
"Available to install" with a Download button (`internal/manager/plugins_api.go`, admin-gated).
No `/plugins` page — it's all the connector list.

**Binary size (NEW):** `wick plugin build` strips `-s -w`; `pkg/connector`/`pkg/job`
decoupled from `pkg/tool` (DefaultTag moved to `pkg/entity`) so plugins don't carry the
templ/HTML stack. httpbin sample: 19→13 MB.

**Skill (NEW):** `.claude/skills/plugin-module/SKILL.md` — the packaging+shipping layer
on top of `connector-module`/`tool-module` (which still own the module contract). Covers
plugins layout, key==folder slug rule, `wick plugin build`, `<app> plugin
install/enable`, the `plugins.json` catalog, and the PR→release CI flow. Every command
+ enforcement claim verified against the code.

**CI split + auto-catalog (NEW):** two isolated pipelines in one repo —
`release-plugins.yml` (push, path `plugins/**`) builds only changed plugins,
releases `<name>/v<ver>`, then regenerates `plugins.json` from live releases and
commits it. Core release (PR→`release` / `v*` tags) is untouched by plugin pushes
and vice-versa. Flow + constraints in `plugins/RELEASE.md`.

### Still open (control surface)

- [ ] Publish the `plugins` repo + its releases (the catalog 404s until `plugins.json` is reachable at the raw URL — expected pre-publish).
- [ ] Plugin folder names can't contain `-` (catalog parses `<name>-<ver>-<os>-<arch>.zip`); documented in RELEASE.md — could be made robust later.

## Roadmap (PLAN.md §14)

| Fase | Scope | Status | Notes |
|---|---|---|---|
| 0 | proto + handshake + plugin manager (`hashicorp/go-plugin`) | ✅ done | gRPC contract `pkg/plugin/proto`, AutoMTLS over UDS |
| 1 | 1 pilot connector ported + measure on Termux | 🟡 partial | pilots **slack + googleworkspace** built in-tree (`cmd/plugins/`); warm IPC ~222µs measured on amd64. **Pending:** run `measure.md` protocol on Termux/arm64 |
| 2 | Registry + manifest + verify checksum/sig | ✅ done | signed manifest envelope (`pkg/plugin/manifest.go`), ed25519 (`signing.go`), `VerifyManifest`; **driven** by `<app> plugin install` (verify-on-install) + `wick plugin build --sign-key` (sign-on-build) |
| 3 | Lifecycle: lazy spawn, idle-kill, cap concurrent, LRU evict, enable/disable | ✅ done | lease model + in-flight tracking; cap+bounded-queue; crash backoff/circuit-breaker; UDS hardening; DB enable/disable overlay (`StateStore`); **driven** by `<app> plugin enable/disable/remove` (reloader reconciles) |
| 4 | Sandbox + streaming + warm pool | ✅ done (scoped) | see honest scoping below |
| 5 | Buka marketplace pihak ketiga | 🟡 discovery done | `Registry` (GitHub Releases API, curated repo, ETag cache) + `<app> plugin search`/`install <name>` shipped. **Missing:** the `plugins` registry repo published with releases + admin marketplace UI + signing trust policy for 3rd-party |

## Fase 4 — honest scoping (what shipped vs deferred)

| Item | Status | Notes |
|---|---|---|
| Warm pool (keep-warm) | ✅ done | `WICK_PLUGIN_WARM` env; eager-spawn at boot (api+mcp); pinned (exempt idle-kill + LRU) |
| Streaming — chunked transport | ✅ done | `ExecuteStream` wired end-to-end (server chunks 1MiB, client reassembles, adapter dispatches); removes the 4MB gRPC message ceiling; e2e >2MiB over real subprocess |
| Streaming — true incremental-to-LLM | ⬜ deferred | needs `connector` contract + execution-envelope + MCP partial-result changes; its own phase |
| Sandbox — rlimit (mem/CPU/files) | ✅ done | plugin SDK self-applies `WICK_PLUGIN_RLIMIT_AS_MB`/`_CPU_SEC`/`_NOFILE` at startup (linux/android), soft-fail |
| Sandbox — cgroups / namespaces / uid-drop | ⬜ out of scope | infeasible on Termux non-root / single-user |

## Follow-on items (beyond §14)

| Item | PLAN ref | Status | Notes |
|---|---|---|---|
| **`<app> plugin` CLI (consumption)** | §6.1 | ✅ done | `app/plugin_cmd.go` — `install`/`list`/`enable`/`disable`/`remove`; wires `SetEnabled` + `VerifyManifest`; reloader poller reconciles. See SHIPPED section at top |
| **go.work + CI integration** | §21 | ✅ done | repo-root `go.work` (`use . ./plugins`) builds the nested module against local `pkg/plugin`; release.yml `unit-tests` job builds the scaffold (`connector/_template` by explicit path — `_`-dirs are invisible to `go ./...`) so a `pkg/plugin` change that breaks the author contract fails CI |
| Marketplace catalog (raw JSON, NOT GitHub API) | §6.2 | ✅ done | `internal/connectors/plugin/registry.go` — `Catalog.List/Resolve` fetches a single curated `plugins.json` from the plugins default branch via **raw.githubusercontent.com** (no API → no rate limit, no token), ETag + 15min cache. Each entry carries per-os/arch release download URLs; binary pulled only on install. Env: `WICK_PLUGIN_CATALOG`. Tests in `registry_test.go` |
| Admin UI: marketplace in the connector list | §6.2 | ✅ done | Merged into `ConnectorsIndex.svelte` (ONE list, not a separate page): built-in + downloaded plugins render as normal cards; catalog entries not yet installed show under "Available to install" with a Download button → `installPlugin` → reloader registers within ~5s. Backend: `internal/manager/plugins_api.go` (admin-gated GET/install/enable/disable/remove). FE builds clean |
| Plugin binary size | §8/§9 | ✅ addressed | `wick plugin build` strips `-s -w` (19→13MB). Decoupled `pkg/connector`/`pkg/job` from `pkg/tool` (moved `DefaultTag`→`pkg/entity`, alias kept) so plugins no longer drag templ/HTML stack. 13MB is the gRPC+protobuf+go-plugin floor (Terraform providers are 15-30MB) — can't go lower without dropping gRPC. (Further size cuts deferred per user) |
| proto_version range negotiation | §19.2 | ✅ done | `MinProtoVersion..ProtoVersion` inclusive range; `VerifyManifest` range-checks; `SupportedProtoVersions()` builds `VersionedPlugins`; `ProtoVersionSupported()`. Adding a future v2 = register its descriptor + bump the const. Tests in `handshake_test.go` |
| Generalize platform: tool + job kinds | §18 | 🟡 contract done | `Manifest.Kind` (connector\|tool\|job) + `NormalizeKind`; `wick plugin build --kind` stamps it; loader/reloader route by kind (skip non-connector in the connectors dir). Reuses the Connector gRPC service for all kinds (no new proto — protoc unavailable). **Deferred:** host-side tool/job execution adapters (their own runners) |
| Polyglot plugins (non-Go) | §16 | ✅ documented | `plugins/POLYGLOT.md` — full language-agnostic contract: go-plugin handshake line format, magic cookie, AutoMTLS, the proto service, hand-written manifest. Implementable today; no Go-specific assumption in the wire protocol |
| cosign signing | §13, §15 | ✅ done | `wick plugin build --cosign-key` signs the binary via the **external cosign CLI** (sidecar `.sig`/`.pem` in the zip), soft-skips with a warning if cosign is absent. Intentionally NOT a sigstore-go dependency — that would re-bloat every plugin binary. ed25519 manifest signing (`--sign-key`) unchanged |
| **`plugins` monorepo starter** | §21 | 🟡 in-tree | scaffold at `plugins/` (nested module). Repo-root `go.work` wires it to this checkout's `pkg/plugin` — so its `go.mod` is **clean (no `replace`)** and extract-ready. `connector/_template/` (working connector), README, CI. **Still to do:** extract to its own repo once a wick release contains `pkg/plugin` (then bump the `require` to that version; nothing else changes) |
| **`wick plugin build` subcommand** | §21.2 | ✅ done | `cmd/cli/plugin.go`; generic over `--kind connector\|tool\|job`; compiles `<kind>/<name>/`, `--dump-manifest`, **zip** output; `--all` cross-build, `--changed --since`, `--all-plugins`, `--sign-key`. Round-trip test in `cmd/cli/plugin_test.go`. (NOT `wick build connector` — generic + on `plugin` group) |
| CI/CD: changed-only build & release + auto-catalog | §21.4 | ✅ done | `.github/workflows/release-plugins.yml` (ROOT). **Gated like core: PR → `release`** (not push), `paths: plugins/connector\|tool\|job/**`; core `release.yml` gets `paths-ignore: plugins/**` → plugin-only PR runs only plugin pipeline, core-only only core, mixed both. Chain: **`guard` (reject fork + head=master + author=admin via ADMIN_TOKEN) → `detect` (PR base..head diff) → `test` (hard gate) → `build` (matrix, idempotent skip if tag exists, ADMIN_TOKEN release) → `update-catalog` (checks out master, regenerates `plugins.json` from releases API + backfills name/desc from released zips, pushes master with ADMIN_TOKEN, `[skip ci]`)**. Audited twice (correctness + security): fork-PR/secret-exfil closed by `guard`, pipefail-crash + non-numeric-version-jq-crash fixed, darwin cross-build verified (pure Go). Caveat: don't make path-filtered workflows required checks; needs `ADMIN_TOKEN` secret. See `plugins/RELEASE.md` |
| Core binary slimming (build-tags) | — | ⬜ dropped | investigated: connectors are only ~1MB of the 45MB binary (substrate stays); real weight is embedded frontend assets (~22MB). Not worth it for connectors |

## Config knobs shipped

`WICK_PLUGINS_DIR`, `WICK_PLUGIN_PUBKEY`, `WICK_PLUGIN_REQUIRE_SIGNATURE`,
`WICK_PLUGIN_MAX_PROCS`, `WICK_PLUGIN_QUEUE_TIMEOUT`, `WICK_PLUGIN_SOCKET_DIR`,
`WICK_PLUGIN_WARM`, `WICK_PLUGIN_RLIMIT_AS_MB`, `WICK_PLUGIN_RLIMIT_CPU_SEC`,
`WICK_PLUGIN_RLIMIT_NOFILE`.
