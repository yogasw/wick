# Memory Guard — raw-cgroupfs fallback (real enforcement without systemd)

**Related:** [[daemon-source-false-positive]] fixed the status *label* that
falsely implied protection existed here. This plan is the actual fix for
the underlying gap that label bug was covering up: on a systemd-less host
(this session's Fly.io Machine, `FLY_MACHINE_ID=2870671c072668`), memory
enforcement genuinely did not exist before this change. It does now.

**Goal:** give `memory_guard_mode=enforce` a real, kernel-enforced
per-agent memory ceiling on a machine with no systemd — using cgroup v1
directly, which we proved by hand is writable on this exact host — instead
of leaving `auto`/`scope` permanently unable to do anything but bias
`oom_score_adj` and gate admission.

**Not deployed to the live `wick-agent` binary on this host.** Everything
below happened in the same throwaway clone as the earlier fix
(`files/wick`), verified with a real, disposable binary built against it.
The running production process still lacks this until someone builds and
swaps that binary — a separate step from what's recorded here.

---

## STATUS — 2026-08-17

**Implemented, unit-tested, and verified end-to-end against this host's
real cgroup v1 mount — a live smoke test actually created a cgroup,
applied a limit, ran a process inside it, and read back the kernel's own
usage counter. Not upstreamed (no PR, per standing instruction from the
earlier session).**

---

## Why this is possible here

Diagnosing the earlier "scope isolation unavailable" message, we confirmed
by hand:

```
$ mount | grep cgroup
cgroup on /sys/fs/cgroup/memory type cgroup (rw,...,memory)

$ mkdir /sys/fs/cgroup/memory/test-probe && ls .../test-probe/
memory.limit_in_bytes  cgroup.procs  memory.max_usage_in_bytes  ...
```

The kernel exposes a fully writable cgroup v1 memory controller. What was
missing was systemd — the *manager* wick was built to drive that
controller through (`systemd-run --user --scope`). The controller itself
was never the problem. This plan drives it directly instead.

## Design

`memscope.Available()` used to mean exactly one thing: "can we
`systemd-run`?". It's replaced with a ranked `Backend` choice
(`internal/agents/provider/memscope/backend_linux.go`):

```
DetectBackend() → tries systemd-run probe first (unchanged from before)
                → falls back to a cgroupfs probe (new)
                → BackendNone if neither works
```

`Available()` still exists (`DetectBackend() != BackendNone`) so nothing
that only ever needed a yes/no had to change.

**The one thing systemd-run does that plain cgroupfs manipulation cannot**:
place a process in a fresh cgroup *and then run a program inside it*, in
one step, from the outside. mkdir + write memory.limit_in_bytes + write
cgroup.procs only works from *inside* the process being placed — you
cannot move another process into a cgroup and have it come up already
confined from birth without something in its own exec path doing that
join. So the cgroupfs backend's `Wrap()` doesn't call cgroup-tools on the
target from outside; it re-execs **wick's own binary** through a new
hidden subcommand, which joins the cgroup on its own first pid and then
`syscall.Exec`s into the real agent binary — same PID throughout, so
`kill(-pgid)` teardown is untouched by construction, not merely verified:
`execve` is POSIX-guaranteed to preserve pid and pgid. (The systemd path
has to *verify* this empirically — see
`teardown_integration_test.go` — because `systemd-run` inserts a real
external process between wick and the agent. The cgroupfs path has no
such process to verify against: there never is one.)

```
                    (no systemd anywhere on this host)
memguard.Wrap()
   │  backend == BackendCgroupFS
   ▼
argv = [<wick binary>, "__agent-exec", --root= --slice= --unit= --limit-mb=, --, <real bin>, <real args>]
   │
   ▼  (spawned exactly like the systemd-run argv was — same call site,
   │   same procgroup.Apply / Setpgid, unchanged)
wick __agent-exec
   │  mkdir root/slice/unit.scope
   │  write memory.limit_in_bytes (if LimitMB > 0)
   │  write own pid → cgroup.procs
   ▼
syscall.Exec(realBin, realArgs, environ)     ← same pid, same pgid, becomes the agent
   │
   ▼
kernel enforces memory.limit_in_bytes on that cgroup's whole tree
```

## What this backend cannot give you, and does not pretend to

cgroup v1 has no per-group equivalent of v2's `memory.events` `oom_kill`
counter. The systemd/v2 path can say "used 1.5GB, over its 512MB limit,
confirmed by a kernel kill counter". This backend can say "used 1.5GB, in
a group with a 512MB limit" — real numbers, both kernel-sourced — but
cannot *confirm from the counter alone* that a specific exit was the
limit being hit. `ReadStatsV1At` therefore always returns `OOMKills: 0`,
on purpose, documented at the point it does it. `ClassifyExit`'s existing
"no evidence → not an OOM" rule (`internal/agents/provider/memguard.go`)
already does the right thing with that: it degrades to the ordinary exit
path rather than fabricating a verdict. **The limit still kills — only
the after-the-fact explanation is weaker, never the enforcement itself.**

## Files

New (`internal/agents/provider/memscope/`):

| File | What it holds |
|---|---|
| `backend_linux.go` | `Backend` enum, `DetectBackend()` (systemd → cgroupfs → none), `Available()` kept as a derived yes/no |
| `backend_other.go` | Same vocabulary off Linux; `DetectBackend()` always `BackendNone` |
| `cgroupfs_linux.go` | `cgroupFSProbe`, `EnsureCgroupSlice` (aggregate limit via `memory.use_hierarchy`), `WrapArgvCgroupFS` (builds the self-reexec argv) |
| `cgroupfs_other.go` | Stubs so `memguard.go` (platform-agnostic) compiles everywhere |
| `execagent_linux.go` | `RunAgentExec` — the receiving end of the re-exec: join cgroup, then `execFn` (var, stubbed in tests) |
| `execagent_other.go` | Stub |
| `readv1.go` | `ReadStatsV1At` — parses `memory.max_usage_in_bytes`; untagged like `read.go` so it's tested on every platform |

Changed:

| File | What changed |
|---|---|
| `memscope.go` | Added `AgentExecSubcommand` constant |
| `install_linux.go` | Removed the old `Available()`/probe (moved into `backend_linux.go`) |
| `read_linux.go` | `ReadStats` tries the systemd/v2 path, falls back to the v1 path |
| `internal/agents/provider/memguard.go` | `wraps()`/`Wrap()` now dispatch on `Backend`, not a bool; split into `wrapSystemd` (unchanged behavior) and `wrapCgroupFS` (new); added `selfExecutable` seam |
| `app/app.go` | Registered the new hidden subcommand |

New: `app/agentexec_cmd.go` — thin cobra wiring for `wick __agent-exec`
(`Hidden: true`), delegates straight to `memscope.RunAgentExec`.

## Tests written

`internal/agents/provider/memscope/`:

- `cgroupfs_linux_test.go` (7 tests) — probe against a missing root
  (hermetic) and against the real host mount (skips gracefully where
  unavailable, mirroring `teardown_integration_test.go`'s own pattern);
  `EnsureCgroupSlice` directory creation, byte conversion, zero-means-none;
  `WrapArgvCgroupFS` argv shape, both nonzero and zero limit.
- `execagent_linux_test.go` (5 tests) — `RunAgentExec` with `execFn`
  stubbed (a real exec would replace the test binary and the test would
  never report a result): joins the cgroup with its own pid before
  "exec", writes the byte-converted limit, measure-mode (limit 0) still
  joins without writing a ceiling, bin/args reach the exec call
  unmodified, and a root that cannot be a directory (`ENOTDIR` — chosen
  over a permission-bits test specifically because this suite may run as
  root, which bypasses permission bits and would make that version flaky)
  fails without ever reaching exec.
- `readv1_test.go` (4 tests) — peak reported; **OOMKills is always 0,
  pinned explicitly** as the documented limitation, not an oversight;
  missing scope reads as unknown; malformed counter doesn't panic or
  invent a number.

`internal/agents/provider/memguard_test.go` — extended, not replaced:

- `withScopesAvailable` now drives the new `memscopeBackend` seam
  (`BackendSystemd` when true) — every pre-existing test kept its exact
  original assertions unchanged.
- Two new tests: `TestMemGuard_CgroupFSBackendReExecsSelf` (backend =
  cgroupfs → `Wrap` returns wick's own binary + `__agent-exec` + the real
  command intact after `--`) and
  `TestMemGuard_CgroupFSBackendDegradesWhenSelfPathUnresolvable`
  (`os.Executable` failing degrades to an unwrapped, unguarded spawn — an
  agent that runs beats one that doesn't start at all, the same
  degrade-rather-than-refuse rule the systemd path already followed for a
  failed `EnsureSlice`).

## Verification performed

```bash
go build ./internal/agents/provider/memscope/...     # linux, windows, darwin, android — all clean
go vet   ./internal/agents/provider/memscope/...      # all clean
go test  ./internal/agents/provider/memscope/... -v   # 30/30 PASS
go test  ./internal/agents/provider/... -count=1      # all packages PASS, no regressions
go vet   $(go list ./... | grep -v .../template)      # clean (template/ is a pre-existing,
                                                        #   unrelated scaffold-generation gap)
go build .                                             # full `wick` dev-CLI binary — clean
```

**`templ generate`** was run once (`go run github.com/a-h/templ/cmd/templ@latest generate`)
to produce the gitignored `*_templ.go` files a fresh clone is missing —
unrelated to this change, but needed to get a true full-repo build for a
real end-to-end check rather than a per-package one.

### The real smoke test — not simulated

`app.Run()` (in `app/`) is a *library* entry point; the repo's own
`main.go` builds a different binary (`cmd/cli`, the developer-facing
`wick` tool) that never calls it. Confirmed this the hard way — the first
build's binary didn't even contain the string `agent-exec`. Built a
throwaway two-line `main.go` in a separate temp module with a `replace`
directive back to this clone, calling `app.Run()`, to get a binary that
actually contains the code path production `wick-agent` runs:

```bash
$ /tmp/wick-agent-built __agent-exec \
    --root=/sys/fs/cgroup/memory --slice=wick-smoke-test.slice \
    --unit=smoke-1 --limit-mb=64 -- /bin/echo hello-from-cgroup
hello-from-cgroup
$ echo $?
0

$ cat /sys/fs/cgroup/memory/wick-smoke-test.slice/smoke-1.scope/memory.limit_in_bytes
67108864                                    # exactly 64 * 1024 * 1024
$ cat .../smoke-1.scope/memory.max_usage_in_bytes
262144                                      # the kernel's own recorded peak for that process
$ cat .../smoke-1.scope/cgroup.procs
(empty — echo already exited, correctly left the group)
```

Every number came from the kernel, not from wick's own bookkeeping. Cleaned
up (`rmdir`) after confirming.

## Found in review, fixed

- **Scope directories leaked.** `systemd-run --collect` reaps a transient
  scope when its last process exits; raw cgroupfs has no daemon behind it,
  so every `RunAgentExec` left its directory behind. `ScopeUnitName`
  carries an increasing sequence, so names never repeat — that is one new
  permanent directory per spawn, growing without bound on a long-running
  host. The smoke test above hid it: the `rmdir` was run by hand.

  Fixed with `RemoveCgroupScopeAt` / `RemoveCgroupScope` and
  `MemGuard.ReleaseScope`, called from `agent.go`'s exit path **after**
  `ClassifyExit` — removing the group takes its accounting files with it.
  Unconditional on exit reason: a clean exit leaks exactly like a crash.
  `rmdir`, not `RemoveAll`, so a group that still holds a process refuses
  (EBUSY) rather than being silently forgotten.

- **`TestMemGuard_CgroupFSBackendReExecsSelf` failed on Windows.** It sat
  in the untagged `memguard_test.go` but asserted Linux-only behaviour:
  `cgroupfs_other.go`'s `WrapArgvCgroupFS` returns the command unchanged,
  so `bin` stays the agent binary off Linux. Green on Linux, red on
  Windows — the PR's "no regressions" claim was true only on the platform
  it was tested on. Moved to `memguard_cgroupfs_linux_test.go`; the
  branch-selection cases stay untagged, since that logic is
  platform-agnostic.

- **Stale doc reference.** `readv1.go` described a `ReadStatsCgroupFS`
  that does not exist; production reaches the v1 reader through
  `ReadStats`, which tries v2 first and falls back.

## Explicitly not done

- **No PR / no push upstream.**
- **Live `wick-agent` on this host is unchanged.** All of this ran in the
  clone and a disposable test binary; production still lacks the
  cgroupfs fallback until someone rebuilds and swaps it in — a distinct,
  explicit step.
- **`memory_guard_method` dropdown in `internal/agents/config/general.go`
  was not touched.** `auto` and `scope` both already resolve through
  `DetectBackend()`, so the new fallback activates automatically under the
  existing values — no new option was strictly required for it to work.
  A future improvement could still surface which backend is active on the
  Resources page (today it only distinguishes "available" vs not); not
  done here.
- **No live OOM-kill test.** Verifying the *kill* itself (as opposed to
  the setup that makes a kill possible) would mean deliberately running a
  process past its ceiling on a shared host — declined for safety. Cgroup
  v1's `memory.limit_in_bytes` enforcement is long-standing, well-known
  kernel behavior; what was actually in question here — and now verified —
  was whether wick could drive it correctly on this specific host, not
  whether Linux cgroups work.
