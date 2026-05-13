---
outline: deep
---

# Pool & Sessions

The **pool** caps how many agent subprocesses run at once across all sessions, FIFO-queues the rest, kills idle ones, and revives them with `--resume` when new messages arrive.

A **session** is what holds the conversation: routing key, agent registry, log files, optional workspace binding.

::: info Source
Pool: [`internal/agents/pool/`](https://github.com/yogasw/wick/blob/master/internal/agents/pool) — [`pool.go`](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go) (slot allocation), [`buffer.go`](https://github.com/yogasw/wick/blob/master/internal/agents/pool/buffer.go) (message buffer), [`factory.go`](https://github.com/yogasw/wick/blob/master/internal/agents/pool/factory.go) (build agents).
Session: [`internal/agents/session/`](https://github.com/yogasw/wick/blob/master/internal/agents/session) — [`session.go`](https://github.com/yogasw/wick/blob/master/internal/agents/session/session.go) (Meta, Create/Load/SwitchWorkspace), [`agents.go`](https://github.com/yogasw/wick/blob/master/internal/agents/session/agents.go) (per-session AgentEntry).
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
| `DefaultWorkspace` | _(empty)_ | Fallback workspace name when session has none. Empty = per-session temp dir. | [pool.go:59](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go#L59) |

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
    Workspace    string    // workspace name; "" = use DefaultWorkspace
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
    Provider      string    // provider type ("claude" / "codex" / "gemini")
    CLISessionID  string    // <-- key to resume; written when CLI emits SessionStart
    Status        Status
    CreatedAt     time.Time
    LastActive    time.Time
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
   - load session.Meta → resolve workspace cwd (workspace.ResolvePath, fallback chain)
   - look up CLISessionID for resume
   - factory.Build(FactoryOptions) → returns Agent + State + Store + OnStarted hook
   - drain Buffer into one combined input
   - markStatus(running)
   - a.Start(ctx) → fires CLI subprocess
   - OnStarted(pid, binary, argv, firstUserMessage) → completes spawn-log start event
   - if drained text non-empty → a.Send(combined)
     (user turns were already persisted in step 3 — no double-write)
```

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

The format of the resume ID is CLI-specific:

| CLI | Where it comes from | How to pass it |
|---|---|---|
| Claude | `system.subtype=init` event | `claude --resume <id>` |
| Codex | `thread.started` event | `codex --resume <id>` (when phase 6 lands) |
| Gemini | `init` event | env `GEMINI_SESSION_ID` (when phase 6 lands) |

Today, only Claude is wired end-to-end. Codex / Gemini parsers are stubs in [`internal/agents/event/`](https://github.com/yogasw/wick/blob/master/internal/agents/event); resume flow ships when those parsers land.

## Workspace cwd resolution

[`pool.resolveCwd`](https://github.com/yogasw/wick/blob/master/internal/agents/pool/pool.go) at spawn time:

1. `sess.Meta.Workspace` non-empty → `workspace.ResolvePath(layout, name)`. Returns custom path or `<base>/workspaces/<name>/files/`.
2. Empty → `cfg.DefaultWorkspace` set? → resolve that.
3. Both empty → per-session temp dir at `sessions/<id>/cwd/`. Created on demand.

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
3. Workspace files left alone (workspaces are shared)
```

## Telemetry hooks

Pool fires two callbacks the UI subscribes to:

| Hook | Fires when | Use |
|---|---|---|
| `OnSessionCreated(sess)` | Pool auto-creates a session for an inbound channel message | Register session into `manager.Manager` so the dashboard sees it without reload. |
| `OnLifecycle(LifecycleEvent)` | `spawning` (post-Start) and `killed` transitions | UI badges, spawn-log enrichment. |

`Idle` / `Working` transitions are NOT routed via `OnLifecycle` — they're implicit from the event flow. UIs that want every transition subscribe to `AgentEvent` via the factory's `OnEvent`.

## See also

- [Channels](./channels) — where `SendFunc` is called from.
- [Workspaces](./workspaces) — `cwd` resolution.
- [Providers](./providers) — `FactoryOptions.ProviderType` / `ProviderName` forwarding.
- [Command Gate](../command-gate) — gate's PreToolUse hook fires inside the spawned subprocess; pool doesn't see it.
