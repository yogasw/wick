---
name: wick-resource-limits
description: Use when the user asks about agent memory or CPU limits, an agent or build being killed, paused or OOM, the Resource Guard watchdog, the Resources page, usage history, disk space, why a spawn is queued, or how to cap what an agent may consume. Covers the three guard modes and what each actually does, which platform enforces what, how to read the four separate measurements, and the rules for suggesting limits safely.
---

# Agent resource limits

Wick can cap how much memory, CPU, and IO its agents consume, and records what they actually used. Enforcement is done by the **kernel**, not by wick sampling and killing — a sampler cannot catch a process that allocates a gigabyte between two samples.

Two things follow from that, and they explain most questions about this subsystem:

- **Enforcement needs cgroups, so it is Linux-only.** Measurement, reporting, and crash recovery work everywhere.
- **A killed agent is killed by the kernel.** Wick's job is to report it accurately, not to prevent it after the fact.

The one exception is the **Resource Guard** watchdog (Linux): a 1-second loop over the whole host that stops agent work when CPU or memory nears a hang, before the kernel acts at the cliff edge — on a small host without swap the kernel may never act and the machine just freezes. See "The fast watchdog" below.

## The three modes

`memory_guard_mode`, default `off`:

| Mode | Writes limits | Kills anything | Records usage |
|---|---|---|---|
| `off` | no | no | no |
| `measure` | **no** | **no** | **yes — fully** |
| `enforce` | yes | kernel kills the offender | yes |

The distinction that trips people up: **`measure` records everything and applies nothing.** It is not a dry run that logs what it *would* do — it is real measurement with no ceilings. That makes it the correct first step: run in `measure` for a while, look at the observed peaks, then set limits from real numbers rather than guesses.

Do not describe `measure` as "not doing anything". It is doing the measuring; it is only withholding the enforcement.

## What each platform can actually do

Answer from this table rather than inferring from "Linux-only":

| Capability | Linux + systemd | Linux, no systemd | Windows | macOS |
|---|---|---|---|---|
| Per-agent memory ceiling | yes | yes | no | no |
| Aggregate ceiling for all agents | yes | yes | no | no |
| CPU weight / quota / task cap | yes | no | no | no |
| Naming an OOM kill with its peak | yes | no | no | no |
| Peak readable in `measure` mode | yes | yes | no | no |
| Agents die before wick does | yes | yes | no | no |
| Queue a spawn when RAM is low | yes | yes | no | no |
| Machine memory total / available | yes | yes | yes | no |
| Process listing | yes | yes | yes | no |
| Disk capacity | yes | yes | yes | yes |
| Usage history + Resources page | yes | yes | yes | partial |
| **Crash recovery + respawn** | yes | yes | yes | yes |

"Cannot enforce" is not "does not work". On Windows and macOS the Resources page, history, and crash recovery all still function — only the ceilings are unavailable.

A container without systemd still enforces memory: there is a raw-cgroupfs path. It loses the CPU/task controls (those are systemd unit properties) and cannot name an OOM kill with a measured peak, but the memory ceilings hold.

## The config knobs

Under **Resource Guard** (formerly labelled Memory Guard — the keys did not change) and **Usage History** in the agents config:

| Key | Default | Meaning |
|---|---|---|
| `memory_guard_mode` | `off` | `off` / `measure` / `enforce` |
| `memory_guard_method` | `auto` | leave on `auto` unless diagnosing |
| `agent_memory_max_mb` | derived | per-agent ceiling; `0` = none |
| `agents_total_memory_mb` | derived | ceiling for all agents together; `0` = none |
| `tool_memory_max_mb` | derived | ceiling for one shell command run by the wick provider |
| `min_free_memory_mb` | derived | queue a spawn when free RAM is below this; `0` = off |
| `protect_wick_from_oom` | `true` | make agents die before wick itself (enforce only) |
| `agents_cpu_weight` | `50` | relative CPU share vs the rest of the machine |
| `agents_cpu_quota_pct` | `0` (off) | hard CPU cap, % of ONE core (140 on 2 cores = 70% of the machine) |
| `agents_tasks_max` | `512` | process-count cap — the fork-bomb guard |
| `agents_io_weight` | `0` (off) | relative block-IO share |
| `resource_guard_interval_ms` | `1000` | watchdog sampling interval |
| `resource_guard_exhaust_horizon_sec` | `20` | act when free memory is projected to run out within this |
| `resource_guard_safe_pct` | `80` | stop acting once host CPU and memory are both below this |
| `resource_guard_cpu_psi_max` | `60` | CPU pressure (some avg10, %) that, with a fully busy CPU, means near a hang |
| `resource_guard_action` | `kill` | `off` / `log` / `pause` / `kill` — how far the watchdog may go in enforce |
| `resource_history_enabled` | `true` | record usage samples |
| `resource_sample_interval_sec` | `15` | seconds between samples |
| `resource_retention_minutes` | `360` | how long samples are kept |
| `resource_history_max_points` | `4096` | hard cap on stored samples |

Defaults for the derived values come from the machine's own size, so they are sane without tuning.

CPU weight, CPU quota and tasks max **apply live**: change them in the UI and the watchdog writes them onto the running agents.slice within a second (and into the slice unit, so a reload keeps them). No restart and no new spawn needed — but only in `enforce` mode.

`agents_tasks_max` deserves a mention because it catches something no memory knob can: thousands of tiny processes cripple the scheduler while staying comfortably under every memory ceiling.

A per-instance `MemoryMaxMB` overrides the global per-agent value when set. Note it does **not** take the smaller of the two — a memory ceiling is per-process, not a share of a pool, so an explicit per-instance value wins outright.

## The fast watchdog (Resource Guard)

A core wick feature, not a script: it covers **every agent wick spawns** — sessions, sub-agents, Team agents, workflow agent nodes — and everything they start, including `systemd-run --user` units (`run-*`), which land in app.slice outside every agents.slice limit. It never acts on the agent CLI itself (claude, codex, …) or on wick.

One rule: **when the host nears a hang, stop agent child processes one at a time until host CPU AND memory are both under the safe line** (`resource_guard_safe_pct`, default 80%). Measured on the whole host, not just agents.slice — a build that escaped the slice still fills the machine.

**Near a hang** means any of:
- memory ≥ 90% used, under `min_free_memory_mb`, or projected (regression over the last 10 s) to run out within `resource_guard_exhaust_horizon_sec` while above the safe line, or memory pressure "full" > 20%;
- CPU ≥ 95% busy (from `/proc/stat` deltas, not load average) **and** tasks queueing (CPU pressure some avg10 > `resource_guard_cpu_psi_max`, default 60, or runnable tasks > 2× cores) for 10 s. Busy with nothing queueing is a build using idle CPU and is left alone;
- wick's own 1-second loop running a second late — acted on at once.

**What it does**, every ~2 s until safe: memory high → kill the agent child with the most resident memory; otherwise → the child that used the most CPU since the last sample (not `ps %cpu`, which averages over the process's life). Children are build tools, test runners, browsers and scripts. Only when none is left does it stop the heaviest agent — its conversation is kept and resumes on the next message. For CPU it first caps agents.slice at 100% briefly; the configured quota is restored after 30 s calm.

**Modes.** `memory_guard_mode = enforce` → acts (default action `kill`); `measure` → records what it *would* stop and stops nothing; `off` → no watchdog. `resource_guard_action = pause` pauses/freezes instead of killing (resumed after 30 s calm); `log` records only.

Every action is written as a system line into the history of the session whose agent was affected (e.g. "wick stopped `node vitest` (1.2 GB) to keep the host alive (CPU 97%, memory 91%)"), and listed under **Resource Guard** on the Resources page with the safe line and the CPU quota as a share of the machine.

**Spawn gate.** While the host is near a hang or memory is heading there, new agents are queued (not refused); the queue is re-offered every few seconds and starts once the host recovers.

**Sub-agent queue.** `sub_agents_max_parallel` is per *conversation*: every delegate, mention and `continue` from one conversation shares the same slots; extra work is queued in order.

**Config check.** `agents_total_memory_mb` is refused when it plus wick's reserve plus `min_free_memory_mb` exceeds the machine's RAM — such a ceiling never binds.

**Builds you start yourself** should stay inside the agent limits: `systemd-run --user --collect --slice=agents.slice -p MemoryMax=1800M …`. Without `--slice=agents.slice` the unit lands in app.slice, where only the watchdog sees it.

### Small hosts (2 vCPU, ≤ 4–8 GB)

- `memory_guard_mode = enforce`, `resource_guard_action = kill`.
- `agents_cpu_quota_pct = 140` — leaves wick and the OS more than half a core under full load.
- `sub_agents_max_parallel = 1` — one sub-agent at a time; heavy builds (vite, vitest, go test) one at a time too.
- Keep the derived memory values: they are computed from the RAM the machine reports at runtime, so they follow a RAM upgrade without editing.

## Reading the Resources page

Four measurements that are easy to conflate. Keeping them apart is the difference between "the machine feels slow" and knowing why:

| Measurement | Answers | Failure it predicts |
|---|---|---|
| Memory per tree | how much an agent holds | an OOM kill |
| **Process list** | *which* process holds it | points at the real culprit — chromium, not "claude" |
| CPU % and IO rate | how hard it is working | contention, slowness |
| **Disk capacity** | how much room is left | **writes failing outright** |

Disk capacity and IO rate are the pair most often confused: a busy disk is *slow*, a full disk *fails*. Wick writes continuously — transcripts, spawn logs, trace events — so capacity is tracked separately, against the filesystem the data directory actually lives on.

Available space is reported alongside free space because a filesystem may reserve a percentage for root: an unprivileged wick cannot use all of "free", and promising room that is not there is worse than reporting less.

The process list is capped (heaviest first). That is deliberate — a browser-driving agent holds dozens of renderers that add rows without adding information.

## Suggesting a limit

The suggested ceiling is the observed peak plus about 30% headroom.

The headroom is not padding for its own sake: a limit set exactly at the observed peak kills the next run that does slightly more work — which is the failure that makes an operator distrust the guard and switch it off entirely.

**Applying suggested limits never turns enforcement on.** Filling in numbers and starting to kill agents are different actions; a click read as "fill in the blanks" must not begin enforcing.

## Recommended rollout

1. Set `memory_guard_mode` to `measure` and leave it for a representative period — long enough to include the heavy sessions.
2. Read the observed peaks on the Resources page.
3. Apply the suggested limits (still not enforcing).
4. Switch to `enforce` once the numbers look right.

Going straight to `enforce` with guessed numbers is what produces surprise kills.

## When an agent is killed

A memory kill is reported with its actual cause — there are three, and they behave differently:

| Cause | What it means | Auto-restarted? |
|---|---|---|
| **Its own limit** (`agent_memory_max_mb`) | the agent alone crossed its ceiling | **no** — the same work would hit the same ceiling; the message names the peak and the limit, and the remedy is a higher limit or smaller work |
| **The combined limit** (`agents_total_memory_mb`) | all agents together crossed the shared ceiling and the kernel picked this one | yes — contention, not this agent's fault; raise the combined limit or run fewer agents at once |
| **The machine ran out** | the global OOM killer picked this agent (agents are biased to die before wick) | yes — free up host memory, lower the combined limits, or run fewer agents |

On Linux with systemd the report carries the measured peak, so there is no guessing. Either way a kill is not a lost session: the conversation is intact, and the notice the agent receives states the cause so it knows whether to continue, shrink the work, or relay a settings change to the user.

If the user reports an agent dying with no OOM report, check the platform table first: outside Linux + systemd the kill can still happen but cannot always be *named*, which looks like an unexplained exit. Unexplained exits are auto-restarted up to 3 times in a 10-minute window.
