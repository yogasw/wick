# Agent bridge — cross-process comms (sockets + SSE)

How a wick agent (or an out-of-process MCP client) reaches the running
daemon and the live web UI. Read this before adding any new "an MCP
tool fires → make something happen in the UI" interaction (open a
modal, refresh a panel, open the sidebar, …) so the flow is consistent
instead of a new bespoke channel each time.

## Shipped this session (for docs + changelog sync)

Everything below landed together; each line is a discrete change with
its key files, ready to fan out into the docs site + changelog.

1. **`wick_session_workspace` MCP tool + session Config tab** — spin up
   ephemeral connector instances scoped to one session: a private clone
   of a base connector (point at staging, use a different key) that
   appears in `wick_list`/`wick_get`/`wick_execute` for that session only
   (id `sw_<uuid>`) and is purged when it ends. Actions: `list` / `add` /
   `duplicate` / `configure` / `test` / `remove`. Config is HUMAN-driven:
   the agent creates blank instances + can open the fill modal, but the
   user types values; secrets are stored as `wick_cenc_` MASTER tokens
   (system-decryptable only) and decrypted at execute time via the
   virtual `SessionInstance` path (no DB row). Eligibility = module
   `AllowSessionConfig` capability AND per-instance admin toggle (custom
   defs carry `allow_session_config`). Replaces the earlier per-key
   override model (`wick_session_config` / `config_overrides.json`).
   Files: `internal/mcp/handlers/session_workspace.go`,
   `internal/mcp/handlers/session_instances.go`,
   `internal/agents/sessionworkspace/`, `internal/connectors/service.go`
   (virtual Execute), `internal/tools/agents/session_workspace_handler.go`
   (Config tab), `internal/tools/agents/js/sessionconfig.js`,
   `internal/mcp/handlers/tools.go`.
2. **`ask_user` multi-question wizard** — `questions[]` renders a
   step-by-step form (one question per step, Back/Skip/Next, progress).
   Field types `choice` / `multi` / `rank` / `dropdown` / `text` /
   `secret` / `number`, each option with optional `description`.
   Single-select click auto-advances; Enter advances text fields;
   responsive (docks bottom on mobile, footer always visible); clear
   required-field validation. Files: `internal/agents/askuser/askuser.go`,
   `internal/mcp/handlers/ask_user.go`, `tools.go`,
   `internal/tools/agents/view/askuser.templ`,
   `internal/tools/agents/js/askuser.js`.
3. **`ask_user` works from stdio** — new `askuser.sock` so codex /
   external MCP clients reach the daemon's ask manager. Decoupled from
   the master command gate (ask_user no longer dies when the gate is
   off). Per-channel `AskUserEnabled` toggle (slack/telegram/rest), and
   gate allowlists `ask_user` + `wick_session_workspace` + `ToolSearch`.
   Files: `internal/agents/askuser/socket.go`,
   `internal/pkg/api/askuser_policy.go`, `internal/agents/config/{slack,telegram,rest}.go`,
   `cmd/gate/main.go`.
4. **Codex system prompt fix** — codex was never receiving the wick
   system prompt: the spawner used the unknown `instructions_files` key
   (silently ignored). Switched to `model_instructions_file`, and the
   `soul.md` is now written under the per-session dir (not the shared
   project workspace) so the identity block can't be clobbered across
   sessions. Files: `internal/agents/provider/codex/spawn.go`,
   `internal/agents/provider/{agent,spawner}.go`, `internal/agents/pool/factory.go`.
5. **Spawn log fidelity** — `OnSpawn` records the real argv/pid for
   respawn-per-turn providers (codex) so the spawn log shows the
   Reproduce command per turn; fixed the Windows spawn-log link
   (`filenameOf`, separator-agnostic). Files:
   `internal/agents/pool/factory.go`, `internal/agents/provider/agent.go`,
   `internal/tools/agents/view/provider_detail.templ`.
6. **Realtime session-title sync** — agentctl `refresh_session` op +
   `Broadcaster.PublishSessionMeta` SSE event. A stdio `wick_set_title`
   relays to the daemon, which reloads the session into the in-memory
   registry and pushes the new title to open tabs (sidebar + tab title
   update live, no reload). Files: `internal/agents/agentctl/`,
   `internal/tools/agents/stream.go`, `internal/pkg/api/server.go`,
   `internal/tools/agents/js/agents.js`, `view/layout.templ`.
7. **Attention notifications** — `notify.js`: a chime + browser
   Notification on `ask_user` / `approval_request` when the tab is not
   focused (self-gating; audio + permission unlocked on first gesture).
   Files: `internal/tools/agents/js/notify.js`, `agents.js`.
8. **JS split** — `ask_user` card + wizard extracted from the 2.8k-line
   `agents.js` into `askuser.js` (delegated via `window.WickAskUser`).
9. **Immutable system prompt** — consolidated the "Asking the user"
   guidance into the shared `system_prompt_immutable.md` (use `ask_user`,
   single or `questions[]`; never the native picker); per-provider
   override files kept but emptied. Files:
   `internal/agents/config/system_prompt_immutable*.md`,
   `system_prompt_default.go`.
10. **This doc** — the cross-process comm bridge reference below.

Not done (follow-ups): server-side web-push on `ask_user`/approval for
the "web UI fully closed" case (currently only the unfocused-tab path
is covered); optional merge of the three sockets into one multiplexed
channel.

## Add a new "MCP → UI" interaction (recipe)

To make a tool call trigger a UI action in the user's browser:

1. **Decide the shape** (see "Choosing a channel" below): blocking
   question → askuser socket; fire-and-forget signal → agentctl op;
   pure server→browser push → just an SSE event.
2. **Daemon → browser:** add a `Publish<Thing>` method on the
   `Broadcaster` (`internal/tools/agents/stream.go`) that fans out an
   `Event{Type: "<thing>", Data: <json>}` to the session's subscribers.
3. **Browser handler:** handle `ev.type === "<thing>"` in the SSE
   dispatch in `internal/tools/agents/js/agents.js` (or a sibling JS
   module it delegates to, like `askuser.js`).
4. **If the trigger is out-of-process** (stdio MCP, hook binary): add an
   op to the agentctl socket (step below) so the sibling process can
   ask the daemon to do steps 2–3. In-process callers (the HTTP MCP
   server) just call the daemon code directly.
5. **Notify when unfocused:** call `window.WickNotify.alert(title, body)`
   in the browser handler if the interaction needs the user's attention
   (it self-gates to unfocused tabs).

Adding an agentctl op: add a constant + a `case` in
`internal/agents/agentctl/server.go::handle`, a typed helper in
`agentctl.go` (mirror `SignalRefresh`), and wire the handler closure in
`internal/pkg/api/server.go` where `agentctlSrv` is constructed.

## The transports

wick runs as ONE daemon (the `wick server` process) that owns the agent
pool, the in-memory session registry, and the SSE broadcaster. Agents
and tools reach it through one of these:

| Transport | Who | Shape | Notes |
|---|---|---|---|
| **HTTP MCP** `/mcp` (in-process) | claude (wick-injected `--mcp-config` → loopback) | request/response | Same process as the daemon — handlers call registry/broadcaster directly. |
| **stdio MCP** (`wick mcp serve`) | codex + external clients (Claude Desktop/Code/Cursor) | request/response | A SEPARATE process. Cannot touch the daemon's in-memory state — must use a socket below. |
| **gate.sock** | `wick-gate` hook binary (PreToolUse) | request → blocks → reply | Command permission. `internal/agents/gate`. |
| **askuser.sock** | stdio MCP (`ask_user`, `wick_session_workspace` configure/add) | request → blocks → reply | Renders a card/modal in the UI; blocks the tool until the user answers. `internal/agents/askuser/socket.go`. |
| **agentctl.sock** | stdio MCP | command (`switch_provider`, `kill`, `refresh_session`) | Fire-and-forget control ops. `internal/agents/agentctl`. |
| **SSE** `/stream?session=<id>` | daemon → browser | server push | The only daemon→browser channel. Events: `lifecycle`, `ask_user`, `approval_request`, `session_meta`, … `internal/tools/agents/stream.go`. |

All sockets live in `~/.<app>/agents/*.sock`; the security boundary is
the `0700` parent dir, not the socket file (no HTTP auth on them).

## The canonical flow (out-of-process tool → UI)

```
stdio MCP tool (separate process)
  └─ dial <socket> (askuser / agentctl)  ──►  daemon socket listener
                                                └─ mutate registry / pool
                                                └─ Broadcaster.Publish<X>  ──SSE──►  browser
                                                                                      └─ agents.js handler
                                                                                          → render modal / refresh panel / …
                                                                                      └─ WickNotify.alert (if unfocused)
```

In-process (claude/HTTP MCP) skips the socket — the handler calls the
same daemon code directly. That asymmetry is by design: a socket only
exists because the caller is a different process. The MCP tool handler
itself stays identical (e.g. `WickSetTitle` calls a `refreshSession`
hook; the hook is the in-process sync in the daemon and the agentctl
signal in stdio).

## Choosing a channel

- **Need an answer back, blocking the tool** (ask the user, confirm) →
  **askuser.sock** (request/reply, blocks until the UI posts an answer
  or it times out).
- **Tell the daemon to do something, no answer needed** (refresh a
  session, switch provider, kill) → **agentctl.sock** op.
- **Just push state to open browsers** (title changed, status changed,
  new panel data) → **SSE event** only (no socket if the trigger is
  already in the daemon).
- **Permission gate on a command** → **gate.sock** (its own hook
  binary; don't reuse it for non-command interactions).

## Why three sockets, not one

They differ in lifecycle: gate blocks a hook subprocess and maps the
reply to an exit code; askuser is a long-blocking request/reply tied to
a UI answer; agentctl is short fire-and-forget control. Merging them
into one multiplexed socket is possible but would re-plumb three
working paths for little gain — keep them separate, and reach for
agentctl when adding a new fire-and-forget "MCP → UI" op.
