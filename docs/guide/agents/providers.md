---
outline: deep
---

# Providers

A **provider type** is an AI CLI: `claude`, `codex`, `gemini` ([provider.go:36-40](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go#L36)).
A **provider instance** is a configured copy of one. Multiple instances per type are supported — same `claude` binary, different env vars (e.g. two PATs).

::: info Source
Code: [`internal/agents/provider/`](https://github.com/yogasw/wick/blob/master/internal/agents/provider).
UI handler: [`internal/tools/agents/providers.go`](https://github.com/yogasw/wick/blob/master/internal/tools/agents/providers.go).
:::

## Why multi-instance

The use case is mundane: you have a personal Anthropic PAT and a work one. Both target the same `claude` binary. You want to pick which to use per session.

Each instance carries:

| Field | Notes | Source |
|---|---|---|
| `Type` | `claude` / `codex` / `gemini`. | [provider.go:34](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go#L34) |
| `Name` | Unique within type. Defaults to `Type` itself. Pick anything: `work`, `personal`, `staging`. | [provider.go:53](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go#L53) |
| `Binary` | Absolute path. Empty = let wick resolve via PATH + scan. | [provider.go:54](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go#L54) |
| `ExtraArgs` | Appended to every spawn argv. | [provider.go:55](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go#L55) |
| `Env` | Extra env vars. **This is where `ANTHROPIC_API_KEY` goes for a per-instance PAT.** | [provider.go:56](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go#L56) |
| `Disabled` | Toggle without deleting. | [provider.go:57](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go#L57) |

The default seed: when the instance list is empty, [`Load`](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go#L89) auto-creates one default per type whose `Name` equals the type. So a fresh install always shows three cards (`claude/claude`, `codex/codex`, `gemini/gemini`).

## Web UI

> **📸 Screenshot needed:** `agents-providers-list.png` — capture `/tools/agents/providers` showing the three default cards (claude / codex / gemini) with version + path resolved, plus the "Add Instance" + "Rescan all" + "Auto-rescan" header. Save to `docs/public/screenshots/agents-providers-list.png`.

> **📸 Screenshot needed:** `agents-provider-edit.png` — open Edit on one provider card, capture the form with Binary, ExtraArgs, Env (showing `ANTHROPIC_API_KEY=...` placeholder), Disabled toggle. Save to `docs/public/screenshots/agents-provider-edit.png`.

What each card shows ([Status struct](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go#L70)):

- **Path resolved** — where wick found the binary. Source label: `registry` / `path` / `scan` / `miss`.
- **Version** — first line of `<bin> --version`.
- **Last probed** — when the cache was last filled.
- **Edit / Rescan / Delete** buttons per card.
- **Add Instance** for a new named profile of the same type.

The page also surfaces a [Gate Status card](../command-gate#diagnostics) and a recent spawns table (filterable by type/name/session).

## Binary resolution chain

Both the UI probe and the spawn site walk the same chain. First hit wins:

| Step | What it checks | Source |
|---|---|---|
| 1. **registry** | `Instance.Binary` set in the UI form. Used as-is, no PATH lookup. | [provider.go:62](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go#L62) (`Bin()`) |
| 2. **path** | `exec.LookPath(<type>)` against `%PATH%` + `PATHEXT` (Windows). | |
| 3. **scan** | Known install locations the installer drops but doesn't always wire into PATH. | [scan_unix.go](https://github.com/yogasw/wick/blob/master/internal/agents/provider/scan_unix.go), [scan_windows.go](https://github.com/yogasw/wick/blob/master/internal/agents/provider/scan_windows.go) |
| 4. **miss** | All three failed. Probe reports `PathFound=false`; spawn falls back to bare type name and fails at `Start()`. | |

### Why scan exists

Tray-launched wick inherits `PATH` from Explorer / login session, **not from your shell**. So installer-modified `PATH` (npm prefix, claude installer) is often invisible to the tray even though `where claude` works in your terminal. The scan step closes that gap without making you edit `Binary` manually.

**Windows scan** ([scan_windows.go](https://github.com/yogasw/wick/blob/master/internal/agents/provider/scan_windows.go)): npm root list (`%APPDATA%\npm`, `C:\nvm4w\nodejs`, nvm-windows, fnm, volta, `Program Files\nodejs`) cross-product with `.cmd` / `.exe` extensions. Plus per-type installer paths — Claude: `~/.local/bin`, `LOCALAPPDATA\Programs\claude`, `Program Files\Claude`.

**macOS / Linux scan** ([scan_unix.go](https://github.com/yogasw/wick/blob/master/internal/agents/provider/scan_unix.go)): per-user bin (`~/.local/bin`, `~/.npm-global/bin`, pnpm/yarn/volta/asdf/bun) → glob versioned dirs (`~/.nvm/versions/node/*/bin`, fnm Linux + macOS, asdf shims) → system bin (homebrew Apple Silicon + Intel, MacPorts, distro `/usr/bin`).

Order: per-user bin → versioned managers → system bin. First hit wins.

## Status cache

`--version` probing on Node-shimmed CLIs (codex / gemini `.cmd`) takes 1–3 seconds because Node has to start. Three providers in sequence on a cold boot would block the Providers page for nearly 10 seconds.

Wick persists status in `~/.<app>/config.json` under `provider_statuses` (keyed `<type>/<name>`). The page render path **never** spawns `--version` — it always reads the cache. Cache misses render an empty card and trigger a background rescan; the next reload shows the result.

::: info Code reference
Cache logic: [`status_cache.go`](https://github.com/yogasw/wick/blob/master/internal/agents/provider/status_cache.go).
The `LoadCached` invariant ("page render never blocks on probe") is what stopped the page-hang race that earlier in-memory caches couldn't fix on cold boot.
:::

| Trigger | Action |
|---|---|
| Server boot | Background `RescanAll` (30s timeout) — primes the cache once. |
| Open Providers page | `LoadCached`. Miss = empty card now, fill in background. |
| Save / delete instance | Background `RescanOne` (10s) auto-fired by [Save](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go#L143). |
| **"Rescan all"** header | Sync `RescanAll` (30s) + 303 redirect. |
| **"Rescan"** per card | Sync `RescanOne` (15s) + 303 redirect. |
| Auto-rescan on + entry stale > 24h | Background `RescanOne`; current render still uses cached value. |
| `auto_rescan` off | No background refresh. Manual Rescan only. |

Toggle auto-rescan from the Providers page header. The wired closure pattern ([provider.go:`SetAutoRescanLookup`](https://github.com/yogasw/wick/blob/master/internal/agents/provider/provider.go)) keeps the provider package zero-dep on HTTP / configs stack.

## Hide console windows on Windows

Windows console-subsystem children (`claude.exe`, `codex.exe`, npm shims) spawned from a parent without an attached console (tray app) make Windows allocate a fresh console window → flash + auto-close. Solution: `SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}` (CREATE_NO_WINDOW).

Pattern lives in two spots:

- `--version` probe — [provider/hide_console_windows.go](https://github.com/yogasw/wick/blob/master/internal/agents/provider/hide_console_windows.go)
- Long-lived spawn — [provider/claude/hide_console_windows.go](https://github.com/yogasw/wick/blob/master/internal/agents/provider/claude/hide_console_windows.go)

Same pattern is used by `internal/systemtray/{editor,notify}_windows.go`. Dev mode (`go run` from a shell) has an attached console → child inherits → no flash; CREATE_NO_WINDOW is safe to apply universally.

## Spawn log

Every spawn writes a JSONL file under `~/.<app>/agents/providers/spawns/`:

```
<type>__<name>__<session>__<unix-ms>.jsonl
```

Two events per spawn: `start` (with PID, argv, binary, first user message) and `exit` (status, duration). Filename encoding lets `ls` filter by type / name / session without reading file bodies. Stable across restart, friendly to `tar`.

Source: [`spawnlog.go`](https://github.com/yogasw/wick/blob/master/internal/agents/provider/spawnlog.go).

The Spawn detail page in the UI (link from the recent-spawns table) renders the start + exit events plus the resolved provider source label.

## Spawn / probe log keys

Prefix-consistent so `grep "agents."` against the server log traces one spawn end-to-end:

| Log key | Site | Fields |
|---|---|---|
| `agents.probe: resolve` | `provider.Probe` (debug) | `type, name, path, source (registry\|path\|scan\|miss), found` |
| `agents.probe: ok` | `provider.Probe` (debug) | `type, name, version` |
| `agents.probe: --version failed` | `provider.Probe` (warn) | `type, name, path, err` |
| `agents.spawn: resolve provider` | `pool.Build` (info) | `session, provider_type, provider_name, binary, source` |
| `agents.spawn: starting` | `claude.Spawn` (info) | `bin, argv, cwd, resume` |
| `agents.spawn: started` | `claude.Spawn` (info) | `pid, bin` |
| `agents.spawn: start failed` | `claude.Spawn` (error) | `bin, err` + hint to set `Binary` |

These land in `~/.<app>/logs/server-YYYY-MM-DD.log` (zerolog's global logger initialized at server boot, not the tray).

## See also

- [Workspaces](./workspaces) — `default_provider` field per workspace.
- [Pool & Sessions](./pool) — how `provider_type` / `provider_name` are forwarded to the spawner.
- [Command Gate](../command-gate) — gate sidecar lives next to the main binary, separate from providers.
