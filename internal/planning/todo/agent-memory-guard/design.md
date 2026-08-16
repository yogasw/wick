# Agent Memory Guard — Design

Wick on production gets OOM-killed. The cause is not a leak in wick: an agent
subprocess (claude, codex) or a tool subprocess (grep, cat) balloons — usually
Playwright/Chromium or a large data read — and because every one of them is a
child of wick inside wick's own systemd cgroup, the kernel's OOM killer takes
wick down with them. This design makes the kernel kill the offender instead,
leaves wick alive to report why, and ships default-off so nothing changes until
the operator has measured their own machine.

## TODO

- [x] Task 1 — `oomscore` package: bias the OOM killer toward agents
- [x] Task 2 — `memscope` package: render/install `agents.slice`, wrap argv in `systemd-run --scope`, availability probe
- [x] Task 3 — `memscope` readback: `memory.peak` + `memory.events` for a live/just-exited scope
- [x] Task 4 — `ExitOOM` exit reason + classification from `oom_kill` before the scope is reaped
- [x] Task 5 — Config surface: `GeneralConfig` group (incl. `MemoryGuardMethod`) + per-instance `MemoryMaxMB`
- [x] Task 6 — Wire agent spawn (claude / codex / gemini) through the guard, honouring the method knob
- [x] Task 7 — Teardown regression test: `kill(-pgid)` still reaps the whole tree through a scope *(written + compiles; RUN IT on a Linux box with a systemd user session before flipping production to enforce)*
- [x] Task 8 — Wire wick-provider tool subprocesses (`tool_shell.go`, `tool_shell_bg.go`)
- [x] Task 9 — Spawn admission on available memory
- [x] Task 10 — `wick memory report` CLI (works with the guard off)
- [x] Task 11 — systemd unit: restart rate-limit + migration notice
- [x] Task 12 — Diagnostics endpoint (`GET /agents/api/memory` + apply-suggested)
- [x] Task 13 — Contention controls on the slice: CPU weight, CPU quota, TasksMax, IO weight (all configurable, enforce-only)
- [x] Task 14 — Usage history: memory + CPU + IO sampled per agent tree, bounded by retention AND a point ceiling, purged on every sample
- [x] Task 15 — Resources page (`/tools/agents/resources`): live table, trend charts, apply-suggested
- [ ] Before production enforce: run `go test -tags integration ./internal/agents/provider/memscope/ -v` on the Linux box — it gates everything

Implementation verified on: Windows (full test suite, native), Linux
(cross-compiled build + vet + test binaries compile), macOS (our packages build
+ vet; the full binary hits a pre-existing `fyne.io/systray` CGO cross-compile
failure that also exists on master).

## Constraints

- All user-facing UI text and every `desc=` in `wick:"..."` tags in **English**.
- No "qiscus" in examples/placeholders — generic names only.
- zerolog pattern: `l := log.With().Str("component", "x").Logger()`.
- Never edit `_templ.go`; edit `.templ` and regenerate.
- Linux-only mechanisms get `_linux.go` + `_other.go` no-op pairs. Windows is the
  development platform — everything must compile and run there, just unguarded.
- Default **off**. An installation that does not opt in must behave byte-identically
  to today: no cgroup created, no `oom_score_adj` written, no sampling goroutine.

## Problem

Three facts combine into the failure:

1. **The cap counts processes, not bytes.** `PoolConfig.MaxConcurrent`
   (`internal/agents/pool/capacity.go`) grants slots. One slot is an idle claude at
   ~150 MB or a claude driving Chromium at ~2 GB — the pool cannot tell them apart.
2. **Grandchildren are invisible.** Chromium is spawned by Playwright, which is
   spawned by the agent. Sampling the agent's own RSS reports ~150 MB while the real
   tree is ~2 GB. Any guard that reads a single PID's RSS measures the wrong number.
3. **Everything shares wick's cgroup.** The generated systemd unit
   (`internal/pkg/daemon/service_linux.go`) has no `Delegate=`, so agents land in
   `wick.service`'s own cgroup. Under pressure the kernel picks a victim by badness
   score across that cgroup, and wick — holding session state and buffers — is a fat
   target. `Restart=on-failure` with no rate limit then restarts wick, which resumes
   the session, which respawns the agent, which balloons again.

The wick provider adds a fourth path: it is in-process, but it spawns tool
subprocesses of its own (`tool_shell.go`, `tool_shell_bg.go`) via `safeexec.Command`.
A `grep -r` over a large tree or a `cat` of a huge file is an unguarded direct child
of wick.

## Approach

The kernel enforces; wick observes and explains.

A userspace sampler cannot win this race — Chromium allocates a gigabyte in well
under one sampling interval, so a watchdog is always late. cgroup v2 `memory.max` is
checked by the kernel on every allocation, applies to the whole subtree including
grandchildren, and kills instantly. Wick's job is to create the cgroup, then read
back what happened so a human gets a sentence instead of exit code 137.

Four layers, four independently testable units. Each takes PIDs and numbers, knows
nothing about `Agent` or `Pool`, and is testable without spawning a real agent.

| Layer | Unit | Enforcer | Protects against |
|---|---|---|---|
| 1 | `oomscore` | kernel | wick being chosen as the OOM victim |
| 2 | `memscope` | kernel (cgroup v2, via systemd scope) | one agent tree exhausting the machine |
| 3 | `memscope` peak read | — | not knowing what to set the limit to |
| 4 | pool admission | wick | starting an agent when RAM is already gone |

### Flow

```
spawn request
   │
   ├─[4] admission: available RAM ≥ MinFreeMemoryMB? ──no──> queue (existing tryGrantQueue)
   │
   ├─ procgroup.Apply(cmd)                    (existing — teardown still works)
   ├─[2] scope.Wrap(cmd, limit) → systemd-run --scope --slice=agents.slice
   ├─[1] oomscore bias on the child            (mode == enforce)
   └─ exec
        │
   ┌────┴─── agent runs ───┐
   │                       │
  [3] memory.peak       [2] kernel enforces MemoryMax
   │   readable any time  │   → kills that scope's tree only, instantly
   └──────────┬───────────┘
              │
        process exits
              │
        scope.Events() → oom_kill > 0 ? ──yes──> ExitOOM + peak
              │                                   (message names the numbers)
        --collect reaps the scope   ← read events BEFORE it disappears
```

### Placement: a sibling slice, not a sub-cgroup of wick

Agents go into `agents.slice` — a slice **beside** `wick.service`, not beneath it.
Each spawn becomes its own transient scope inside that slice:

```
user@1000.service
├── app.slice/wick.service        ← wick itself, never a victim
└── agents.slice                  MemoryMax=<aggregate>
    ├── claude-agent-4412.scope   MemoryMax=<per-agent>
    └── codex-agent-4530.scope    MemoryMax=<per-agent>
```

Two properties fall out, and both are the point:

- **Killing an agent kills only that agent.** The scope is the kill boundary, so an
  OOM in one session cannot reach a sibling session, and can never reach wick.
- **Memory pressure from agents is not pressure on wick.** Because the slice is a
  sibling rather than a child, wick's own cgroup accounting never includes agent
  memory, so wick never looks like the fat process worth killing.

This is validated in production (`agent-isolate.sh`), which already runs this exact
layout via wrapper symlinks. An earlier draft of this design placed agents in a
sub-cgroup of wick's own via `Delegate=yes` — that is strictly worse, because agent
memory then counts toward wick's cgroup and an aggregate limit there would put wick
inside the blast radius.

### Mechanism: `systemd-run --scope`, not `CgroupFD`

Wick wraps the spawn rather than creating cgroups itself:

```
systemd-run --user --scope --quiet --collect \
    --slice=agents.slice --unit=<provider>-agent-<pid> \
    -p MemoryMax=<limit> -p MemoryHigh=infinity -p MemorySwapMax=0 \
    -- <real binary> <args...>
```

`CgroupFD` + `clone3` (Go 1.25) was the earlier choice and is more direct — no
intermediary process. It is rejected because it requires `Delegate=yes` on wick's
unit, which forces the sub-cgroup placement rejected above. `systemd-run --scope`
needs no unit change, no root, and no sudo, and is the arrangement already proven on
this deployment.

Cost: one short-lived `systemd-run` process per spawn, and the agent's PID as wick
sees it is the scope's child. `Process.Pid()` must therefore report the real agent
PID, not `systemd-run`'s — see Teardown below.

### `MemoryHigh=infinity` and `MemorySwapMax=0` are mandatory

Not tuning details — the difference between a clean kill and an outage.

`MemoryHigh` throttles allocation instead of killing. A process past it is stalled,
not stopped, and it stays stalled indefinitely while holding its slot. This is not
hypothetical on this deployment: an incident on 15 Aug turned into a **116-minute
stall with no OOM kill** precisely because `MemoryHigh` was set. Every scope must
therefore pin `MemoryHigh=infinity` and rely on `MemoryMax` alone.

`MemorySwapMax=0` matters for the same reason: with swap available, a process over
its limit thrashes rather than dying, converting a fast clean failure into a slow
machine-wide one.

### Teardown must keep working

Wick stops a session with `kill(-pgid)` to the whole process group. Routing the spawn
through `systemd-run` inserts a process between wick and the agent, so this must be
re-verified rather than assumed: if the scope breaks the process-group relationship,
wick keeps its memory guard but loses the ability to stop a session at all.

`procgroup.Apply` therefore stays exactly where it is, and an integration test asserts
that after `kill(-pgid)` no process from the tree survives. The production preflight
already tests this (`agent-isolate.sh preflight`, test 3/3) and it passes there; the
test encodes that guarantee so a future refactor cannot silently break it.

### Modes

`off` is genuinely nothing — not a limit set high, not a disabled sampler. No cgroup
directory, no file writes, no goroutine.

| Mode | Behaviour | Effect on agents |
|---|---|---|
| `off` (default) | nothing at all | none |
| `measure` | cgroup created with **no limit**; `memory.peak` read | none — measurement only |
| `enforce` | `memory.max` set + `oom_score_adj` bias + admission | kernel kills offenders |

`measure` deliberately does **not** write `memory.high`. `memory.high` throttles
allocation — a behaviour change — and a mode whose entire purpose is "tell me the
numbers without touching anything" must not slow a workload down. It creates the
cgroup solely so `memory.peak` accounts the whole tree without the double-counting
that `/proc` RSS summing suffers from shared pages.

## Installing the slice from inside wick

Wick writes and reloads the slice unit itself. No wrapper scripts, no symlink
redirection, no sudo — everything here is user-scope.

On first spawn with the guard on, wick ensures `~/.config/systemd/user/agents.slice`
exists and matches the configured aggregate limit:

```ini
[Unit]
Description=Agent sessions (claude, codex, …), isolated from the wick daemon
Before=slices.target

[Slice]
MemoryMax=<aggregate>
MemoryHigh=infinity
MemorySwapMax=0
CPUWeight=<agents_cpu_weight>     ; omitted when 0
CPUQuota=<agents_cpu_quota_pct>%  ; omitted when 0
TasksMax=<agents_tasks_max>       ; omitted when 0
IOWeight=<agents_io_weight>       ; omitted when 0
```

then `systemctl --user daemon-reload`. Both steps are idempotent, so this runs on
every spawn without cost; the unit is rewritten only when the rendered content
differs from what is on disk. It reuses the existing patterns in
`internal/pkg/daemon/service_linux.go` (same directory, same `daemon-reload`
best-effort handling).

`daemon-reload` does **not** restart running units, so installing or re-limiting the
slice never disturbs a session in flight. Membership is fixed at spawn: sessions
started before the change keep their old placement until they end.

### Two methods, safe to run together

`agent-isolate.sh` reaches the same layout from the other side: it points
`/usr/local/bin/claude` at a wrapper that calls `systemd-run`. Each method catches
something the other cannot.

| | Wrapper (symlink) | Wick spawns the scope |
|---|---|---|
| Catches `claude` run by hand, or by cron/scripts | yes | no |
| Needs sudo to install | yes | no |
| Per-instance limits from the UI | no — hardcoded array | yes |
| Knows which session the scope belongs to | no | yes |

Neither supersedes the other, so `MemoryGuardMethod` selects who wraps:

| Value | Wick's behaviour | For |
|---|---|---|
| `wrapper` | does not wrap; assumes something external does | the current production box |
| `scope` | wraps its own spawns via `systemd-run` | after the script is uninstalled |
| `auto` (default) | probes for `systemd-run`; wraps if available | fresh installs |

**Double-wrapping is safe and is not detected against.** A claude wrapped by both
(1200 M wrapper scope nested in a 1536 M wick scope) is governed by the tightest
limit — the kernel enforces every ceiling in the hierarchy, so the inner one simply
wins. Nothing needs to negotiate.

That property is what makes the transition safe: during migration both methods may be
active, and if either fails to apply, the other still holds. There is no window in
which an agent runs unguarded.

Measurement is unaffected by this knob. Reading `memory.peak` / `memory.events`
(layer 3) and admission on available RAM (layer 4) work identically no matter who
created the scope — layer 3 reads whatever is under `agents.slice`, and layer 4 reads
`/proc/meminfo` and never touches cgroups at all.

One reporting consequence: under `wrapper`, wick can still detect that an OOM
happened, but cannot always attribute the scope to a specific session when several
agents of the same provider run concurrently. The message degrades from "session a3f9
was stopped" to "a claude agent was stopped". Under `scope`, wick names the session
exactly, because it created and named the scope itself.

Interactive `claude` from a shell (`~/.local/bin`) is untouched under `scope` — wick
governs only what wick spawns — and is covered under `wrapper`. That is the clearest
reason to keep the wrapper available rather than treat it as a stepping stone.

## Contention controls (CPU, tasks, IO)

Memory is the only resource whose exhaustion KILLS — the kernel must pick a
victim, and without the guard that victim can be wick. CPU and IO exhaustion
merely slow everything down, and a fork bomb exhausts the scheduler rather than
RAM. So memory gets the deep treatment above, and the rest get one knob each on
the shared slice, all enforced by the kernel, all `enforce`-mode only:

- **`CPUWeight`** (default 50) — a bias, not a cap. When the CPU is contended,
  agents yield to wick and the OS; when it is idle, agents use all of it. This
  is what prevents "wick feels hung" on a 1–2 vCPU box while codex compiles —
  the failure that LOOKS like a crash but is just starvation.
- **`CPUQuota`** (default off) — a hard cap on combined agent CPU. Deliberately
  off: a cap slows legitimate heavy work even when the machine is idle, causing
  timeouts and retries that ADD load. The weight above gives the same
  protection under contention without punishing idle-time work. The knob exists
  for boxes that need a guarantee anyway.
- **`TasksMax`** (default 512) — the fork-bomb guard. Thousands of tiny
  processes can cripple the scheduler while staying comfortably under every
  memory ceiling; no memory knob catches this. When the limit is hit, `fork`
  fails inside the slice — agents feel it, wick does not.
- **`IOWeight`** (default off) — same shape as CPUWeight for block IO. Off
  because IO starvation has not been an observed incident; the knob exists so
  turning it on is a config change, not a code change.

All four live on `agents.slice`, not per-scope: contention is a machine-level
phenomenon, and per-agent fairness has not been a problem worth per-agent knobs.

**Measure mode writes none of these — nor the aggregate `MemoryMax`.** Measure's
entire promise is "record numbers, change nothing", and every slice control
changes behaviour. An earlier revision wrote the aggregate ceiling in measure
mode too, which could kill agents collectively while the operator believed
nothing was enforced; `MemGuard.sliceLimits()` now returns empty limits for any
mode but `enforce`, and a regression test pins it.

## Aggregate limit

`agents.slice` carries `MemoryMax` across all agents, in addition to each scope's own
limit. Two ceilings, different jobs:

- **Per-scope** (`AgentMemoryMaxMB`) — stops one runaway session.
- **Aggregate** (`AgentsTotalMemoryMB`) — stops N well-behaved sessions from summing
  past what the machine has.

An earlier draft rejected any total cap, on the grounds that reaching it forces wick
to choose a victim among innocent agents. That reasoning was wrong about *who*
chooses: the kernel picks, by badness score, from **inside the slice only**. Wick
implements no victim policy, and wick itself is never a candidate — it lives outside
the slice. The objection applied to a wick-implemented total cap, not a kernel one.

Default: `AgentsTotalMemoryMB` = the agent budget from the derivation below. Setting
it to 0 leaves the slice unlimited, and per-scope limits alone apply.

## Usage history and the Resources page

A limit chosen from one snapshot is a limit chosen from luck: a browser can
allocate and release a gigabyte between two glances. So wick samples every agent
tree on an interval and keeps a bounded series behind
`/tools/agents/resources`.

**What is sampled** (`internal/pkg/memreport`): per agent process tree — RSS,
CPU, block-IO read/write, and process count, all summed across descendants in one
walk. CPU and IO arrive from `/proc` as counters cumulative since process start,
so a rate needs two samples; the first sample of any process reports 0 rather
than a number derived from its whole lifetime.

**Retention is two independent bounds, because either alone fails:**

- **Time** (`resource_retention_minutes`, default 360 = 6h) — a machine left
  running for a month must not report a month of history.
- **Points** (`resource_history_max_points`, default 4096) — a hard ceiling on
  the ring, so a misconfigured short interval cannot grow the buffer without
  limit no matter what the window allows.

Purging runs on every sample, and lowering retention in the UI purges
immediately rather than at some later tick. The buffer is in memory, never on
disk: this is diagnostic telemetry whose whole value is recency, and persisting
it would add a schema, a migration, and disk growth on exactly the machines this
feature exists to protect.

**Independent of the guard mode.** History runs while the guard is `off`, and
that is the point — measuring is how an operator learns what to set. It changes
nothing about how agents run.

**The page** shows machine memory in context, a live per-agent table (tree size,
CPU, disk rates, process count, and the heaviest descendant — naming chromium
rather than just reporting a big total), trend charts over a selectable window,
and suggested limits with an apply button. Applying fills in the numbers and
**does not** switch enforcement on; conflating those would start killing agents
on a click the operator read as "fill in the blanks".

**Suggested values prefer measurement over arithmetic.** With history recorded,
the per-agent suggestion is the observed peak plus 30% headroom, not a figure
derived from total RAM. Headroom is not padding for its own sake: a limit set
exactly at the observed peak kills the next run that does slightly more work,
which is the failure that makes operators distrust the guard and turn it off.

## Calibration

Enabling a limit you guessed is how agents start dying for no reason the operator
can explain. Two tools, for two moments.

### `wick memory report` — usable today, with the guard off

Reads `/proc` directly: builds the process tree from `PPid`, sums RSS per subtree
rooted at each known agent PID. Less precise than cgroup accounting (shared pages
count twice) but requires no cgroup, no config change, and no restart.

```
Machine: 3.0 GB total, 1.2 GB available

wick (pid 4412)                          412 MB

Agents (2 running)
  claude   session a3f9    tree: 1.24 GB   ← chromium 890 MB
  codex    session 7b21    tree:  340 MB

Tool subprocesses (spawned by wick provider)
  grep     pid 8821                        180 MB

Total agent memory: 1.58 GB

Suggested settings for this machine:
  max_concurrent        1
  agent_memory_max_mb   1600   (peak seen 1.24 GB + 30%)
  tool_memory_max_mb     256   (peak seen  180 MB + 40%)
  min_free_memory_mb     512
```

`--watch 30s --for 24h` samples over time and writes a report file. A single
snapshot almost certainly misses Chromium's peak, so the watch mode is what actually
produces a defensible number.

### Diagnostics page — after `measure` is on

Numbers now come from cgroup `memory.peak`: exact, subtree-inclusive, no
double-counting. Per instance: peak, mean, how often it approached the limit. An
**Apply suggested values** button fills the config fields from observed peaks.

### Intended sequence

```
1. wick memory report --watch 30s --for 24h    ← turns nothing on
2. read the numbers, set the config
3. mode = measure, leave it a week             ← exact numbers, zero risk
4. diagnostics → Apply suggested values
5. mode = enforce                              ← only now does anything die
```

Every step reverses by setting mode back to `off`. At no point is a limit guessed.

## Configuration

Global, in `GeneralConfig` (`internal/agents/config/general.go`), group `Memory Guard`:

| Knob | Type | Default | Meaning |
|---|---|---|---|
| `MemoryGuardMode` | dropdown `off\|measure\|enforce` | `off` | Master switch |
| `MemoryGuardMethod` | dropdown `auto\|scope\|wrapper` | `auto` | Who wraps the spawn. `wrapper` = something external already does; wick only measures |
| `AgentMemoryMaxMB` | number | auto | Default per-agent scope limit. 0 = unlimited |
| `AgentsTotalMemoryMB` | number | auto | Aggregate limit on `agents.slice`. 0 = unlimited |
| `ToolMemoryMaxMB` | number | auto | Limit for wick-provider tool subprocesses. 0 = unlimited |
| `MinFreeMemoryMB` | number | auto | Below this available RAM, queue the spawn. 0 = admission off |
| `ProtectWickFromOOM` | bool | `true` | Bias the OOM killer toward agents (only acts in `enforce`) |
| `AgentsCPUWeight` | number | `50` | CPU priority of agents under contention (kernel default 100). 0 = no preference |
| `AgentsCPUQuotaPct` | number | `0` | Hard cap on combined agent CPU, % of one core. 0 = uncapped |
| `AgentsTasksMax` | number | `512` | Max processes+threads across all agents (fork-bomb guard). 0 = unlimited |
| `AgentsIOWeight` | number | `0` | Disk-IO priority under contention (kernel default 100). 0 = no preference |
| `ResourceHistoryEnabled` | bool | `true` | Record usage samples. Runs regardless of guard mode |
| `ResourceSampleIntervalSec` | number | `15` | Seconds between samples |
| `ResourceRetentionMinutes` | number | `360` | Keep samples this long; older ones are purged |
| `ResourceHistoryMaxPoints` | number | `4096` | Hard ceiling on stored samples, whatever retention says |

"auto" = derived from detected RAM at first boot, not a fixed number. A 3 GB box and
a 32 GB box must not get the same default. Derivation:

```
agent_budget            = total_ram - 400MB (OS) - 600MB (wick)
agents_total_memory_mb  = agent_budget
agent_memory_max_mb     = agent_budget / max(1, MaxConcurrent)
tool_memory_max_mb      = min(512, agent_memory_max_mb / 4)
min_free_memory_mb      = max(256, total_ram / 8)
```

Sanity check against the production box this was designed for (3 GB, the tightest
case): budget 2 GB → aggregate 2048, per-agent 2048 at `MaxConcurrent: 1`. The
production script independently arrived at 2 G aggregate and 1200 M per session,
which is the same shape with a more conservative per-agent figure — reachable here by
setting `MaxConcurrent: 2` or overriding per instance.

Per-instance, on `provider.Instance` (`internal/agents/provider/provider.go`, beside
the existing `MaxConcurrent`):

| Field | Default | Meaning |
|---|---|---|
| `MemoryMaxMB` | `0` | 0 = inherit global. >0 = this instance's own limit |

### Resolution rule — deliberately unlike `MaxConcurrent`

`MaxConcurrent` resolves as `min(providerMax, globalRemaining)` because slots are a
shared pool and no provider may exceed the global ceiling.

Memory resolves differently:

> `MemoryMaxMB` if non-zero, else the global `AgentMemoryMaxMB`. **No `min()`.**

A per-instance limit may exceed the global default, because that is the entire point:
the instance that drives Playwright gets 4096 while the global default stays 2048,
without every other agent being allowed to grow. Applying `min()` here would mean
per-instance could only ever lower the limit, forcing the operator to raise the
global ceiling — making every agent fatter — to accommodate one heavy instance.

Total machine memory is bounded separately, by the aggregate slice limit (above),
`MaxConcurrent`, and admission (layer 4) — not by this per-process number.

### On the aggregate cap (revised decision)

An earlier revision of this document argued against any total cap: reaching it would
force wick to pick a victim among individually well-behaved agents, and "your agent
died because another agent was greedy" is not an actionable message.

That argument does not apply to the design as it now stands, because **wick does not
pick**. The kernel does, by badness score, restricted to processes inside
`agents.slice`. There is no victim-selection code to write, and wick is outside the
slice so it can never be selected. The original objection was to a wick-implemented
cap, and it stands against that; it does not stand against a kernel-enforced one.

What remains true from that argument: a per-scope kill is the more explicable
failure, because the process that dies is the one that exceeded its own limit. The
aggregate cap is therefore the backstop, not the primary mechanism — set it to the
agent budget, and expect per-scope limits to fire first in normal operation.

## Reporting

`ExitReason` (`internal/agents/provider/agent.go:106`) gains `ExitOOM`, following the
existing `exitReasonName` / `exitReasonDetail` / `exitChatMessage` trio. No new
reporting path — a new reason travelling the path that already exists.

An OOM-killed process is indistinguishable from any other `SIGKILL`: exit code 137,
no signal detail that separates them. The only reliable source is the scope's
`memory.events` `oom_kill` counter, which is why it **must** be read before the scope
disappears. `--collect` reaps a transient scope as soon as its last process exits, so
the read races the reap: wick reads `memory.events` on the exit path before returning
from `Wait()`, and treats a missing file as "unknown, not OOM" rather than guessing.
`dmesg` is not an alternative — it is not readable without root.

The message must carry numbers, because a number is what makes it actionable:

> Agent stopped: used 1.6 GB, over its 1.5 GB limit. Raise the limit in provider
> settings, or split the work into smaller steps.

For a wick-provider tool subprocess the agent does not die — the `tool_result`
carries the error:

> `grep` stopped: exceeded the 512 MB memory limit.

The agent reads that and can narrow its own scope, which is what makes a small tool
limit tolerable.

## Degradation

Scope isolation needs `systemd-run` on `PATH` and a working user bus
(`XDG_RUNTIME_DIR` set, DBus reachable). Neither holds in a container without
systemd, on Termux, or on Windows. Spawning must still succeed. Wick degrades and
**says so**:

- The probe runs once at startup and is cached: `systemd-run --user --scope --quiet
  --collect -p MemoryMax=64M -- /bin/true`. This mirrors the production preflight's
  first test, and answers the only question that matters — can this process create a
  scope at all.
- Layer 1 (`oom_score_adj`) still works; it is a plain file write needing no bus.
  This is orthogonal to scope availability but not to mode: layer 1 acts in `enforce`
  only, so an `enforce` install without scopes still gets victim biasing, while a
  `measure` install gets neither.
- Layer 2 is unavailable. **No silent substitute** — in particular, no fallback to an
  RSS-sampling watchdog that kills. A sampler cannot see a gigabyte allocated between
  two ticks, so substituting one would deliver the appearance of protection without
  its substance.
- The diagnostics page and `wick memory report` both show:
  *"scope isolation unavailable — systemd user session not reachable; agents run
  unguarded."*

Degrading silently leaves an operator believing they are protected when they are not.

## systemd unit

The generated unit (`internal/pkg/daemon/service_linux.go:163-178`) needs one change:

```ini
[Unit]
StartLimitIntervalSec=300
StartLimitBurst=3
```

`Delegate=yes` is **not** needed — that was required only by the rejected sub-cgroup
placement. A sibling slice needs nothing from wick's own unit, which is a large part
of why this placement was chosen: existing installations get the guard without
touching their service file.

The rate limit is still needed, and is independent of everything else here. Today's
`Restart=on-failure` with no limit turns one OOM into a loop: wick dies, restarts 5 s
later, resumes the session, respawns the agent, balloons again. After this change,
3 crashes in 5 minutes leaves the unit `failed` and stays there — an invisible loop
becomes a visible incident. Worth applying even if the memory guard is never enabled.

`wick install service` rewrites the unit; existing installations run unchanged until
they do.

## Testing

The mechanism is systemd and the kernel, so the tests target wick's decisions rather
than trying to reproduce a kernel OOM in CI. Everything below runs unprivileged, and
on Windows.

Pure logic, no root and no systemd:

- **argv construction** (`memscope`): given a limit and a slice, the produced argv
  contains `MemoryHigh=infinity` and `MemorySwapMax=0`. These two are the difference
  between a clean kill and the 116-minute stall, so they are asserted explicitly
  rather than trusted to review.
- **slice unit rendering**: content matches the aggregate limit; rendering is stable,
  so the idempotent write does not rewrite an unchanged file.
- **`memory.events` parsing**: `oom_kill > 0` → `ExitOOM`; `0` → existing
  classification; a missing or malformed file → "unknown, not OOM" and never a false
  `ExitOOM`.
- **`oomscore`**: writes the expected value to the expected path against a temp root;
  a write failure is logged, not fatal.
- **limit resolution**: per-instance overrides global, including the "may exceed
  global" case that distinguishes it from `MaxConcurrent`.
- **admission**: below `MinFreeMemoryMB`, the spawn queues instead of starting.
- **mode `off` writes nothing**: given a temp root, assert it stays empty — no slice
  unit, no `oom_score_adj`, no argv wrapping.
- **`_other.go` no-ops** report "unavailable" cleanly, so the Windows build passes.

Requires a Linux box with a systemd user session (`-tags integration`, skipped
elsewhere):

- **teardown regression** (task 7): spawn a tree inside a scope, `kill(-pgid)` it,
  assert no process survives. This is the one that gates the feature — wick's session
  teardown must survive the extra process `systemd-run` inserts. The production
  preflight already verifies this passes; the test exists so a later refactor cannot
  silently break it.
- **real OOM kill**: a deliberate memory hog in a 250 MB scope exits 137, and
  `memory.events` reports `oom_kill ≥ 1`.
- **blast radius**: while the hog dies, a sibling scope survives and wick's own RSS is
  unmoved. This is the actual claim of the whole design — that only the offending
  agent dies — and it is worth asserting directly rather than inferring.
