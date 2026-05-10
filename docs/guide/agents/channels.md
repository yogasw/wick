---
outline: deep
---

# Channels

A **channel** is where a message comes from. Wick agents are reachable from three channels at once:

| Channel | Connection | Session key | Source |
|---|---|---|---|
| **Slack** | Socket Mode (default) or HTTP Event API | `thread_ts` | [`channels/slack.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack.go) |
| **Telegram** | Long polling | `tg-<chatID>` | [`channels/telegram.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram.go) |
| **Web UI** | Direct HTTP + SSE | UUID minted by wick | [`internal/tools/agents/`](https://github.com/yogasw/wick/blob/master/internal/tools/agents) |

All three implement the same `Channel` interface ([channel.go:22](https://github.com/yogasw/wick/blob/master/internal/agents/channels/channel.go#L22)) — the pool sees them uniformly via a `SendFunc`.

```
┌──────────┐  ┌────────────┐  ┌────────┐
│  Slack   │  │  Telegram  │  │ Web UI │
└────┬─────┘  └─────┬──────┘  └───┬────┘
     │              │             │
     └──────────────┼─────────────┘
                    ▼
              SendFunc (pool.Send)
                    │
                    ▼
              ┌──────────┐
              │   Pool   │ — slot allocation, queue
              └──────────┘
                    │
                    ▼
            Provider subprocess
                    │
                    ▼
                AgentEvent ─────► back to channel (reactions / chunks / SSE)
```

::: info Source
Channel interface: [`channel.go:22`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/channel.go#L22).
DB-backed config store: [`channels/store.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/store.go).
Web UI handler: [`internal/tools/agents/handler.go`](https://github.com/yogasw/wick/blob/master/internal/tools/agents/handler.go).
:::

## Common shape

Every channel:

1. Listens for inbound messages.
2. Runs **access control** (channel-specific).
3. Intercepts **meta-commands** (see [below](#meta-commands)) before they reach the agent.
4. Calls `sendFn(ctx, sessionID, agentName, source, role, text)` to dispatch into the pool.
5. Subscribes to agent events via `OnAgentEvent` to stream the reply back.
6. Subscribes to gate approval requests via `OnApprovalRequest` for interactive Bash approval inside the channel.

The `Channel` interface itself ([channel.go:22-26](https://github.com/yogasw/wick/blob/master/internal/agents/channels/channel.go#L22)) is just `Name() string`, `Start(ctx) error`, `Stop()`. The lifecycle hooks (`OnAgentEvent`, `OnApprovalRequest`, `OnApprovalResolved`) are channel-specific and wired by `server.go` at boot.

## Slack

> **📸 Screenshot needed:** `agents-slack-config.png` — capture `/tools/agents/channels/slack` showing the form (Mode, Bot Token, App Token, Access Mode, Workspace dropdown). Save to `docs/public/screenshots/agents-slack-config.png`.

> **📸 Screenshot needed:** `agents-slack-thread.png` — capture a Slack thread mid-conversation: user message with ⏳/⚙️/✅ reaction lifecycle visible, bot reply chunked. Save to `docs/public/screenshots/agents-slack-thread.png`.

> **📸 Screenshot needed:** `agents-slack-approval.png` — capture a Slack thread where an approval prompt was posted (Approve / Block / Always buttons). Save to `docs/public/screenshots/agents-slack-approval.png`.

### Connection modes

| Mode | When | Config |
|---|---|---|
| `socket` | **Default.** No public URL needed. Wick opens a Socket Mode connection to Slack and receives events over a websocket. | `BotToken` (`xoxb-`) + `AppToken` (`xapp-`) |
| `http` | Webhook style. Slack POSTs events to your public URL; you sign with the signing secret. | `BotToken` + `SigningSecret` + a publicly reachable wick |

Both are implemented in [`slack.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack.go); pick via the `Mode` config dropdown.

### Session binding

Slack threads = wick sessions. The first message in a thread auto-creates a session keyed by `thread_ts`. Replies to the same thread reuse it. New top-level message in a channel = new thread = new session.

### Reaction lifecycle

The agent's progress is mirrored on the user's message ([slack.go:34-39](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack.go#L34)):

| Reaction | Stage |
|---|---|
| ⏳ `hourglass_flowing_sand` | Queued (no slot yet, FIFO waiting) |
| ⚙️ `gear` | Running — first text delta arrived |
| ✅ `white_check_mark` | Done — final reply posted |
| 🚫 `no_entry_sign` | Blocked — gate or access control rejected |
| ❌ `x` | Error — exception during the turn |

### Chunked reply

Slack hard-limits messages to 4000 chars. Wick chunks at **3800** to leave 200 chars headroom for continuation markers ([slack.go:32](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack.go#L32)). Each chunk is a separate threaded reply.

### Access control

Three modes (config field `AccessMode`):

| Mode | Who can talk to the bot |
|---|---|
| `everyone` | Any Slack user with access to the channel. |
| `users` | Only user IDs in `AllowedUsers`. |
| `groups` | Only members of the user groups in `AllowedGroups`. |

Checked per-message. No restart needed — see [hot-reload](#hot-reload).

### Hot-reload

The Slack listener watches its config row in the `agent_channels` table every 30 seconds ([slack.go: `Reload`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack.go)). A hash diff over `AccessMode + AllowedUsers + AllowedGroups + tokens` triggers a graceful stop + restart of the Socket Mode connection. Config save → 30s tail → Slack picks up the new tokens. No server restart.

### Workspace selection

When **only one workspace exists**, Slack uses it without asking — the operator doesn't need to set `Workspace`. With multiple workspaces, the `Workspace` config field picks one.

### App manifest

A ready-made Slack app manifest is shipped at [`docs/slack-app-manifest.json`](https://github.com/yogasw/wick/blob/master/docs/slack-app-manifest.json). Drop it into the Slack app create flow and you get the right scopes (`app_mentions:read`, `chat:write`, `reactions:write`, etc.) without hand-toggling.

## Telegram

> **📸 Screenshot needed:** `agents-telegram-config.png` — capture `/tools/agents/channels/telegram` showing the form (Bot Token, Allowed IDs, Workspace). Save to `docs/public/screenshots/agents-telegram-config.png`.

> **📸 Screenshot needed:** `agents-telegram-chat.png` — capture a Telegram chat with the bot: user message → bot reply, plus an inline-keyboard approval message (Approve / Block buttons). Save to `docs/public/screenshots/agents-telegram-chat.png`.

### Setup

1. Create a bot via [@BotFather](https://t.me/BotFather) → grab the token (`123456:ABC-...`).
2. Paste the token into `/tools/agents/channels/telegram` → `BotToken`.
3. Optional: list allowed chat IDs in `AllowedIDs` (kvlist). Empty = open to all chats the bot is added to.

The token is validated at config-save time. Invalid token → channel stays in **dormant mode** (no listener, no error log spam) and re-validates on the next save ([telegram.go:99-117](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram.go#L99)).

### Session binding

One Telegram chat = one wick session, keyed `tg-<chatID>` ([telegram.go:242](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram.go#L242)). The session lives across messages in that chat.

::: warning Default workspace fallback
When the Telegram config has no `Workspace` set, it falls back to the literal `"main"` ([telegram.go:262-265](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram.go#L262)), not the built-in `default` workspace. So if you set up Telegram on a fresh install with the default workspace only, the agent will fail to spawn until you either (a) create a workspace named `main`, or (b) set `Workspace` to `default` in the channel config.
:::

### Connection: long polling

Telegram doesn't support Socket Mode like Slack. Wick uses long polling with a 60-second timeout ([telegram.go:158-175](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram.go#L158)). No public URL needed.

### Approvals via inline keyboard

Gate approval requests appear as an inline-keyboard message in the chat. Buttons: **Approve once**, **Allow this session**, **Always**, **Block**. Telegram limits `callback_data` to 64 bytes, so wick stores the full gate fields server-side and sends only a short token in the button ([telegram.go:55-59](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram.go#L55)).

When you tap a button, the original approval message is **edited in place** to show the outcome — no spam in the chat history.

### Chunked reply

Telegram caps messages at 4096 chars. Wick buffers all output and posts the full reply chunked on `Done` ([telegram.go:64-67](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram.go#L64)). Streaming text deltas don't post intermediate updates (Telegram has no equivalent of Slack's reaction lifecycle).

## Web UI

> **📸 Screenshot needed:** `agents-web-session.png` — capture `/tools/agents/sessions/<id>` with the conversation visible, composer at the bottom, and the running-agent indicator. Save to `docs/public/screenshots/agents-web-session.png`.

> **📸 Screenshot needed:** `agents-web-approval.png` — capture a session detail with the gate approval modal open (4 buttons + countdown timer + cmd shown). Save to `docs/public/screenshots/agents-web-approval.png`.

> **📸 Screenshot needed:** `agents-web-askuser.png` — capture a session with the AskUser inline card visible (question + option buttons + optional freeform input). Save to `docs/public/screenshots/agents-web-askuser.png`.

The web UI is the always-on third channel. No config — it's just `/tools/agents` plus per-session pages.

| Concern | How |
|---|---|
| **Session create** | First POST to `/tools/agents/sessions/{id}/send` with a fresh UUID auto-creates the session. The UI mints the UUID. |
| **Streaming** | SSE at `GET /tools/agents/stream` broadcasts `agent_event`, `approval_request`, `approval_resolved`, `ask_user`, `ask_user_resolved`. The page subscribes via `EventSource`. |
| **Approval modal** | When `approval_request` fires for the visible session, JS opens a modal with 4 buttons + 25s countdown. Click → `POST /sessions/{id}/approve` with `{id, decision}`. |
| **AskUser card** | When `ask_user` fires, JS renders an inline card in the composer area with the question, option buttons, and (if `allow_freeform=true`) a text input. Submit → `POST /sessions/{id}/answer`. |
| **Approved-commands panel** | Lists every `approve_always` rule for the current session, with a Revoke button. |

### AskUser MCP tool

This is the agent-initiated counterpart to the gate. The agent calls the `ask_user` MCP tool ([askuser.go:38-45](https://github.com/yogasw/wick/blob/master/internal/agents/askuser/askuser.go#L38)):

```json
{
  "session_id": "9b7e-...",
  "question": "Which environment?",
  "options": [
    {"label": "Production", "value": "prod"},
    {"label": "Staging", "value": "stg"}
  ],
  "allow_freeform": false
}
```

The handler registers a pending question, broadcasts SSE, blocks the MCP call until the user answers (default 5min timeout per [askuser.go:27](https://github.com/yogasw/wick/blob/master/internal/agents/askuser/askuser.go#L27)), then returns the answer to the agent. Unlike the gate, this is **voluntary** — the agent decides when to ask, and a forgetful agent can skip it.

The reason wick ships its own AskUser MCP tool instead of relying on Claude Code's `AskUserQuestion` harness tool: the harness tool isn't available when Claude runs in pipe mode (`-p`), only inside the Claude Code TUI. An MCP tool works in every mode.

## Meta-commands

Channels intercept these before they reach the agent ([metacmd.go:31-66](https://github.com/yogasw/wick/blob/master/internal/agents/channels/metacmd.go#L31)). All are case-insensitive and accept `/` or `!` prefix.

| Command | Action |
|---|---|
| `/agent <name>` | Switch the active named agent in this session. |
| `/reset` | Clear the session context. The next message starts a fresh subprocess (no `--resume`). |
| `/status` | Reply with the current session + agent state. |
| `/dashboard` _(or `/link`)_ | Reply with the dashboard URL for this session — built from `PublicURL` config + session ID. |
| `/log [N]` | Reply with the last N command-gate log lines. |

Meta-commands aren't forwarded to the agent subprocess. They run inside wick.

::: info Why the `!` prefix
Some Slack workspaces strip leading `/` characters from messages routed through certain integrations. The `!` prefix is a fallback that survives that path.
:::

## Channel config in DB

Channel configs live in `agent_channels` ([store.go](https://github.com/yogasw/wick/blob/master/internal/agents/channels/store.go)), one row per channel type:

| Column | Holds |
|---|---|
| `type` | `slack` / `telegram` |
| `name` | Display name (currently always `default`) |
| `enabled` | Mirrors whether `bot_token` is non-empty |
| `config` | JSON map: per-field settings (one per `wick:"key=..."` field) |

`config` is a flat JSON map, not a typed struct on disk. The typed struct is rebuilt at load time ([store.go:112-141](https://github.com/yogasw/wick/blob/master/internal/agents/channels/store.go#L112)). Reasoning: keeps channel-specific schema migrations cheap — add a new field to `SlackChannelConfig`, add the form field, no DB migration.

## Adding a new channel

The recipe for a hypothetical Discord channel:

1. **Config struct** in `internal/agents/config/discord.go` with `wick:"..."` tags.
2. **Channel implementation** in `internal/agents/channels/discord.go` — implement `Channel`, mirror Slack/Telegram for `OnAgentEvent` / `OnApprovalRequest` plumbing.
3. **DB load** helper in `channels/store.go`: `LoadDiscordConfig(db)`.
4. **Wire-up** in `server.go`: `EnsureChannel(db, "discord")` at boot, construct + `Start` if configured.
5. **UI handler** in `internal/tools/agents/channels_handler.go` — form save/load.

The `Channel` interface itself doesn't change. The hard parts are the platform-specific bits: how messages stream back, how access control works, how approvals are rendered.

## See also

- [Pool & Sessions](./pool) — how `SendFunc` actually does the dispatch.
- [Workspaces](./workspaces) — per-channel `Workspace` config field.
- [Command Gate](../command-gate) — the approval modal in the web UI is the same approval Slack/Telegram render.
