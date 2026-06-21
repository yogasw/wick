# Connector Plugin Platform — Progress Tracker

Status of the [PLAN.md](./PLAN.md) roadmap (§14) and follow-on items. Legend:
**✅ done** · **🟡 partial** · **⬜ not started**.

> Implementation lives on branch `feat/connector-plugin-platform-phase0-1`
> (Phases 0–4, ~56 commits). Host execution envelope (`internal/connectors/service.go`,
> `registry.go`, `enc.go`, `pkg/connector/connector.go`) is intentionally untouched —
> connectors plug in via the existing `op.Execute` seam (adapter pattern).

## Roadmap (PLAN.md §14)

| Fase | Scope | Status | Notes |
|---|---|---|---|
| 0 | proto + handshake + plugin manager (`hashicorp/go-plugin`) | ✅ done | gRPC contract `pkg/plugin/proto`, AutoMTLS over UDS |
| 1 | 1 pilot connector ported + measure on Termux | 🟡 partial | pilots **slack + googleworkspace** built in-tree (`cmd/plugins/`); warm IPC ~222µs measured on amd64. **Pending:** run `measure.md` protocol on Termux/arm64 |
| 2 | Registry + manifest + verify checksum/sig | ✅ done | signed manifest envelope (`pkg/plugin/manifest.go`), ed25519 (`signing.go`), `VerifyManifest`, `wick plugin install/list/remove` |
| 3 | Lifecycle: lazy spawn, idle-kill, cap concurrent, LRU evict, enable/disable | ✅ done | lease model + in-flight tracking; cap+bounded-queue; crash backoff/circuit-breaker; UDS hardening; DB enable/disable overlay (`plugin_states`) + `wick plugin enable/disable` |
| 4 | Sandbox + streaming + warm pool | ✅ done (scoped) | see honest scoping below |
| 5 | Buka marketplace pihak ketiga | ⬜ not started | discovery via **GitHub Releases API** (curated registry repo, §6.2/§16) — NOT static index.json |

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
| Marketplace via GitHub Releases API | §6.2, §16, §20.4 | ⬜ not started | curated registry repo (e.g. `yogasw/wick-connectors`); metadata-only until install; cache + ETag |
| Admin UI: browse marketplace + enable/disable toggle | §6.2 | ⬜ not started | FE-heavy; pairs with Fase 5 |
| Connector repo extraction (out-of-tree) | §5 | ⬜ not started | pilots currently in-tree under `cmd/plugins/` |
| cosign / keyless signing | §13, §15 | ⬜ not started | currently ed25519 (stdlib) |
| Polyglot plugins (non-Go) | §16 | ⬜ not started | go-plugin gRPC supports it; contract is language-agnostic |
| Generalize platform: tools + jobs kinds | §18 | ⬜ not started | shared platform, per-kind contract |
| proto_version range negotiation | §19.2 | 🟡 partial | single proto version handshake done; range nego not |
| CI/CD auto-build & release plugins | §20 | ⬜ not started | per-connector pipeline + proto-bump automation |
| Core binary slimming (build-tags) | — | ⬜ dropped | investigated: connectors are only ~1MB of the 45MB binary (substrate stays); real weight is embedded frontend assets (~22MB). Not worth it for connectors |

## Config knobs shipped

`WICK_PLUGINS_DIR`, `WICK_PLUGIN_PUBKEY`, `WICK_PLUGIN_REQUIRE_SIGNATURE`,
`WICK_PLUGIN_MAX_PROCS`, `WICK_PLUGIN_QUEUE_TIMEOUT`, `WICK_PLUGIN_SOCKET_DIR`,
`WICK_PLUGIN_WARM`, `WICK_PLUGIN_RLIMIT_AS_MB`, `WICK_PLUGIN_RLIMIT_CPU_SEC`,
`WICK_PLUGIN_RLIMIT_NOFILE`.
