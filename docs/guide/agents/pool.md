---
outline: deep
---

# Pool & Sessions

The **pool** caps how many agent subprocesses run at once across all sessions, FIFO-queues the rest, kills idle ones, and revives them with `--resume` when new messages arrive.

A **session** is what holds the conversation: routing key, agent registry, log files, optional project binding.

::: info Source
Pool: [`internal/agents/pool/`](https://github.com/yogasw/wick/blob/master/internal/agents/pool) — [`pool.go`](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go) (slot allocation), [`buffer.go`](https://github.com/yogasw/wick/blob/master/internal/agents/pool/buffer.go) (message buffer), [`factory.go`](https://github.com/yogasw/wick/blob/master/internal/agents/pool/factory.go) (build agents).
Session: [`internal/agents/session/`](https://github.com/yogasw/wick/blob/master/internal/agents/session) — [`session.go`](https://github.com/yogasw/wick/blob/master/internal/agents/session/session.go) (Meta, Create/Load/SetProject), [`agents.go`](https://github.com/yogasw/wick/blob/master/internal/agents/session/agents.go) (per-session AgentEntry).
:::

## Mental model

```
┌──────────────────────────── Pool ────────────────────────────┐
│                                                               │
│   active map  ┌──────────────────────────────┐                │
│   (max=2):    │ slot 1: sess-A / "default"   │                │
│               │ slot 2: sess-B / "reviewer"  │                │
│               └──────────────────────────────┘                │
│                                                               │
│   queue:      [sess-C, sess-A/"backend"]                      │
│                                                               │
│   buffers:    sess-A → ["msg1", "msg2"]   ← drained on grant  │
│               sess-D → ["pending..."]      ← persisted to     │
│                                              meta.PendingInput│
└───────────────────────────────────────────────────────────────┘
```

| Knob | Default | What | Source |
|---|---|---|---|
| `MaxConcurrent` | 2 | Subprocess cap across all sessions. | [pool.go:159](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go#L159) |
| `IdleTimeout` | 120s | Time without I/O before subprocess kill. Timer pauses while output streams. | [pool.go:162](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go#L162) |
| `KillAfterIdle` | 0 | Extra grace seconds after idle timeout. | [pool.go:56](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go#L56) |
| Queue | FIFO | Sessions waiting for a slot. | [pool.go:38](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go#L38) |
| Revive | automatic | New message → spawn with `--resume <cli_session_id>`. | [pool.go:264](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go#L264) |

## Session anatomy

A session lives at `~/.<app>/agents/sessions/<id>/` ([layout.go:51](https://github.com/yogasw/wick/blob/master/internal/agents/config/layout.go#L51)):

```
sessions/<id>/
├── meta.json            ← session.Meta
├── agents.json          ← []AgentEntry (per-session named agents)
├── agent.md             ← snapshot of the active preset
├── conversation.jsonl   ← user/assistant turns (append-only)
├── commands.jsonl       ← legacy per-session gate log (kept for compat)
└── raw.jsonl            ← raw stream events (optional)
```

`session.Meta` ([session.go:51-60](https://github.com/yogasw/wick/blob/master/internal/agents/session/session.go#L51)):

```go
type Meta struct {
    ProjectID    string    // project id; "" = per-session temp dir
    Origin       Origin    // "slack" | "ui" | "api"
    ChannelID    string    // Slack channel ID (Slack-only)
    ActiveAgent  string    // current agent in agents.json
    Status       Status    // "idle" | "queued" | "running"
    CreatedAt    time.Time
    LastActive   time.Time
    PendingInput []string  // buffered messages — survives wick restart
}
```

`PendingInput` is the on-disk twin of the in-memory message buffer (next section). Survives wick restart so a session that was queued at shutdown gets its messages drained on next boot.

### Session ID by origin

| Origin | ID format | Set by |
|---|---|---|
| `slack` | Slack `thread_ts` (e.g. `1715167891.234567`) | [`channels/slack.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack.go) |
| `telegram` | `tg-<chatID>` | [telegram.go:242](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram.go#L242) |
| `ui` | UUID minted by the web UI | [`internal/tools/agents/handler.go`](https://github.com/yogasw/wick/blob/master/internal/tools/agents/handler.go) |
| `api` | UUID (future) | — |

## Per-session agents

One session can hold many named agents (e.g. `backend`, `reviewer`, `default`); only one is active at a time. Each agent in `agents.json` ([session/agents.go](https://github.com/yogasw/wick/blob/master/internal/agents/session/agents.go)):

```go
type AgentEntry struct {
    Name          string    // unique within session
    Provider      string    // provider type ("claude" / "codex" / "gemini" / "omp" / "opencode")
    CLISessionID  string    // <-- key to resume; written when CLI emits SessionStart
    Status        Status
    CreatedAt     time.Time
    LastActive    time.Time
    MaxTurns      int       // --max-turns cap; 0 = unlimited (provider default)
}
```

`CLISessionID` is captured from the CLI's `SessionStart` event by [`event.ClaudeParser`](https://github.com/yogasw/wick/blob/master/internal/agents/event) and persisted by [`store.Store`](https://github.com/yogasw/wick/blob/master/internal/agents/store). Switch agent via the `/agent <name>` meta-command — the previous agent stays in `agents.json` but the new one becomes `ActiveAgent`.

## Send flow

When a message arrives ([pool.go: `Send`](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go#L177)):

```
1. Look up active map[sessionKey(sess, agent)] → cache hit?
   └─ yes → entry.agent.Send(text) — no spawn, no buffer
   └─ no  → continue

2. Ensure session exists on disk (channels pass thread_ts; auto-create if missing)
3. Append text to in-memory Buffer + persist to meta.PendingInput
   AND append the user turn to conversation.jsonl so a page refresh
   while the session is queued/spawning still shows the messages.

4. mu.Lock — check capacity
   ├─ already mid-spawn? → return (in-flight spawn will drain buffer)
   ├─ active+spawning < max? → mark spawning, release lock, spawn()
   └─ pool full → enqueue (dedup: skip if same sess+agent already queued),
                  mark session status=queued, fire one-shot PreemptIdleSlot,
                  return

5. spawn():
   - load session.Meta → resolve project cwd (project.ResolvePath, fallback chain)
   - no agents.json entry yet? → pick provider: session's project default →
     global `agents.default_provider` → (still empty) per-type `claude`
     default in factory.go, then AddAgent(provider)
   - look up CLISessionID for resume
   - factory.Build(FactoryOptions) → returns Agent + State + Store + OnStarted hook
   - drain Buffer into one combined input
   - markStatus(running)
   - a.Start(ctx) → fires CLI subprocess
   - OnStarted(pid, binary, argv, firstUserMessage) → completes spawn-log start event
   - if drained text non-empty → a.Send(combined)
     (user turns were already persisted in step 3 — no double-write)
```

This is the path a channel (Slack/Telegram/REST) takes for a brand-new session — it never goes through the UI's New Session composer, so this provider precedence is the only place a channel-spawned session picks up its project's configured default provider.

The "spawning" set ([pool.go:35](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go#L35)) is what prevents two concurrent `Send` calls from each seeing "slot free" and both calling `spawn` at once. In-flight spawns count against the cap.

### Message buffer

Persistence model ([buffer.go](https://github.com/yogasw/wick/blob/master/internal/agents/pool/buffer.go)):

| Operation | What |
|---|---|
| `Append` | Append to `lines[]` + persist `lines` snapshot to `meta.PendingInput`. |
| `Drain` | Join all lines with `\n`, clear in-memory + persist `nil` to `meta.PendingInput`. |
| `NewBuffer` | Reads `meta.PendingInput` into `lines[]` so a wick restart resumes. |

When the slot is granted, the entire buffer is **drained as one combined input** (joined by `\n`) and sent as a single message to the spawned agent. So a queued session that received three messages while waiting gets all three delivered in one turn. See agents-design.md §5.1.1 for the rationale.

Each user message is also written to `conversation.jsonl` at Send time (not at drain time), so the UI's conversation tab shows the messages even before the subprocess spawns. Without that, refreshing the page while queued would render "No messages yet" — the messages would live only in `meta.PendingInput`, which the conversation view doesn't read.

### Preemption

When the pool is full and a queued session has been waiting, `PreemptIdleSlot` finds the longest-idle active session (Lifecycle == Idle, oldest LastActive) and stops it so the slot frees up. The victim keeps its `CLISessionID` on disk and resumes via `--resume` on its next message.

Preemption fires from two places:

| Trigger | When | Notes |
|---|---|---|
| `Send` (one-shot) | At the moment a session enqueues | Skipped if no active session is currently Idle. |
| `preemptLoop` (1 s ticker) | Background, while `len(queue) > 0` | Closes the gap where every active was Working at enqueue time but later went Idle — without the retry, the queue would wait out the full idle TTL. Only runs when `PreemptIdle = true`. |

## Spawn environment (Claude)

Each Claude spawn includes some fixed extra flags:

| What | Flag(s) | Why |
|---|---|---|
| MCP tool pre-approval | `--allowedTools mcp__wick__wick_list,...` | Headless agents can't answer an interactive permission prompt. All five wick meta-tools (`wick_list`, `wick_search`, `wick_get`, `wick_execute`, `wick_list_providers`) are pre-approved automatically so MCP tool calls don't stall. |
| Skills dir | `--add-dir ~/.claude/skills` | Agents can read skill files bundled outside the workspace. Only added when `~/.claude/skills/` exists on disk. The system-prompt path table carves out `~/.claude/skills/**` (and the matching `~/.codex/skills/**`, `~/.gemini/skills/**`, `~/.agents/skills/**`) as read-allowed while the rest of `~/.claude/**` remains denied. |
| Max turns | `--max-turns N` | Only added when `max_turns > 0` (set on the workflow agent node). `0` = omit the flag, letting the provider default apply. |

`WICK_CLAUDE_STDERR_LOG` (env var, unset by default) redirects the spawned Claude process's stderr to a file instead of wick's stderr. Regardless of this setting, the last ~4 KB of stderr is always captured in memory so abnormal exits can surface the real error in logs (`exit_code` + `stderr_tail` fields).

## Spawn environment (omp, opencode)

Both run **one process per turn** (send mode `queue`, like codex): a message sent mid-turn waits for the turn to end. The prompt goes in on stdin, which is then closed, so it never shows in process listings and a prompt starting with `-` is not read as a flag.

| | `omp` | `opencode` |
|---|---|---|
| argv | `--profile <p> -p --mode json --no-title --auto-approve --cwd <ws> [--append-system-prompt <soul.md>] [--model m] [--resume <sid>]` | `run --format json --thinking --auto [--model m] [--session <sid>]` |
| Account | the instance's omp profile | `XDG_DATA_HOME=<instance data dir>` |
| wick system prompt | `<session>/.omp/soul.md` via `--append-system-prompt` | `<session>/.opencode-wick/soul.md` as an extra `instructions` entry — the project's `AGENTS.md` still loads |
| wick MCP | a static `wick` entry in `~/.omp/profiles/<p>/agent/mcp.json` with `${WICK_MCP_URL}` / `${WICK_MCP_TOKEN}` placeholders; the values come from the spawn env, so the token is never written to disk | `OPENCODE_CONFIG_CONTENT` (`mcp.wick`, type `remote`, `{env:…}` placeholders) |
| Permissions | `--auto-approve` | `--auto` + `"permission": "allow"` |
| Sharing | — | `"share": "disabled"` is forced, so a user/project config with `share: auto` (or `OPENCODE_AUTO_SHARE`) can never publish a wick session |
| Resume id | `id` of the first `{"type":"session"}` line | `sessionID` on every line |
| Skills | `~/.agents/skills` (omp's native user root) + wick's shipped catalog in the system prompt | `~/.claude/skills` / `~/.agents/skills` natively + the catalog |

### MCP per user, and nothing from the host

Each spawn's MCP is wick's server with **that session's** bearer, plus the instance's own `extra_mcp_servers` — never the host user's MCP config.

| | `omp` | `opencode` |
|---|---|---|
| Host sources removed | per-spawn `--config <session>/.omp/wick-settings.yml` overlay (outranks project + global settings): `mcp.enableProjectConfig: false` drops every project-level MCP file (`.omp/mcp.json`, `.mcp.json`, `.claude/`, `.cursor/`, `.vscode/`, `opencode.json`); `enabledProviders: []` keeps foreign **user** configs (`~/.claude.json`, `~/.claude/mcp.json`, `~/.cursor`, …) at their opt-in-off default; `CLAUDE_CONFIG_DIR` and `PI_CONFIG_FILES` are blanked (omp turns `~/.claude` on whenever `CLAUDE_CONFIG_DIR` is set) | `XDG_CONFIG_HOME=<instance data dir>/config`, so `~/.config/opencode` is never merged; `OPENCODE_CONFIG` / `OPENCODE_CONFIG_DIR` blanked; MCP servers declared in the project's `opencode.json(c)` / `.opencode/` and `~/.opencode` are set `enabled: false` in wick's last-merged inline layer |
| Not used, and why | `disabledProviders` would also switch off those providers' **skills** and context files | `OPENCODE_DISABLE_PROJECT_CONFIG` would also drop the project's `AGENTS.md` rules |
| Extra servers | merged into the profile `mcp.json` next to `wick`; entries wick wrote earlier and no longer wants are removed (tracked in `wick-mcp-managed.json`), hand-added entries stay | merged into the inline `mcp` block, `${VAR}` rewritten to `{env:VAR}` |

`extra_mcp_servers` (Providers → instance → Configuration) is JSON in `mcpServers` shape — `{"github": {"type": "http", "url": "…", "headers": {"Authorization": "Bearer ${GITHUB_TOKEN}"}}}` or a stdio `{"command": …, "args": […], "env": {…}}`. The name `wick` is reserved. A header/env value whose key looks like a credential must be a `${VAR}` reference to the instance **Env**; plaintext is refused, so no secret is ever written to a config file.

Proof: `WICK_E2E_MCP_ISOLATION=1 WICK_E2E_OMP_BIN=… WICK_E2E_OPENCODE_BIN=… go test ./internal/agents/provider/{omp,opencode} -run MCPIsolation -v` runs the real binaries with a HOME/project full of dummy MCP configs: the dummy server gets **no** request from a wick spawn, while an unisolated control run does reach it.

### opencode never picks a model on its own

Without `--model`, opencode quietly runs its hosted default (`opencode/…`, opencode Zen), which sends the whole conversation to opencode's servers even with no login. So an opencode spawn always passes `--model`: a session pin, else `--model`/`-m` in the instance args, else the instance's `opencode_model`. None → the spawn is refused ("log in first or pick a model"). `opencode/…` models additionally require `opencode_allow_hosted` (default off; the Providers page warns while it is on).

### No self-update

`OPENCODE_DISABLE_AUTOUPDATE=true` on every opencode spawn, login, probe and install check; omp's overlay sets `startup.checkUpdate: false` and `marketplace.autoUpdate: off` (and omp only checks for updates in interactive mode anyway). A wick-managed binary therefore keeps the sha256 recorded when it was installed.

The gate hook is **off** for both (their hooks are TS/JS extensions, not a command contract), which is why approvals are bypassed unconditionally. Both binaries go through the memory-guard shim like claude/codex.

Parsing: `event.OMPParser` ends the turn at `agent_end` unless `isTerminal` is `false`; `event.OpencodeParser` ends it at a `step_finish` whose reason is not `tool-calls`, and turns each `tool_use` line (opencode only reports finished tools) into a tool start **and** result. An error whose text looks like a rate limit / quota becomes `akun instance <type>/<name> kena limit/kuota: …`, naming which login ran dry.

MVP limits: no long-lived process (`omp --mode rpc` / `opencode serve` are later), no failover between instances when an account hits its limit, no gate.

## Exit flow

When the agent subprocess exits ([pool.go: `onAgentExit`](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go#L369)):

```
1. state.MarkKilled()
2. session.markStatus(idle)               ← MUST run before releaseSlot (see below)
3. releaseSlot(key) — delete active[key]
4. OnLifecycle(killed)
5. tryGrantQueue() — pop head, spawn next queued session
```

::: warning Order matters
`markStatus(idle)` runs **before** `releaseSlot` ([pool.go:378](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go#L378)). The reverse order causes a Windows-specific race: a fast `Send` arriving right after `Active==0` could see the slot empty, call `spawn`, and have its meta.json write collide with the trailing idle write (two `os.Rename` to the same target).
:::

The body runs under `p.wg` so `Stop()` can wait for tail work to finish before tearing down.

## Stuck and silent turns

A turn can go quiet without being dead: a long `bash`, a build, a sub-agent. The idle timer ([`agent.go`](https://github.com/yogasw/wick/blob/master/internal/agents/provider/agent.go): `idleKill`) must tell that apart from a hung one, and whichever way a turn ends, the person watching has to see what it had done and why it stopped. This section records why it works the way it does, because the first version of each rule below was learned from a real stuck session.

### What went wrong (2026-10-02, opencode session stuck "running")

1. opencode reports a tool only once it has **finished**. A long tool or a `task` sub-agent therefore looked like a silent turn, and the 120 s idle timer killed it. The sub-agent runs in its *own* opencode session, whose frames the translator drops (it filters on session id), so nothing told wick it was still working.
2. The kill closes the stdout pipe, and the reader reached EOF **before** the idle goroutine recorded why. The exit was filed as `ExitClean`. `Pool.HandleExit` treats a clean exit of a respawn provider (opencode, omp, codex) as a *turn boundary* and returns early: session status stayed `running`, the slot was not released, and `inflight.jsonl` was only merged into `conversation.jsonl` at the next wick boot. The UI showed a spinner with no answer.

### Rules

| Rule | Where | Why |
|---|---|---|
| An idle kill is always recorded as `ExitIdle` (`idleKilled` is set **before** `Kill`) | `agent.go` `idleKill` + the `drained` path | The kill is what closes the pipe; without the flag the reader wins the race and reports `ExitClean` |
| A turn killed mid-flight is flushed to `conversation.jsonl` with the cause ("no output for N s … idle timer stopped it") | `idleKill` → `store.Flush` | The reader sees the partial work and the reason; between turns (state idle) nothing is written |
| The idle timer asks the process first (`BusyReporter.Busy()`); a process that says it is still working is left alone | `spawner.go`, `agent.go` `procBusy` | Silence on the stream is not death when the work runs on a server wick can ask |
| opencode announces a started tool (`tool_running` frame → `ToolUse`) and the finished frame only closes it | `opencode/translate.go`, `event/opencode.go` | Lets the agent hold its idle timer while a tool runs; no duplicate tool call in the transcript |
| opencode: a dropped `/event` stream is reconnected while the session is still busy (backoff 1, 2, 4 … s, max 10 tries), missed parts are replayed from `GET /session/{id}/message`, each passed on once | `opencode/remote.go` `reconnect`, `resync` | The server keeps running the turn when the connection drops; ending the turn there threw the work away |

### Per provider

| | claude | codex | gemini | omp | opencode |
|---|---|---|---|---|---|
| `ExitIdle` always recorded, status back to idle, partial turn flushed with the cause | yes | yes | yes | yes | yes |
| Idle timer asks the process (`Busy()`) | no | no | no | yes (`get_state`: streaming, compacting or pending async work) | yes (`GET /session/status`) |
| Started-tool event, so a long tool is not silence | not changed (the agent loop pauses the idle timer from `ToolUse` to `ToolResult`; whether the claude parser reports tool starts early was not re-checked) | not changed (not re-checked) | not changed (not re-checked) | already sent (`tool_execution_start`) | yes |
| Reconnect and resync | no | no | no | not applicable | yes |

- **omp** talks to its child over stdio RPC. A dropped pipe *is* a dead process, so "connection lost but process alive" cannot happen and there is nothing to reconnect to.
- **claude, codex, gemini** run the turn inside the process itself, so it cannot be re-attached. After an idle kill the next user message respawns with `--resume`, as for any idle reap; wick does not replay the cut-off turn by itself.
- The reconnect budget (about 17 minutes in total) is deliberately long: turns of 30 minutes are normal here. A server that is *gone* is detected at once (`/session/status` fails), not after the full budget.
- The silence watchdog in `opencode/remote.go` (`silentTurnTimeout`) pauses during a reconnect, because that gap is not the model's silence.

### Not done

Activity-based liveness for claude, codex and gemini (for example a child process still running or CPU still moving) and an automatic resume after an idle kill. Both need a decision on how often a turn may be repeated.

## Resume flow

The point of `CLISessionID` is to make the kill-revive cycle invisible to the user.

```
T+0s    User sends message → spawn → CLI emits SessionStart with id "abc-123"
        → store captures id → agents.json entry gets CLISessionID="abc-123"
        → conversation streams normally

T+120s  No I/O for IdleTimeout → state.MarkIdle → state.MarkKilled
        → onAgentExit → session.Status=idle, slot released
        → CLI subprocess gone, but conversation log + agents.json intact

T+5min  User sends new message
        → spawn → load agents.json → CLISessionID="abc-123"
        → factory.Build with ResumeID="abc-123"
        → CLI spawns with --resume abc-123 → restores its own context
        → conversation continues seamlessly
```

The CLI is responsible for replaying its own conversation context from the resume ID — wick doesn't replay `conversation.jsonl` into the subprocess.

### Stale resume self-heal

If a `--resume` spawn exits with "No conversation found" (the CLI can't find the stored session, e.g. after a full Claude data clear), the pool automatically clears the stale `CLISessionID` from `agents.json`. The next spawn starts fresh instead of retrying a dead ID. A log line at `INFO` level records the clear.

The format of the resume ID is CLI-specific:

| CLI | Where it comes from | How to pass it |
|---|---|---|
| Claude | `system.subtype=init` event | `claude --resume <id>` |
| Codex | `thread.started` event | `codex --resume <id>` (when phase 6 lands) |
| Gemini | `init` event | env `GEMINI_SESSION_ID` (when phase 6 lands) |

Today, only Claude is wired end-to-end. Codex / Gemini parsers are stubs in [`internal/agents/event/`](https://github.com/yogasw/wick/blob/master/internal/agents/event); resume flow ships when those parsers land.

## Project cwd resolution

[`pool.resolveCwd`](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go) at spawn time:

::: info CWD stability for resumable sessions
Once a session has a `CLISessionID` (i.e. a real conversation exists), project backfill is skipped. Changing the project binding after that point would move the cwd, which would break `--resume` (Claude's resume is per-cwd). If you need to change the project for an existing session, reset it first.
:::

1. `sess.Meta.ProjectID` non-empty → `project.ResolvePath(layout, id)`. Returns custom path or `<base>/projects/<id>/files/`.
2. Empty → per-session temp dir at `sessions/<id>/cwd/`. Created on demand.

The pool `MkdirAll`s managed paths before `exec.Cmd.Dir`. Custom paths are assumed to still exist; if you deleted yours, spawn surfaces a clean error.

## Restart recovery

`Pool.New` returns an empty pool. Wick boot ([`server.go`](https://github.com/yogasw/wick/blob/master/internal/pkg/api/server.go)):

1. Construct pool with config.
2. **Don't** auto-spawn for sessions whose previous status was `running` — those subprocesses are already dead. Their `agents.json` keeps the `CLISessionID`, so the next message from any channel revives them via the resume flow.
3. Channels start, listeners come online, business as usual.

The only thing the pool does NOT recover by itself: a session that was `queued` at shutdown with messages in `PendingInput`. The next inbound message to that session will trigger `Send`, which goes through `bufferFor` — `NewBuffer` reads `PendingInput` into `lines[]`, the new message gets appended, and the combined drain goes to the agent on its first slot.

## Reset

Reset ([`/reset` meta-command](./channels#meta-commands)):

```
1. Kill subprocess if alive
2. Truncate conversation.jsonl, commands.jsonl, raw.jsonl (keep _meta header)
3. Clear CLISessionID in agents.json (so next send is fresh, no --resume)
4. Re-snapshot agent.md from preset
5. Re-merge CLAUDE.md (project-level + agent.md)
```

Useful for "the agent went down a wrong path; start fresh."

## Delete

Session delete ([`session.Delete`](https://github.com/yogasw/wick/blob/master/internal/agents/session/session.go#L174)):

```
1. Kill subprocess if alive
2. rm -rf sessions/<id>/
3. Project files left alone (projects are shared)
```

## Telemetry hooks

Pool fires two callbacks the UI subscribes to:

| Hook | Fires when | Use |
|---|---|---|
| `OnSessionCreated(sess)` | Pool auto-creates a session for an inbound channel message | Register session into `manager.Manager` so the dashboard sees it without reload. |
| `OnLifecycle(LifecycleEvent)` | `spawning` (post-Start) and `killed` transitions | UI badges, spawn-log enrichment. |
| `OnUserMessage(UserMessageEvent)` | A `role=user` turn is sent from a non-`"ui"` source (channel or [schedule runner](./scheduled-messages)) | Push a `user_message` SSE event so an already-open web session renders the turn live. See [Channels ▶ SSE event vocabulary](./channels#sse-event-vocabulary). |

`Idle` / `Working` transitions are NOT routed via `OnLifecycle` — they're implicit from the event flow. UIs that want every transition subscribe to `AgentEvent` via the factory's `OnEvent`.

## See also

- [Channels](./channels) — where `SendFunc` is called from.
- [Scheduled Messages](./scheduled-messages) — another non-web caller of `Send`, via the schedule runner.
- [Projects](./projects) — `cwd` resolution.
- [Providers](./providers) — `FactoryOptions.ProviderType` / `ProviderName` forwarding.
- [Memory Guard](./memory-guard) — the byte-limit axis this pool's `MaxConcurrent` doesn't cover.
- [Command Gate](../command-gate) — gate's PreToolUse hook fires inside the spawned subprocess; pool doesn't see it.
