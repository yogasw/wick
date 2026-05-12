---
outline: deep
---

# Channels

A **channel** is where a message comes from. Wick agents are reachable from three channels at once:

| Channel | Connection | Session key | Source |
|---|---|---|---|
| **Slack** | Socket Mode (default) or HTTP Event API | `thread_ts` | [`channels/slack/slack.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack/slack.go) |
| **Telegram** | Long polling | `tg-<chatID>` | [`channels/telegram/telegram.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram/telegram.go) |
| **Web UI** | Direct HTTP + SSE | UUID minted by wick | [`internal/tools/agents/`](https://github.com/yogasw/wick/blob/master/internal/tools/agents) |

All three implement the same `Channel` interface ([channel.go:59](https://github.com/yogasw/wick/blob/master/internal/agents/channels/channel.go#L59)) — the pool sees them uniformly via a `SendFunc`. Wiring is handled by `*Registry` (not `server.go` directly); `channels/setup/` composers do the one-call boot assembly.

```
┌──────────┐  ┌────────────┐  ┌────────┐
│  Slack   │  │  Telegram  │  │ Web UI │
└────┬─────┘  └─────┬──────┘  └───┬────┘
     │              │             │
     └──────────────┼─────────────┘
                    ▼
              Registry.Add (auto-wires deps via setter interfaces)
                    │
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
                AgentEvent ─────► Registry.DispatchAgentEvent ─► channels
```

::: info Source
Channel interface + types: [`channels/channel.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/channel.go).
Registry + fan-out: [`channels/registry.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/registry.go).
Setup composers: [`channels/setup/setup.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/setup/setup.go).
DB-backed config store: [`channels/store.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/store.go).
Web UI handler: [`internal/tools/agents/handler.go`](https://github.com/yogasw/wick/blob/master/internal/tools/agents/handler.go).
:::

## Common shape

Every channel:

1. Listens for inbound messages.
2. Runs **access control** (channel-specific).
3. Intercepts **meta-commands** (see [below](#meta-commands)) before they reach the agent.
4. Calls `sendFn(ctx, sessionID, agentName, source, role, text)` to dispatch into the pool.
5. Receives agent events via `OnAgentEvent` to stream the reply back.
6. Receives gate approval requests via `OnApprovalRequest` for interactive Bash approval inside the channel.

The `Channel` interface itself ([channel.go:59](https://github.com/yogasw/wick/blob/master/internal/agents/channels/channel.go#L59)) requires only `Name() string`, `Start(ctx) error`, `Stop()`, `IsConfigured() bool`. Everything else is opt-in via setter and receiver interfaces that `Registry.Add` wires automatically via type assertion:

| Interface | What it gives the channel |
|---|---|
| `SendFuncSetter` | Pool dispatch closure |
| `SessionCheckerSetter` | Probe whether a session already exists (used for first-turn context injection) |
| `SessionStartHookSetter` | Callback fired once on brand-new session |
| `ApproveFnSetter` | Gate approval resolver (channel name pre-bound by registry) |
| `PublicURLSetter` | Base URL for `/dashboard` meta-command replies |
| `AgentEventReceiver` | `OnAgentEvent` — stream agent output back to the user |
| `ApprovalReceiver` | `OnApprovalRequest` / `OnApprovalResolved` — render gate modal in channel |
| `HTTPHandlerProvider` | Expose a webhook path (Slack HTTP mode) |
| `LookupProvider` | Back `picker` config fields with a live search against the upstream (Slack users, channels, …) |
| `HealthChecker` | Power the **Test Integration** button on the channel config page — return per-probe pass/fail rows |

Channels declare exactly the interfaces they need; unused ones are simply not implemented.

## Slack

> **📸 Screenshot needed:** `agents-slack-config.png` — capture `/tools/agents/channels/slack` showing the form (Mode, Bot Token, App Token, Access Mode, Workspace dropdown). Save to `docs/public/screenshots/agents-slack-config.png`.

> **📸 Screenshot needed:** `agents-slack-thread.png` — capture a Slack thread mid-conversation: user message with ⏳/⚙️/✅ reaction lifecycle visible, bot reply chunked. Save to `docs/public/screenshots/agents-slack-thread.png`.

> **📸 Screenshot needed:** `agents-slack-approval.png` — capture a Slack thread where an approval prompt was posted (Approve / Block / Always buttons). Save to `docs/public/screenshots/agents-slack-approval.png`.

### Connection modes

| Mode | When | Config |
|---|---|---|
| `socket` | **Default.** No public URL needed. Wick opens a Socket Mode connection to Slack and receives events over a websocket. | `BotToken` (`xoxb-`) + `AppToken` (`xapp-`) |
| `http` | Webhook style. Slack POSTs events to your public URL; you sign with the signing secret. | `BotToken` + `SigningSecret` + a publicly reachable wick |

Both are implemented in [`slack/slack.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack/slack.go); pick via the `Mode` config dropdown.

### Session binding

Slack threads = wick sessions. The first message in a thread auto-creates a session keyed by `thread_ts`. Replies to the same thread reuse it. New top-level message in a channel = new thread = new session.

### Reaction lifecycle

The agent's progress is mirrored on the user's message ([slack.go:34-39](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack/slack.go#L34)):

| Reaction | Stage |
|---|---|
| ⏳ `hourglass_flowing_sand` | Queued (no slot yet) — only added when the pool hasn't dispatched within 3 seconds, so fast-path turns never flash it |
| _(cleared)_ | Accepted by the pool — queue emoji removed; the assistant banner takes over |
| 🚫 `no_entry_sign` | Blocked — gate or access control rejected |
| ❌ `x` | Error — exception during the turn |

The bot uses reactions only for states the operator can't see anywhere else. Queue state lives only on the message until the pool takes it; once accepted, the queue reaction is cleared and the assistant banner (`is thinking…`) carries progress. On a successful `done` the banner is cleared too — the reply itself is the signal. Blocked / error remain as reactions so the post-mortem state is visible at a glance.

### Progress banner (assistant threads)

When the workspace has Slack AI features enabled and the bot holds the `chat:write` scope, wick also calls [`assistant.threads.setStatus`](https://api.slack.com/methods/assistant.threads.setStatus) to render an "is thinking…" banner above the input. The banner is cleared on `done` / `blocked` / `error`. Workspaces without AI features get a one-line debug log and rely on the reaction emoji alone.

### Chunked reply

Slack hard-limits messages to 4000 chars. Wick chunks at **3800** to leave 200 chars headroom for continuation markers ([slack.go:32](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack/slack.go#L32)). Each chunk is a separate threaded reply.

### Approval prompt cleanup

Gate approval prompts in Slack are interactive button messages. When the prompt is resolved — decision clicked, request expired, or revoked from elsewhere — wick **deletes** the prompt message entirely instead of leaving an "Approved" / "Blocked" residue ([slack.go `OnApprovalResolved`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack/slack.go)). The thread stays clean; the decision is observable through reaction state + downstream agent output.

### Access control

Three independent per-resource whitelists, each with its own `*_mode` dropdown (`all` / `whitelist`):

| Field pair | What it gates |
|---|---|
| `UsersMode` + `AllowedUsers` | Who (Slack user IDs) may trigger the agent |
| `GroupsMode` + `AllowedGroups` | Which user groups |
| `ChannelsMode` + `AllowedChannels` | Which channels / DMs the bot accepts messages from |

**Semantics** ([slack.go `allowedCfg`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack/slack.go)):

- If both `UsersMode` and `GroupsMode` = `whitelist` → **OR** (pass when either matches).
- If only one is `whitelist` → that list gates alone.
- If both are `all` → identity check is skipped.
- `ChannelsMode` is always **AND** on top (different dimension: scope of *where*).

The allow-list fields use the **picker** widget — searchable typeahead backed by Slack's API (see [pickers](#pickers) below) — so the operator picks chips by name instead of pasting raw IDs. The list field is hidden whenever its mode is `all` to keep the form compact.

Approval gates have their own approver block:

| `GateApprovers` | Who may resolve approval buttons |
|---|---|
| `trigger_users` _(default)_ | Anyone who passes the access whitelists. |
| `admins` | Workspace admins / owners (probed via `users.info`). |
| `custom` | Explicit `GateApproverUsers` + `GateApproverGroups` pickers. |

Unauthorized clicks get an ephemeral "Not authorized" reply and the gate stays open. Checked per-click. No restart needed — see [hot-reload](#hot-reload).

### Pickers

The `picker` widget is a generic typeahead bound to a channel-specific lookup source. Slack registers three sources:

| Source key | Backed by | Fallback |
|---|---|---|
| `slack.users` | [`assistant.search.context`](https://api.slack.com/methods/assistant.search.context) (messages → de-dupe by author) | `users.list` |
| `slack.usergroups` | `usergroups.list` | — |
| `slack.channels` | `assistant.search.context` (channels → parse permalink for ID) | `conversations.list` |

The picker stores the chips as JSON `[{id,name},...]`, identical in shape to the kvlist widget, so the same access-control parser reads either. Lookups are cached 60s per `(source, query)` to avoid hammering Slack's rate limits while the operator types.

### Integration health check

The Slack config page has a **Test Integration** button at the top. Clicking it runs the API calls the channel depends on (in parallel, ~5s budget) and reports only the ones that failed. Each failed row shows the scope hint so the operator can fix the Slack app manifest without guessing.

Probes:

- `auth.test`
- `team.info` (scope: `team:read`)
- `users.list` (scope: `users:read`)
- `usergroups.list` (scope: `usergroups:read`)
- `conversations.list` (scopes: `channels:read`, `groups:read`)
- `chat.postMessage` _(dry-run against an invalid channel ID — distinguishes `missing_scope` from `channel_not_found`)_
- `reactions.add` _(dry-run against an invalid timestamp)_
- `assistant.search.context` (scope: `assistant:write` — optional, falls back to list APIs)

When all probes pass the panel shows a single "✓ All checks passed" line.

### Hot-reload

Hot-reload runs through `Registry.WatchConfigs` (30-second poll). Each channel registers a `ConfigSource` — a `(Hash, Reload)` pair — when it is `Add`ed to the registry. For Slack the source lives in [`slack/source.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/slack/source.go); the fingerprint covers the credentials (`Mode`, `BotToken`, `AppToken`, `SigningSecret`, `pubURL`) plus every access-control field (`UsersMode`, `AllowedUsers`, `GroupsMode`, `AllowedGroups`, `ChannelsMode`, `AllowedChannels`) and the approver block (`GateApprovers`, `GateApproverUsers`, `GateApproverGroups`). When the hash changes the registry calls `Reload`, which triggers a graceful stop + restart of the Socket Mode connection. Config save → 30s tail → Slack picks up the new tokens. No server restart.

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

The token is validated at config-save time. Invalid token → channel stays in **dormant mode** (no listener, no error log spam) and re-validates on the next save ([telegram.go:99-117](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram/telegram.go#L99)).

### Session binding

One Telegram chat = one wick session, keyed `tg-<chatID>` ([telegram.go:242](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram/telegram.go#L242)). The session lives across messages in that chat.

::: warning Default workspace fallback
When the Telegram config has no `Workspace` set, it falls back to the literal `"main"` ([telegram.go:262-265](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram/telegram.go#L262)), not the built-in `default` workspace. So if you set up Telegram on a fresh install with the default workspace only, the agent will fail to spawn until you either (a) create a workspace named `main`, or (b) set `Workspace` to `default` in the channel config.
:::

### Connection: long polling

Telegram doesn't support Socket Mode like Slack. Wick uses long polling with a 60-second timeout ([telegram.go:158-175](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram/telegram.go#L158)). No public URL needed. Hot-reload works the same way as Slack — [`telegram/source.go`](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram/source.go) fingerprints `BotToken + AllowedIDs + Workspace`; `Registry.WatchConfigs` calls `Reload` on change.

### Approvals via inline keyboard

Gate approval requests appear as an inline-keyboard message in the chat. Buttons: **Approve once**, **Allow this session**, **Always**, **Block**. Telegram limits `callback_data` to 64 bytes, so wick stores the full gate fields server-side and sends only a short token in the button ([telegram.go:55-59](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram/telegram.go#L55)).

When you tap a button, the original approval message is **edited in place** to show the outcome — no spam in the chat history.

### Chunked reply

Telegram caps messages at 4096 chars. Wick buffers all output and posts the full reply chunked on `Done` ([telegram.go:64-67](https://github.com/yogasw/wick/blob/master/internal/agents/channels/telegram/telegram.go#L64)). Streaming text deltas don't post intermediate updates (Telegram has no equivalent of Slack's reaction lifecycle).

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

The recipe for a hypothetical Discord channel. `server.go` never changes after the setup hook is in place.

1. **Config struct** in `internal/agents/config/discord.go` with `wick:"..."` tags.
2. **Channel subpackage** `internal/agents/channels/discord/` — implement `Channel` + opt-in interfaces (`AgentEventReceiver`, `ApprovalReceiver`, …). Mirror `slack/` or `telegram/` for the `Reload` + `ConfigSource` pattern.
3. **DB store** in `channels/store.go`: add `LoadDiscord` to `DBStore`, extend the `TelegramConfigStore`-style interface in `channel.go`.
4. **Setup composer** in `channels/setup/setup.go`: add `Discord(reg, store, sendFn)` function + extend `All()` with one line.
5. **UI handler** in `internal/tools/agents/channels_handler.go` — form save/load.

The `Channel` interface itself doesn't change. The hard parts are the platform-specific bits: how messages stream back, how access control works, how approvals are rendered. The `Registry` wires everything else automatically.

## See also

- [Pool & Sessions](./pool) — how `SendFunc` actually does the dispatch.
- [Workspaces](./workspaces) — per-channel `Workspace` config field.
- [Command Gate](../command-gate) — the approval modal in the web UI is the same approval Slack/Telegram render.
