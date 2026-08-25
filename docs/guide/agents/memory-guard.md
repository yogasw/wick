---
outline: deep
---

# Memory Guard

The agent pool ([Pool & Sessions](./pool)) caps how many agent **processes** run at once. It has no idea how much RAM any one of them uses — an idle agent at ~150 MB and one driving a browser at ~2 GB count as the same slot. A single runaway agent (a browser leak, a bad loop) can take the whole machine down with it, wick included.

The memory guard adds a second axis: byte limits, enforced by the kernel, per agent. It ships **off** — a fresh install and an upgraded one behave identically until you opt in.

::: info Source
Config + arithmetic: [`internal/agents/config/memguard.go`](https://github.com/yogasw/wick/blob/master/internal/agents/config/memguard.go).
Mechanism: [`internal/agents/provider/memscope/`](https://github.com/yogasw/wick/blob/master/internal/agents/provider/memscope) (systemd scopes and the raw-cgroupfs fallback), [`internal/agents/provider/memguard.go`](https://github.com/yogasw/wick/blob/master/internal/agents/provider/memguard.go) (wiring into spawn).
Measurement: [`internal/pkg/memreport/`](https://github.com/yogasw/wick/blob/master/internal/pkg/memreport) (live `/proc` reads + history buffer).
:::

## Linux only

Enforcement needs a Linux kernel. Off Linux — Windows, macOS — the guard degrades cleanly: no limit is applied, and both the CLI and the Resources page say so plainly instead of silently doing nothing.

On Linux, wick probes two mechanisms in order at first use and caches whichever works, by actually creating and tearing down a throwaway group rather than guessing from paths or permissions:

| Backend | Requires | Notes |
|---|---|---|
| **systemd** (preferred) | a reachable systemd user session (`systemd-run --user --scope`) | Scopes are reaped automatically (`--collect`), and cgroup v2's `memory.events` gives a real per-scope OOM-kill counter. |
| **cgroupfs** (fallback) | a writable `/sys/fs/cgroup/memory` (cgroup v1) | No systemd needed — the kernel is enough. Used on hosts whose PID 1 isn't systemd (a Fly.io Machine, a bare container, Termux without linger). |
| none | — | Agents run unguarded; only `/proc` measurement remains. |

The cgroupfs fallback enforces exactly as hard as the systemd path does — `memory.limit_in_bytes` is a real kernel ceiling. What it can't do is *report* as confidently: cgroup v1 has no counterpart to v2's `oom_kill` counter, so a scope's peak is readable but a kill can't be positively confirmed. The exit reason falls back to the generic one rather than inventing a kill it can't prove (see [What happens when an agent is killed](#what-happens-when-an-agent-is-killed)).

Because nothing on a systemd-less host performs "create a cgroup, join it, then exec the agent" the way `systemd-run` does, wick re-execs its own binary through a hidden `__agent-exec` subcommand to do it. That's an internal implementation detail — never something to type by hand — but it's what you'll see in a process list on such a host.

Measurement (`wick memory report`, the Resources page, Usage History) reads `/proc` directly and works everywhere the guard doesn't, including with the guard switched off.

## Modes

Set via `memory_guard_mode` under **Agents settings → Memory Guard**:

| Mode | What it does |
|---|---|
| `off` (default) | No slice, no `oom_score_adj`, no argv wrapping. Byte-identical to a build without this feature. |
| `measure` | Each agent runs in its own scope so its usage can be read, but nothing is limited. Use this first to learn real numbers on this machine. |
| `enforce` | The kernel kills an agent that exceeds its limit. Every other agent and wick itself are untouched. |

`memory_guard_method` picks who applies the limit: `auto` (wick applies it when a backend is available — the default) or `wrapper` (something outside wick already wraps the agent binary; wick only measures and reports). Running both at once is safe — the kernel enforces the tightest ceiling in the hierarchy, so migrating from one to the other never leaves a gap.

Choose `wrapper` when something *outside* wick must be limited too — a `claude` you launch by hand in a terminal is not a child of wick, so no wrapping wick does can reach it. Be aware of what wick then stops doing, since it goes beyond the per-agent ceiling:

| | `auto` | `wrapper` |
|---|---|---|
| Per-agent ceiling | wick applies it | your wrapper applies it |
| Combined ceiling on `agents.slice` | ✅ | ❌ never written |
| CPU weight / quota / TasksMax / IOWeight | ✅ | ❌ never written |
| OOM exit reason naming peak + limit | ✅ | ❌ reported as a plain stop |
| `protect_wick_from_oom` | ✅ | ✅ |
| Measurement, admission, Resources page | ✅ | ✅ |

A third value, `scope`, was previously documented as "wick always applies it". It never behaved differently from `auto` — the code only ever branched on `wrapper` — so it is no longer offered. A config that already stores it keeps working and is treated as `auto`.

## Recommended path

1. Leave the guard `off` (or set `measure`) and run `wick memory report --watch 30s --for 1h` while agents do real work, or just leave **Usage History** on (default) and check the [Resources page](#resources-page) after a while.
2. Read the suggested limits (from measured peaks, or from `DeriveMemoryDefaults` if nothing was observed) — either from the CLI output or the Resources page's **Apply** button.
3. Switch `memory_guard_mode` to `enforce`.

## Limits

All under **Agents settings → Memory Guard**, all in MB, `0` = no limit:

| Setting | What it bounds |
|---|---|
| `agent_memory_max_mb` | One agent, counting its whole process tree (browsers, tools, scripts it starts). A provider instance's own **memory limit** field ([Providers](./providers)) overrides this per-instance — it may set a value *higher* than the global default, so one heavy instance doesn't force every other agent's ceiling up too. |
| `agents_total_memory_mb` | Combined ceiling across all running agents — a backstop for several well-behaved agents adding up to more than the machine has. |
| `tool_memory_max_mb` | One shell command the built-in [`wick` provider](./providers#built-in-wick-provider) runs itself (its own tool calls run as real subprocesses, unlike `claude`/`codex`/`gemini`, whose tool calls happen inside the CLI process). Exceeding it fails just that command with an error the agent can react to — the agent keeps running. |
| `min_free_memory_mb` | Queue a new agent instead of starting it while free system memory is below this. Prevents the spawn that pushes the machine over the edge. |

`agent_memory_max_mb`/`agents_total_memory_mb`/`tool_memory_max_mb`/`min_free_memory_mb` default to `0` in a struct literal but are derived from detected RAM at first boot (`DeriveMemoryDefaults`) rather than guessed — the correct values depend on the machine.

### Per-instance memory limit

Each provider instance ([Providers](./providers)) has its own **memory limit (MB)** field, independent of `MaxConcurrent`. Unlike the concurrency cap (which resolves as `min(instance, global)` because slots share one pool), the memory ceiling resolves as "instance value if set, else the global default" — it **may exceed** the global default, since a memory ceiling is per-process, not a shared pool slot.

### Contention controls (enforce mode only)

Written onto the shared `agents.slice`. Memory is the only control that kills; these shape how agents *compete* — with wick and with each other. All default to `0` = kernel default:

::: warning systemd backend only
These are systemd unit properties. On the cgroupfs fallback only the memory controls apply — the aggregate memory ceiling is written onto the slice directly, but CPU weight/quota, tasks max, and IO weight are not.
:::

| Setting | What it does |
|---|---|
| `agents_cpu_weight` | CPU priority of agents vs. the rest of the system when CPU is contended (kernel default weight 100). Ships at `50` — agents yield to wick under load, full speed when idle. Never caps. |
| `agents_cpu_quota_pct` | Hard cap on combined agent CPU as a percentage of one core (100 = one core). Off by default — a cap slows legitimate work even on an idle machine. |
| `agents_tasks_max` | Max processes + threads across all agents. Ships at `512` — stops a fork bomb while staying generous for real work. |
| `agents_io_weight` | Disk-access priority, same idea as CPU weight. |

### OOM protection

`protect_wick_from_oom` (default **on**) tells the kernel to prefer killing an agent over wick itself when the machine as a whole runs out of memory. Applies only in `enforce` mode.

## What happens when an agent is killed

cgroup v2's `memory.events` exposes two different counters, and wick reads them separately because they mean different things:

- **`oom`** — this scope hit its *own* limit. The agent's exit reason names both numbers, and the exit is not auto-restarted — restarting would just hit the same ceiling again:

  > killed by the kernel for exceeding its 2000 MB memory limit. Raise the limit in provider settings, or split the work into smaller steps.

  (or, with a measured peak available: "used 2.3 GB, over its 2000 MB limit. …")
- **`oom_kill` without a matching `oom`** — the *machine* ran out of memory and the global OOM killer picked this agent, even though it never crossed its own ceiling. This stays a retryable error, and the message points at host-level remedies instead of the per-agent limit that was never actually hit:

  > used 1.2 GB when the kernel killed it because the machine ran out of memory — its own limit was not hit. Free up memory on the host, lower the combined agent limits, or run fewer agents at once.

Both are surfaced in the chat and the spawn log — `exit_reason: "oom"` for the own-limit case, `"error"` for the host-OOM case, since it's retried the same as any other error.

Telling the two apart needs a per-scope OOM counter, which only cgroup v2 (the systemd backend) has. On the [cgroupfs fallback](#linux-only) the agent is still killed by the kernel, but the exit reason stays the generic "agent stopped" one rather than claiming a kill it can't prove. Check the Resources page or `wick memory report` for the peak in that case.

## Usage History

Independent of the guard mode — measuring is how you learn what to set, so it runs with the guard `off`. Settings under **Agents settings → Usage History**:

| Setting | Default | What |
|---|---|---|
| `resource_history_enabled` | on | Record memory/CPU/disk samples per agent. Off = the Resources page shows only a live snapshot, no trends. |
| `resource_sample_interval_sec` | 15 | Seconds between samples. Shorter catches brief spikes (a browser opening) but stores more points. |
| `resource_retention_minutes` | 360 (6h) | How long to keep samples; older ones are discarded automatically. |
| `resource_history_max_points` | 4096 | Hard ceiling on stored samples regardless of retention — protects against a very short interval filling memory. |

## Resources page

`/tools/agents/resources` — **admin only** (nav link and its `GET /api/memory`, `GET /api/memory/series`, `GET /api/processes`, `POST /api/memory/apply-suggested`, `POST /api/processes/kill` endpoints), since it reports machine-wide process usage and can act on it.

Shows a live per-agent table (memory, CPU, disk), trend charts backed by the history buffer, and suggested limits with an **Apply** button that writes them straight into Memory Guard settings. On a machine where no backend works at all — neither a systemd user session nor a writable cgroup filesystem — the page says so plainly instead of showing numbers that imply protection that isn't there.

A searchable, paginated **process explorer** lists every process on the machine, grouped by executable and ranked by current CPU/memory rate. Each row can be ended from its menu — the one destructive action on the page:

- never ends wick itself: the row is marked protected up front (the list response carries `self_pid`) rather than offering a button that would silently refuse
- never ends PID 1 (init)
- ending every process in a name group stops at 25 and says what it skipped, since "end 40 things" is a bigger action than one click communicates

Ending a process asks it to close on unix (`SIGTERM`) but ends it outright on Windows (`TerminateProcess`) — Windows has no equivalent for asking an arbitrary, uncooperative process to shut down cleanly.

## `wick memory report` (CLI)

A calibration tool, separate from the guard: it reads `/proc` directly, so it works with the guard off, on a machine that has never been configured, and on platforms without scope isolation (it just reports; it can't enforce there).

```bash
wick memory report                      # one snapshot
wick memory report --watch 30s          # sample every 30s for the default 1h, report peaks
wick memory report --watch 30s --for 6h # sample for a custom duration
```

Output includes the machine's total/available memory, one row per detected agent process tree (`claude`, `codex`, `gemini`, `node`, `python3`) with its total subtree size and heaviest descendant (e.g. "chromium 1.2 GB"), and a **Suggested settings** block with headroom added on top of any observed peak (30%) so a limit set from measurement doesn't kill the very next run that does slightly more work.

## Systemd service restart limit

Unrelated to the guard's own mechanism, but installed alongside it: the systemd user unit `wick install service` writes now includes `StartLimitIntervalSec=300` / `StartLimitBurst=3`. Without a ceiling, `Restart=on-failure` turns a single OOM kill into an invisible loop (wick dies, systemd restarts it, the agent respawns, memory balloons again); three failures in five minutes now leaves the unit `failed` instead, turning a silent loop into a visible incident.

## See also

- [Providers](./providers) — per-instance memory limit field, alongside `Binary`/`Env`/`MaxConcurrent`.
- [Pool & Sessions](./pool) — the process-count axis this feature complements.
