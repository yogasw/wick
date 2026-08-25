---
name: wick-resource-limits
description: Use when the user asks about agent memory or CPU limits, an agent being killed or OOM, the Resources page, usage history, disk space, why a spawn is queued, or how to cap what an agent may consume. Covers the three guard modes and what each actually does, which platform enforces what, how to read the four separate measurements, and the rules for suggesting limits safely.
---

# Agent resource limits

Wick can cap how much memory, CPU, and IO its agents consume, and records what they actually used. Enforcement is done by the **kernel**, not by wick sampling and killing — a sampler cannot catch a process that allocates a gigabyte between two samples.

Two things follow from that, and they explain most questions about this subsystem:

- **Enforcement needs cgroups, so it is Linux-only.** Measurement, reporting, and crash recovery work everywhere.
- **A killed agent is killed by the kernel.** Wick's job is to report it accurately, not to prevent it after the fact.

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

Under **Memory Guard** and **Usage History** in the agents config:

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
| `agents_cpu_quota_pct` | `0` (off) | hard CPU cap |
| `agents_tasks_max` | `512` | process-count cap — the fork-bomb guard |
| `agents_io_weight` | `0` (off) | relative block-IO share |
| `resource_history_enabled` | `true` | record usage samples |
| `resource_sample_interval_sec` | `15` | seconds between samples |
| `resource_retention_minutes` | `360` | how long samples are kept |
| `resource_history_max_points` | `4096` | hard cap on stored samples |

Defaults for the derived values come from the machine's own size, so they are sane without tuning.

`agents_tasks_max` deserves a mention because it catches something no memory knob can: thousands of tiny processes cripple the scheduler while staying comfortably under every memory ceiling.

A per-instance `MemoryMaxMB` overrides the global per-agent value when set. Note it does **not** take the smaller of the two — a memory ceiling is per-process, not a share of a pool, so an explicit per-instance value wins outright.

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
