---
name: agent-resource-guard
description: Use for ANY work on agent resource limits or usage analytics — the memory guard, CPU/IO/task contention controls, OOM detection and reporting, the Resources page, usage history sampling and retention, spawn admission, or `wick memory report`. Covers internal/agents/provider/{memscope,oomscore}, internal/agents/provider/memguard.go, internal/pkg/{memreport,sysmem}, internal/agents/config/memguard.go, internal/agents/pool/admission.go, internal/tools/agents/memory_handler.go, fe/agents/resources, and the systemd unit in internal/pkg/daemon. Explains WHY memory is enforced by the kernel rather than sampled, why the slice is a SIBLING of wick's own cgroup, why MemoryHigh and swap must stay off (a process past them thrashes forever instead of dying), and why measure mode applies no limits while still recording full usage data. Note that measure mode DOES record — it is enforcement that is withheld, not measurement. Read before changing a limit, a default, a mode, the sampler, the retention policy, or the OOM exit path.
allowed-tools: Read, Grep, Glob, Edit, Write, Bash
paths:
  - "internal/agents/provider/memscope/**"
  - "internal/agents/provider/oomscore/**"
  - "internal/agents/provider/memguard.go"
  - "internal/agents/provider/exit_oom.go"
  - "internal/agents/config/memguard.go"
  - "internal/agents/config/general.go"
  - "internal/agents/pool/admission.go"
  - "internal/pkg/memreport/**"
  - "internal/pkg/sysmem/**"
  - "internal/tools/agents/memory_handler.go"
  - "internal/pkg/daemon/service_linux.go"
  - "fe/agents/resources/**"
---

# Agent Resource Guard

Wick spawns agents (claude, codex, gemini) as child processes, and those agents
spawn their own children — MCP servers, tool subprocesses, browsers. This
subsystem bounds what all of that may consume, and measures what it actually
does consume.

**It is not only memory.** Memory is the part that *kills*, so it gets the
deepest treatment; CPU, block-IO, and process count are bounded too, and all
four are measured. The name "memory guard" survives in config keys for
compatibility — read it as "resource guard".

## The one idea to keep

> **The kernel enforces. Wick observes and explains.**

Every design decision below follows from that. A userspace sampler cannot win
against a browser that allocates a gigabyte between two ticks, so wick never
tries to kill on a sample. It asks the kernel to enforce a ceiling, then reads
back what happened so a human gets a sentence instead of exit code 137.

## Easy to read backwards

These four have already been misread — twice from the skill's own description,
before it said this. If you are answering a question about any of them, quote
this section rather than paraphrasing:

| Sounds like | Actually means |
|---|---|
| "measure writes nothing" | Writes no **limits**. Records usage in full — that is the mode's purpose. |
| "swap must stay off" | Not about OOM-killer predictability. With swap, the process **never dies**; it thrashes and holds its slot forever. |
| "the guard ships off by default" | Enforcement is off. **Usage history is on** and independent of the mode. |
| "unavailable without systemd" | **Limits still apply without systemd** — the cgroupfs backend enforces them. What systemd adds is the ability to *name* an OOM kill. And measurement, admission, the Resources page, and `oom_score_adj` never needed either. See the degradation table. |

The shared shape: **measurement and enforcement are separate axes.** Nearly every
misreading here comes from collapsing them into one.

## Why this exists

Production wick was being OOM-killed. Not from a leak in wick — an agent or a
tool subprocess would balloon (usually a browser or a large file read), and
because every one of them was a child of wick inside wick's own cgroup, the
kernel's OOM killer took wick down with them.

Three facts combined:

1. **The pool cap counts processes, not bytes.** `PoolConfig.MaxConcurrent`
   grants slots. One slot is an idle claude at ~150 MB or a claude driving a
   browser at ~2 GB — the pool cannot tell them apart.
2. **Grandchildren are invisible.** A browser started by a tool started by an
   agent is where the memory is. Reading the agent's own RSS reports ~150 MB
   while the real tree is ~2 GB.
3. **Everything shared wick's cgroup**, so wick was a candidate victim.

## Architecture

```
                    user@<uid>.service
                    ├── app.slice/wick.service     ← wick. NEVER a victim.
                    └── agents.slice               ← SIBLING, not a child
                        │                            MemoryMax / CPUWeight /
                        │                            TasksMax / IOWeight
                        ├── claude-agent-7.scope   MemoryMax=<per-agent>
                        └── codex-agent-8.scope    MemoryMax=<per-agent>
```

The slice being a **sibling** of `wick.service` rather than a child is the load-
bearing decision. Agent memory never counts toward wick's own cgroup, so wick
never looks like the fat process worth killing, and an aggregate ceiling on the
slice can never put wick inside the blast radius.

An earlier revision placed agents in a sub-cgroup of wick's own via
`Delegate=yes`. That is strictly worse and was rejected. **Do not reintroduce
it** — and note the unit therefore needs no `Delegate=`, which is why existing
installations get the guard without touching their service file.

### Spawn path

```
spawn request
   │
   ├─[admission]  free RAM ≥ MinFreeMemoryMB? ──no──> queue (existing pool queue)
   ├─ procgroup.Apply(cmd)                    (pre-existing; teardown depends on it)
   ├─[memscope]   WrapArgv → systemd-run --user --scope --slice=agents.slice
   ├─[oomscore]   bias the child toward being the OOM victim   (enforce only)
   └─ exec
        │
   agent runs ── kernel enforces MemoryMax → kills that scope's tree only
        │
   process exits
        │
   memscope.ReadStats(unit) → oom_kill > 0 ? ──yes──> oom > 0 ? ExitOOM (own limit)
                                                      : ExitError (host OOM, retryable)
```

## Packages

| Package | Responsibility |
|---|---|
| `internal/agents/provider/memscope` | Renders + installs `agents.slice`; wraps argv in `systemd-run --scope`, or in `wick __agent-exec` for the raw-cgroupfs fallback; reads `memory.peak` / `memory.events` (v2) or `memory.max_usage_in_bytes` (v1) back; releases v1 scopes. `DetectBackend` picks which. Linux-only, with `_other.go` no-ops. |
| `internal/agents/provider/oomscore` | Writes `/proc/<pid>/oom_score_adj`. The only layer needing neither systemd nor cgroups — so it also works on Termux, where lmkd reads the same knob. |
| `internal/agents/provider/memguard.go` | The single decision layer. Resolves mode/method into "wrap or not", "which limits", "was this an OOM". The three CLI spawners call it and hold no policy of their own. |
| `internal/agents/provider/exit_oom.go` | `ExitOOM` reason + the human sentence. |
| `internal/agents/config/memguard.go` | Mode/method constants, default derivation from RAM, limit resolution, `SuggestLimitMB`. |
| `internal/agents/pool/admission.go` | Refuses a spawn while free RAM is below a floor. Reads `/proc/meminfo`; no cgroups needed. |
| `internal/pkg/memreport` | `/proc` sampling (mem + CPU + IO + process count per tree), the bounded history buffer, and the sampler loop. |
| `internal/pkg/sysmem` | Machine total / available memory, plus **filesystem capacity** (`Disk`). Memory works on Linux (`/proc/meminfo`) and Windows (`GlobalMemoryStatusEx`); capacity also covers macOS. See the platform table below for the current gaps. |
| `internal/tools/agents/memory_handler.go` | `GET /api/memory`, `GET /api/memory/series`, `POST /api/memory/apply-suggested`. Admin-only. |
| `fe/agents/resources` | The Resources SPA. |

## Modes — and what each actually writes

`MemoryGuardMode`, default **`off`**.

| Mode | Slice limits written | Scope created | Kills |
|---|---|---|---|
| `off` | none | no | nothing |
| `measure` | **none** | yes (so `memory.peak` is readable) | nothing |
| `enforce` | all configured | yes | kernel kills the offender |

**`off` must stay byte-identical to a build without this feature**: no slice
unit written, no `oom_score_adj`, no argv wrapping, no goroutine. If a change
makes `off` do anything observable, the change is wrong.

### measure: applies nothing, records everything

> **"Writes nothing" refers to LIMITS, never to data.** In `measure`, usage is
> recorded in full — that is the entire point of the mode. What is withheld is
> enforcement. Do not read "measure writes nothing" as "measure collects
> nothing"; they are opposite claims.

`measure` writes **no slice limits at all — including the aggregate
`MemoryMax`** — while still creating the scope so `memory.peak` is readable per
agent. Its promise is "record numbers, change nothing", and every slice control
changes behaviour.

An earlier revision wrote the aggregate ceiling in measure mode, which could
kill agents collectively while the operator believed nothing was enforced.
`MemGuard.sliceLimits()` returns the zero `SliceLimits` for any mode but
`enforce`, and `TestMemGuard_MeasureWritesNoSliceLimits` pins it. **Do not
"optimise" that branch away.**

Separately: usage history (`resource_history_enabled`) is independent of the
mode entirely and records even when the mode is `off`. Measurement never
depends on enforcement being on.

## Methods

`MemoryGuardMethod`, default `auto`. Decides *who* wraps the spawn.

| Value | Wick's behaviour |
|---|---|
| `auto` | wraps when a backend actually works (probed once — systemd, else cgroupfs) |
| `wrapper` | does **not** wrap — something outside wick already does (a symlink wrapper on the agent binary). Wick still measures and reports. |
| `scope` | **legacy, identical to `auto`.** Accepted, never offered — see below. |

`MemGuard.wraps` tests for `MethodWrapper` and nothing else, so `auto` and
`scope` always took the same branch — `MethodScope` is never compared anywhere
in the tree. It was documented as "wick always applies it", which was never
true and has no honest implementation: "always wrap" cannot be delivered where
there are no cgroups, and forcing it would refuse spawns where every other
failure here degrades instead. It is out of the dropdown and stays accepted for
stored configs. **Do not add a branch for it.**

`wrapper` withholds more than the per-agent ceiling, and this is the part that
gets missed: `Wrap` returns early, so `EnsureSlice` never runs (**no aggregate
ceiling, no CPU/tasks/IO controls**) and the unit name is `""`, which makes
`ClassifyExit` return `ok=false` (**an OOM kill is reported as a plain
`ExitError`**). `BiasChild` is unaffected — it never consults `wraps()`.

**Double-wrapping is safe and is deliberately not detected against.** The kernel
enforces every ceiling in the hierarchy, so the tightest wins. That property is
what makes migration safe: both methods may be active at once, and if either
fails to apply, the other still holds.

## Non-negotiable scope properties

Every scope pins these, at every limit including none:

```
-p MemoryHigh=infinity
-p MemorySwapMax=0
```

- **`MemoryHigh` throttles instead of killing.** A process past it stalls
  indefinitely while holding its slot. This is not hypothetical: it turned one
  production incident into a **116-minute stall with no OOM kill**.
- **Swap has the same failure shape, more slowly.** With swap available a
  process over its limit pages in and out instead of dying, so the ceiling stops
  being a ceiling: nothing is killed, the slot is never released, and the whole
  machine slows down. The problem is not that the OOM killer becomes
  "unpredictable" — it is that it never fires at all.

In both cases the intent is the same: a limit must produce a **fast, clean,
attributable death**, not an indefinite slowdown. A stalled agent is worse than
a killed one, because nothing reports it and nothing recovers from it.

`TestWrapArgv_PinsHighAndSwap` asserts both. If you are ever tempted to set
`MemoryHigh` to a real value "to be gentler", that is the exact bug.

## Contention controls (CPU, tasks, IO)

Memory is the only resource whose exhaustion *kills* — the kernel must pick a
victim. CPU and IO exhaustion merely slow everything down; a fork bomb exhausts
the scheduler rather than RAM. So memory gets per-agent ceilings, and the rest
get one knob each on the shared slice, `enforce`-mode only.

| Knob | Default | What it does |
|---|---|---|
| `AgentsCPUWeight` | `50` | A **bias, not a cap**. Under contention agents yield to wick; when idle they use everything. Prevents "wick feels hung" on a small box — the failure that *looks* like a crash but is starvation. |
| `AgentsCPUQuotaPct` | `0` (off) | A hard cap on combined agent CPU. **Deliberately off**: a cap slows legitimate heavy work even on an idle machine, causing timeouts and retries that add load. The weight gives the same protection under contention without punishing idle-time work. |
| `AgentsTasksMax` | `512` | The fork-bomb guard. Thousands of tiny processes cripple the scheduler while staying under every memory ceiling — **no memory knob catches this**. |
| `AgentsIOWeight` | `0` (off) | Same shape as CPUWeight, for block IO. Off because IO starvation has not been an observed incident; the knob exists so enabling it is a config change, not a code change. |

All four live on the slice, not per-scope: contention is a machine-level
phenomenon.

## Config surface

Global, in `internal/agents/config/general.go`, groups `Memory Guard` and
`Usage History`:

| Key | Default | Meaning |
|---|---|---|
| `memory_guard_mode` | `off` | `off` / `measure` / `enforce` |
| `memory_guard_method` | `auto` | `auto` / `scope` / `wrapper` |
| `agent_memory_max_mb` | derived | Per-agent ceiling. 0 = none |
| `agents_total_memory_mb` | derived | Aggregate ceiling on the slice. 0 = none |
| `tool_memory_max_mb` | derived | Ceiling for a wick-provider shell command |
| `min_free_memory_mb` | derived | Queue a spawn below this free RAM. 0 = off |
| `protect_wick_from_oom` | `true` | `oom_score_adj` bias (enforce only) |
| `agents_cpu_weight` | `50` | see above |
| `agents_cpu_quota_pct` | `0` | see above |
| `agents_tasks_max` | `512` | see above |
| `agents_io_weight` | `0` | see above |
| `resource_history_enabled` | `true` | Record usage samples |
| `resource_sample_interval_sec` | `15` | Seconds between samples |
| `resource_retention_minutes` | `360` | Keep samples this long |
| `resource_history_max_points` | `4096` | Hard ceiling on stored samples |

Per-instance, on `provider.Instance`: **`MemoryMaxMB`**.

### The resolution rule that differs from MaxConcurrent

`MaxConcurrent` resolves as `min(providerMax, globalRemaining)` — slots are a
shared pool, so no provider may exceed the global ceiling.

Memory resolves differently, and this is intentional:

> `MemoryMaxMB` if non-zero, else the global `AgentMemoryMaxMB`. **No `min()`.**

A memory ceiling is per-process, not a share of a pool. A per-instance value
**may exceed** the global default — that is precisely how the one instance
driving a browser is accommodated without making every other agent fatter.
Applying `min()` here would force the operator to raise the global ceiling
instead. See `config.ResolveAgentLimitMB` and its table test.

### Defaults are derived from the machine

`config.DeriveMemoryDefaults(totalRAMBytes, maxConcurrent)`. A 3 GB box and a
32 GB box must not start from the same numbers, so the four byte-limits are left
**zero** in the struct literal and filled from detected RAM. Floors prevent a
tiny machine deriving a zero budget, which would read as "unlimited" and invert
the whole point.

## OOM reporting

An OOM-killed process is **indistinguishable from any other SIGKILL** by exit
status: code 137, no distinguishing signal detail. The cgroup's `memory.events`
`oom_kill` counter is the only evidence, and `dmesg` is not readable without
root.

`--collect` reaps a transient scope as soon as its last process exits, so the
read races the reap. Therefore:

> **Read `memory.events` on the exit path, before returning from `Wait()`.**
> A missing file means "unknown, not OOM" — never guess.

`MemGuard.ClassifyExit` returns `ok=false` whenever there is no evidence, and
only reclassifies `ExitError` (a clean or deliberate stop is already explained).

**`oom` and `oom_kill` are different counters, and the difference is the
verdict.** `memory.events` `oom` counts OOM conditions raised by THIS cgroup
hitting its own limit; `oom_kill` counts processes here killed by ANY OOM
killer — the global one included. `classifyStats` (the pure decision behind
`ClassifyExit`, testable on every platform) discriminates:

- `oom_kill > 0 && oom > 0` → the agent broke its own ceiling → `ExitOOM`,
  `OOMDetail`, never retried.
- `oom_kill > 0 && oom == 0` → the MACHINE ran out and the global OOM killer
  picked this agent (its `oom_score_adj` bias makes that likely) → stays a
  retryable `ExitError`, with `HostOOMDetail` naming host-level remedies.
  Blaming the per-agent limit here was a real production misread: a 1.2 GB
  peak reported as "over its 1285 MB limit".

`OOMDetail` names whatever numbers it actually has, and invents none:

| peak | limit | message shape |
|---|---|---|
| known | known | "used 1.5 GB, over its 1024 MB limit. Raise the limit…" |
| unknown | known | "killed … for exceeding its 1024 MB memory limit. Raise the limit…" |
| known | **none** | "used 1.5 GB and was killed … no individual limit, so the cause is the combined limit or the machine running out" |
| unknown | none | same, without the peak |

The no-per-agent-limit cases matter: printing "its 0 MB limit" describes a
misconfiguration that does not exist and sends the operator to the wrong
setting.

**`ExitReason` values are persisted in spawn logs — append, never insert.**

## Crash recovery

Death detection already existed (`onAgentExit`, `ReconcileDead`) but only
released the slot. From the session's side an unexplained death looked like the
agent stopping mid-sentence: no message, no continuation, and the next user turn
started a fresh process that had no idea anything had gone wrong.

`pool/crashrecovery.go` adds the response. Two decisions, deliberately separate:

**Whether to restart** (`ShouldRespawn`) — only `ExitError` qualifies. Every
other reason already has an explanation, and restarting would fight whoever
caused it:

| Reason | Restart? | Why not |
|---|---|---|
| `ExitError` | **yes** | no explanation; worth another try |
| `ExitClean` | no | it finished — restarting redoes the work |
| `ExitStopped` | no | a preempt, session change, or shutdown asked for it |
| `ExitIdle` | no | the TTL reclaimed it on purpose |
| `ExitRespawn` | no | a turn boundary, not a death |
| **`ExitOOM`** | **no** | repeating the work hits the same ceiling — the loop this subsystem exists to prevent |

Bounded at `maxRespawnAttempts` (3) within `respawnWindow` (10m). The cap is
load-bearing, not cosmetic: the restart goes through `Send`, which can spawn,
which can fail, which lands back here. A process that dies immediately on start
will die again, so unlimited retries turn one broken config into an infinite
loop. `TestRespawnBudget_TerminatesTheRecursion` pins that the budget bounds it
with no off-by-one.

**What the agent is told** (`crashNotice` / `oomNotice`) — the agent resumes
with its conversation intact, so from the inside a crash is invisible: the last
thing it did simply produced nothing. Without a notice it either goes silent or
starts the task over. The message therefore states the cause, warns that work in
flight may be half-finished, and says **continue rather than restart**. The OOM
notice is different on purpose — it asks for a smaller approach, because the
same work would hit the same limit.

Restarting reuses `pool.Send` rather than calling the spawner directly: `Send`
is the pool's one entry point for starting an agent, so the restart inherits
slot accounting, queueing, and admission. A second spawn path would drift.

One subtlety makes that work: `pool.send` buffers non-user turns without
spawning (channel origin-context, reap notices — a user turn always follows or
none is wanted). Recovery notices are the exception, carved out by their
source `"recover"`: the agent just died and NO user turn is coming, so the
notice must spawn on its own or it sits buffered until a human happens to
message the session — which silently disables the entire respawn path.
`TestRecoverTurnSpawnsAlone` / `TestRecoverOOMNoticeSpawnsAlone` pin it.

## wick's own OOM protection

`MemGuard.BiasChild` pushes agents **up** the victim list at spawn. That is half
the job — without a matching push **down** for wick, the daemon stayed an
ordinary candidate, and once the agents were gone and pressure remained the OOM
killer could pick the process whose survival is the entire point.

`Run` therefore calls `oomscore.AdjustSelf(DaemonScore)` in `enforce` mode when
`protect_wick_from_oom` is on. `TestScores_AgentAboveDaemon` asserts the two
scores straddle the kernel default, since equal-sided scores would order nothing.

## Tool subprocesses (wick provider only)

The wick provider is in-process but spawns real shell subprocesses. A `grep -r`
over a large tree is an unguarded direct child of wick — same failure mode as a
ballooning agent, with none of the isolation. Those get their own smaller scope
(`tool_memory_max_mb`).

Unlike an agent, **exceeding the tool limit must not kill the agent**: the call
fails, the model reads the error, and it can retry with a narrower scope. That
recoverability is what makes a tight ceiling here reasonable. The message says
so explicitly, because a bare "command failed" just gets retried unchanged.

CLI providers (claude/codex/gemini) ignore this knob — their tool calls already
run inside the agent's own scope.

## Usage history

`internal/pkg/memreport`. Samples every agent process tree on an interval:
RSS, CPU, block-IO read/write, and process count, summed across descendants in
one walk (`SumSubtreeAll`).

**CPU and IO arrive from `/proc` as counters cumulative since process start**, so
a rate needs two samples and the elapsed time between them. The first sample of
any process reports **0** rather than a number derived from its lifetime, which
would spike wildly for a long-lived agent. `saturatingSub` handles a counter
appearing to go backwards (PID reuse), which would otherwise wrap to a nonsense
rate near 2^64.

### Retention: two independent bounds

Either alone fails, so both are enforced, and purging runs on **every** sample:

- **Time** (`resource_retention_minutes`) — a machine left running for a month
  must not report a month of history.
- **Points** (`resource_history_max_points`) — a misconfigured short interval
  cannot grow the buffer without limit no matter what the window allows.

`SetRetention` purges immediately, so lowering it in the UI frees memory now
rather than at some later tick. `NewHistory` treats non-positive values as
"use the default", never as "unlimited" — an unbounded diagnostic buffer inside
the feature whose job is bounding memory is not defensible.

**In memory, never on disk.** This is telemetry whose whole value is recency;
persisting it would add a schema, a migration, and disk growth on exactly the
machines this protects.

### Sampler lifetime

The sampler must be started with a context that is **cancelled at shutdown**.
It is built in `NewServer` and started in `Run(ctx)` — the same pattern
`pluginReloader` uses. Starting it on `context.Background()` leaks the goroutine
past every stop; `TestSampler_StopsOnContextCancel` pins the exit path.

History runs **regardless of guard mode** — measuring is how an operator learns
what to set, so it must work while the guard is `off`.

## What the Resources page shows, and why each thing is separate

Four measurements that are easy to conflate. Keeping them apart is the
difference between "the machine feels slow" and knowing why:

| Measurement | Answers | Failure it predicts |
|---|---|---|
| Memory per tree (`tree_bytes`) | how much an agent holds | OOM kill |
| **Process list** (`processes`) | *which* process holds it | points at the real culprit (chromium, not "claude") |
| CPU % + IO Bps | how hard it is working | contention, slowness |
| **Disk capacity** (`disk`) | how much room is left | **writes failing outright** |

Disk capacity and IO rate are the pair most often confused. A busy disk is
slow; a **full** disk fails writes. Wick writes continuously — transcripts,
spawn logs, trace events — so capacity is its own row, reported for
`appname.AgentsDir()` so a relocated `WICK_DATA_DIR` reports the filesystem it
actually lives on.

`DiskUsage.AvailBytes` is reported alongside `FreeBytes` because ext4 reserves a
percentage for root: an unprivileged wick cannot use all of "free", and
promising room that is not there is worse than reporting less. `UsedPct` is
computed against total/free so it matches `df`.

`memreport.Subtree(procs, root, limit)` builds the process list, heaviest first,
**capped** (12 in the API). The cap is not cosmetic: a browser-driving agent
holds dozens of renderers that add payload without adding information. Ties
break on PID so a refresh does not reshuffle rows under the operator's cursor.

## Suggested limits

`config.SuggestLimitMB(peakBytes)` — observed peak + 30% headroom.

**Do the arithmetic in bytes and round up.** Converting to MB first and scaling
afterwards silently loses the headroom at small sizes: 1.5 MB truncates to 1 MB,
and `1*130/100` is 1 again — a "suggestion" identical to the peak it was meant
to leave room above. Rounding up also guarantees the result is never below the
peak.

Headroom is not padding for its own sake: a limit set exactly at the observed
peak kills the next run that does slightly more work, which is the failure that
makes operators distrust the guard and switch it off.

**Applying suggestions must never change `memory_guard_mode`.** Filling in
numbers is not the same as turning enforcement on; conflating them would start
killing agents on a click the operator read as "fill in the blanks".

## Degradation

Scope isolation is a **ranked choice of two mechanisms**, not one yes/no. The
preferred one needs `systemd-run` on PATH and a reachable user bus; where that
fails, raw cgroup v1 through a writable `/sys/fs/cgroup/memory` still enforces.
Both are gone on Windows/macOS. Spawning must still succeed — and wick must
**say so**:

- `memscope.DetectBackend` returns `BackendSystemd` / `BackendCgroupFS` /
  `BackendNone`, probed once and cached. `Available()` is kept as
  `DetectBackend() != BackendNone` for the callers that only ever wanted a
  yes/no. **Read the backend, do not re-derive it from "is systemd around".**
- Each probe actually creates and tears down a throwaway group, because that is
  the only answer that cannot be wrong: a mount can be present but read-only,
  or present with no memory controller attached.
- The cgroupfs backend enforces exactly as hard — `memory.limit_in_bytes` is a
  real kernel ceiling — but **reports less**. cgroup v1 has no counterpart to
  v2's `memory.events` `oom_kill`, so `ReadStatsV1At` returns a real peak with
  `OOMKills=0` always. Rule 4 then does the right thing on its own: no
  evidence → not an OOM. Do not "improve" this by inferring a kill from the
  peak crossing the limit; a fabricated kill count is worse than none.
- Nothing on a systemd-less host performs "create a cgroup, join it, exec the
  agent", so wick re-execs **its own binary** through the hidden
  `__agent-exec` subcommand (`app/agentexec_cmd.go` → `memscope.RunAgentExec`)
  to do it. The join must happen in the process that becomes the agent, which
  is why a parent cannot do it. `syscall.Exec` then replaces the image, so no
  intermediate process sits between wick and the agent — `kill(-pgid)`
  teardown still reaches it, the same property `--scope` gives the systemd
  path.
- **The cgroupfs backend must release its own scopes.** `--collect` reaps a
  systemd scope automatically; raw cgroupfs has no daemon, and
  `ScopeUnitName` never reuses a name, so every spawn would leave a permanent
  directory. `MemGuard.ReleaseScope` handles it from the agent exit path,
  **after** `ClassifyExit` — removing the group takes its accounting files
  with it, the same ordering constraint `--collect` imposes. It is
  unconditional on exit reason (a clean exit leaks a directory just like a
  crash), and uses `rmdir` not `RemoveAll`: controller files are kernel
  interfaces that cannot be unlinked, and EBUSY means a process is still in
  there and the group must stay.
- `oom_score_adj` still works **wherever the knob exists** — a plain file write,
  no bus. That covers a Linux container and Termux, but NOT Windows or macOS,
  which have no such file.
- When `BackendNone` is the answer, **there is no silent substitute**. In
  particular, **do not add an RSS-sampling watchdog that kills.** A sampler
  cannot see a gigabyte allocated between two ticks; substituting one delivers
  the appearance of protection without its substance. Adding a *third* real
  kernel mechanism (cgroup v2 without systemd, Job Objects on Windows) is a
  different thing and is legitimate — extend `Backend` for it.
- The Resources page and `wick memory report` both surface the unavailability.
  The page's notice must not blame systemd specifically: that branch is reached
  only when **both** backends failed, so naming one sends the operator chasing
  the wrong thing.

Admission and history are separate from the scope machinery on purpose, so they
survive where it does not: on Linux they read `/proc`, which is why a container
and Termux keep them. Windows reads its own APIs for the process listing and
machine memory; macOS currently reads neither. The table below is the
authority — do not generalise from "not Linux" to "nothing works".

### Platform support — answer from this table, do not infer

**Enforcement is Linux-only** — it needs a cgroup hierarchy, and Windows would
need Job Objects while macOS has no equivalent; neither is implemented. But
enforcement is **not systemd-only**: a container without systemd still enforces
through the cgroupfs backend. And "cannot enforce" is not "does not work" —
measurement, reporting, and crash recovery run everywhere, and the split is not
the same on every non-Linux platform:

| Capability | Linux + systemd | Linux, no systemd (cgroupfs) | Windows | macOS |
|---|---|---|---|---|
| Per-agent memory ceiling | ✅ | ✅ | ❌ | ❌ |
| Aggregate slice ceiling | ✅ | ✅³ | ❌ | ❌ |
| CPU weight / quota / TasksMax / IOWeight | ✅ | ❌⁴ | ❌ | ❌ |
| `ExitOOM` with a measured peak | ✅ | ❌⁵ | ❌ | ❌ |
| Scope peak readable (`measure` mode) | ✅ | ✅ | ❌ | ❌ |
| `oom_score_adj` (agents die before wick) | ✅ | ✅ | ❌ | ❌ |
| Spawn admission on free RAM | ✅ | ✅ | ❌¹ | ❌ |
| Machine memory total/available | ✅ | ✅ | ✅ | ❌ |
| Process listing (`Snapshot`) | ✅ | ✅ | ✅ | ❌ |
| Disk capacity (`sysmem.Disk`) | ✅ | ✅ | ✅ | ✅ |
| Usage history + Resources page | ✅ | ✅ | ✅ | ⚠️² |
| **Crash recovery / respawn / notices** | ✅ | ✅ | ✅ | ✅ |

¹ The reading works on Windows (`GlobalMemoryStatusEx`), but `MinFreeMemoryLoader`
is only consulted where the rest of the guard runs.
² Renders, but the per-process tables are empty — see the known gaps below.
³ Written onto the slice directory directly, with `memory.use_hierarchy=1` so
children share the cap. That write must land before the slice has live
descendants or the kernel returns EBUSY, which is why it is best-effort and
re-attempted harmlessly on later calls.
⁴ These are systemd unit properties. Only the memory controls carry over.
⁵ The kill still happens; it just cannot be *named*. cgroup v1 has no per-group
OOM-kill counter, so the exit keeps its generic reason. See the Degradation
section.

**Known gaps on macOS** (both would be small to close, neither is done):
`Snapshot()` falls through to the no-op, and `sysmem.Total`/`Available` are not
implemented, so the page shows disk capacity and little else. Windows had the
same two gaps until `snapshot_windows.go` and `sysmem_windows.go` were added;
macOS would need `libproc` and `sysctl` respectively.

So on a machine that cannot enforce, `measure` is **not** pointless: what it
cannot do is limit anything, and it never claimed to. On Linux the recording,
the peaks, and the suggestions all still function; on Windows so does everything
except the limits.

Check availability directly rather than inferring it, in the same order
`DetectBackend` does:

```bash
# 1. systemd backend (preferred)
systemd-run --user --scope --quiet --collect -p MemoryMax=64M -- /bin/true; echo $?

# 2. cgroupfs fallback — only consulted if the above is nonzero
d=$(mktemp -d /sys/fs/cgroup/memory/wick-probe-XXXX) && \
  test -f "$d/memory.limit_in_bytes" && : > "$d/cgroup.procs"; echo $?; rmdir "$d"
```

Exit 0 on the first means full scopes. Exit 0 on only the second means limits
are enforced but an OOM kill cannot be named. Both nonzero means the `macOS`
column applies to a Linux box — and the Resources page says so in its own
notice, so an operator is never left believing they are protected.

## Teardown is a gate, not a detail

Wick stops a session with `kill(-pgid)` across the whole process group.
`systemd-run` inserts a process between wick and the agent, so this must be
verified rather than assumed: if the scope breaks the process-group
relationship, wick keeps its memory guard and **loses the ability to stop a
session at all** — worse than the bug being fixed.

`procgroup.Apply` therefore stays exactly where it is, alongside the scope, and:

```bash
go test -tags integration ./internal/agents/provider/memscope/ -v
```

`//go:build linux && integration`. **Run it on a Linux box with a systemd user
session before enabling `enforce` in production.** It also asserts the design's
central claim directly: while a deliberate memory hog dies, a sibling scope
survives untouched.

## systemd unit

`internal/pkg/daemon/service_linux.go` → `renderUnit`.

The unit carries a restart rate-limit:

```ini
StartLimitIntervalSec=300
StartLimitBurst=3
```

`Restart=on-failure` with no ceiling turns one OOM into an invisible loop: wick
dies, restarts 5 s later, resumes the session, respawns the agent, balloons
again. Three failures in five minutes leaves the unit `failed` and keeps it
there — a loop becomes a visible incident. Worth having even with the guard off.

`TestRenderUnit_NoDelegate` asserts `Delegate=` is absent; see the sibling-slice
rationale above.

## Frontend

`fe/agents/resources`, served at `/tools/agents/resources`, admin-only.

Two traps that already bit once:

1. **Vite `base` must live under the SPA mount** (`spaPrefix`, `/workflow/`) —
   `/tools/agents/workflow/resources/`, even though the *page route* is
   `/tools/agents/resources`. Page route and asset route are different things.
   A base outside the mount builds fine and 404s in production.
2. **Nav icons come from a switch on the label** in `agentsNavIcon`, and the
   "More" group auto-expands from a separate list in `agentsMoreOpenAttr`. Miss
   either and the row renders bare or hides itself.

`Sparkline.svelte` draws inline SVG rather than pulling a charting library — one
series type does not justify the build weight or the theme-integration problem.
Its hover readout keeps the **tooltip in HTML, not SVG text**, because the chart
uses `preserveAspectRatio="none"`: the viewBox is stretched horizontally to fill
its container, which would distort any text drawn inside it. Crosshair strokes
use `vector-effect="non-scaling-stroke"` for the same reason.

`dist/` is gitignored and built by CI, so **Go tests must skip when the bundle
is absent** — Go tests run before `npm run build` in the release pipeline. Skip
on "not built"; never on "built wrong".

## Rules when changing this subsystem

1. **Never make `off` do anything.** It is the compatibility contract.
2. **Never write slice limits in `measure`.** Including the aggregate.
3. **Never set a real `MemoryHigh`, never allow swap.** See the 116-minute stall.
4. **Never guess an OOM.** No evidence → not an OOM.
5. **Never add a killing watchdog** as a fallback where cgroups are missing.
6. **Never `min()` the per-instance memory limit** against the global.
7. **Append to `ExitReason`, never insert.** The values are in spawn logs.
8. **Do the headroom arithmetic in bytes**, and round up.
9. **Keep `procgroup.Apply`** wherever it is; run the teardown integration test
   before enabling enforcement.
10. **Degrade loudly.** Silence makes an operator believe they are protected.

## Verification checklist

```bash
go build ./internal/... ./cmd/...
GOOS=linux go build ./internal/... ./cmd/...          # the platform that enforces
go test ./internal/pkg/memreport/ ./internal/pkg/sysmem/ \
        ./internal/agents/config/ ./internal/agents/provider/... \
        ./internal/tools/agents/... -count=1
cd fe && npm --workspace=@wick-fe/agents-resources run test:unit
```

On a Linux box with a systemd user session, before enabling `enforce`:

```bash
go test -tags integration ./internal/agents/provider/memscope/ -v
./wick memory report --watch 30s --for 1h
```

## Rollout sequence to recommend

```
1. Resources page (or `wick memory report --watch`)  ← turns nothing on
2. Read the peaks, set the limits
3. memory_guard_mode = measure, leave it a week      ← exact numbers, zero risk
4. memory_guard_mode = enforce                       ← only now does anything die
```

Every step reverses by setting the mode back to `off`.

## Further reading

- Design rationale, including rejected alternatives:
  `internal/planning/todo/agent-memory-guard/design.md`
- Task-by-task status and what remains:
  `internal/planning/todo/agent-memory-guard/plan.md`
- User-facing docs: `docs/guide/agents/memory-guard.md`
