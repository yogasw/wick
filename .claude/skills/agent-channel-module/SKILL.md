---
name: agent-channel-module
description: Use for ANY work on an agent channel transport — creating a new channel under internal/agents/channels/<name>/ (Slack, Telegram, Discord, …), refactoring an existing one, fixing bugs in event fan-out / approval flow / hot-reload, or touching the registry, setup composer, or shared interfaces in internal/agents/channels. Covers the Channel contract — Name/Start/Stop/IsConfigured plus the opt-in setter and receiver interfaces (SendFuncSetter, SessionCheckerSetter, SessionStartHookSetter, ApproveFnSetter, PublicURLSetter, AgentEventReceiver, ApprovalReceiver, HTTPHandlerProvider) — the ConfigSource hot-reload contract, the registry-driven type-assertion wiring, and the per-transport ConfigStore abstraction that keeps gorm out of subpackages.
allowed-tools: Read, Grep, Glob, Edit, Write, Bash
paths:
  - "internal/agents/channels/**"
  - "internal/agents/pool/**"
  - "internal/pkg/api/server.go"
---

# Agent Channel Module — wick core

> **Scope:** this skill is for transports under `internal/agents/channels/` — Slack, Telegram, and any future channel (Discord, Line, MS Teams, …). Channels are wick-internal: downstream projects never write their own. If you are building a tool, job, or connector, use a different skill.

Activate this skill whenever the user touches a channel transport — creating a new one, refactoring an existing one, fixing event fan-out / approval flow / hot-reload bugs, or editing the registry, setup composer, or shared interfaces. When editing an existing channel, audit it against the rules below and bring it up to spec as part of the change.

## Mental model

A channel bridges one external chat surface (Slack thread, Telegram chat, …) to the wick agent pool. It owns:

- **Inbound**: listening for user messages, access control, meta-command intercept, dispatch to the pool via `SendFunc`.
- **Outbound**: posting agent replies (chunked, threaded, with reactions/lifecycle markers as appropriate).
- **Approval**: surfacing gate approval requests as native UI (Slack buttons, Telegram inline keyboard) and resolving the user's decision back through `ApproveFn`.
- **Hot-reload**: applying new credentials without restarting the server, via a `ConfigSource`.

The registry (`channels.Registry`) is the single seam between the server and individual channels. Server constructs each channel with its config alone, hands it to the registry, and the registry auto-wires shared dependencies through optional setter interfaces and fans events out to whichever channels implement the matching receiver.

## Applies to (non-exhaustive triggers)

- "Add a new channel for Discord / Line / MS Teams / …"
- "Add a meta-command to Slack / Telegram"
- "Fix event fan-out / approval flow / hot-reload bug in channels"
- Any edit under `internal/agents/channels/<name>/`
- Changes to `internal/agents/channels/{channel.go, registry.go, store.go}` or `internal/agents/channels/setup/`

## Before building: gather the contract

Before writing a new channel, ask the user (or skim the upstream docs) for:

> *"Before I write the channel, please share:*
> 1. *Transport — long polling, websocket, webhook (HTTP), or socket-mode? This decides whether `Start` blocks on a poll loop, whether the channel implements `HTTPHandlerProvider`, and whether `Stop` cancels via context only or needs an explicit `bot.StopReceivingUpdates()`.*
> 2. *Auth — bot token, app token, signing secret, OAuth? Each becomes a `Configs` field on the per-instance form. Hot-reload needs the watcher to detect changes; flag which fields trigger reconnect vs cosmetic changes that don't.*
> 3. *Session-key shape — what identifies a conversation? (Slack: `thread_ts`. Telegram: `tg-<chat_id>`.) Must be stable across restarts.*
> 4. *Reply model — single message, threaded replies, streaming (reactions per state), or buffered until done? Decides whether `OnAgentEvent` posts on every TextDelta or accumulates until Done.*
> 5. *Approval UI — buttons, inline keyboard, slash commands, none (web UI only)? If the transport has a payload size limit (Telegram callback_data is 64 bytes), plan a server-side token map.*
> 6. *Access control — public, allow-list of user IDs, group/role membership? Resolved on every inbound message, not cached at boot.*
> 7. *Meta-commands — does the transport support `/dashboard`, `/reset`, `/status`, `/log`, `/agent <name>`? They use the shared `agentchannels.ParseMeta`."*

Explain back the planned shape (event lifecycle, reply policy, approval UX) and wait for confirmation before generating code. Catches surprises while still cheap.

## Package layout

```
internal/agents/channels/
├── channel.go         # interfaces (Channel, optional setters/receivers, ConfigSource, *ConfigStore)
├── registry.go        # Registry: Add/Dispatch*/StartAll/StopAll/WatchConfigs
├── store.go           # DB CRUD: EnsureChannel, GetChannelConfigMap, Load*Config, DBStore
├── metacmd.go         # ParseMeta — shared by every channel
├── setup/setup.go     # Slack/Telegram composers + All() — server.go calls one line
├── slack/             # one subpackage per transport
│   ├── slack.go       # slack.Channel + slack.New(cfg)
│   ├── source.go      # slack.ConfigSource for hot-reload
│   └── slack_test.go
└── telegram/
    ├── telegram.go    # telegram.Channel + telegram.New(cfg)
    └── source.go
```

A new channel adds one subpackage (`channels/discord/`) plus one composer entry in `setup/setup.go`. The root channel.go, registry.go, and server.go never change.

## Channel contract

### Mandatory (`Channel` interface)

```go
type Channel interface {
    Name() string
    Start(ctx context.Context) error  // blocks until ctx cancelled or Stop()
    Stop()                             // signal graceful shutdown; idempotent
    IsConfigured() bool                // true when minimum required fields set
}
```

`IsConfigured` lets the registry skip an unconfigured transport at boot with an info log instead of crashing. Hot-reload can flip a dormant channel live without restart — implement it accordingly.

### Optional setter interfaces (registry auto-wires via type assertion)

| Interface | What it receives | Why |
|---|---|---|
| `SendFuncSetter` | `SendFunc(ctx, sid, agent, source, role, text) error` | Pool dispatch closure. **Required for inbound** — without it the channel cannot reach the agent. |
| `SessionCheckerSetter` | `SessionChecker.SessionExists(id) bool` | Detects first-message sessions so the channel can inject a one-time origin-context system turn. |
| `SessionStartHookSetter` | `SessionStartHook(sid, source, ctxText)` | Optional callback fired once per new session — for audit / dashboard ping. |
| `ApproveFnSetter` | `ApproveFn(sid, rid, decision, matchKey)` | Gate approval resolver. Registry auto-binds the channel name into a 4-arg closure so the manager records which transport posted. |
| `PublicURLSetter` | `string` | Public base URL for dashboard links (`/dashboard` meta-command). Slack uses it; Telegram doesn't. |

A channel implements **only** what it needs. UI/API channels skip every one of these.

### Optional receiver interfaces (registry fans out)

| Interface | When fired |
|---|---|
| `AgentEventReceiver.OnAgentEvent(sid, ev)` | Every agent event (TextDelta, Done, Error). Channel filters by sessionID — events for sessions it didn't originate are ignored. |
| `ApprovalReceiver.OnApprovalRequest(sid, req)` / `OnApprovalResolved(sid, rid, decision)` | Gate approval lifecycle. Same filtering pattern. |
| `HTTPHandlerProvider.HTTPPath() / HTTPHandler()` | Webhook channels (Slack HTTP-mode). Registry mounts on the public mux. Long-poll channels (Telegram) don't implement. |

### Hot-reload (`ConfigSource`)

```go
type ConfigSource interface {
    Hash() string                   // fingerprint of currently-applied config
    Reload(ctx context.Context) error
}
```

The registry watcher polls every source on a tick (default 30s); when `Hash()` changes, it calls `Reload(ctx)`. Each channel ships its own `<name>.ConfigSource` that owns `Hash()` semantics — fingerprint **only** the fields that materially affect connection state, so cosmetic edits don't trigger needless reconnects.

### Config storage abstraction

Sources read config through narrow interfaces declared in the root `channel.go`:

```go
type SlackConfigStore interface {
    LoadSlack() (cfg agentconfig.SlackChannelConfig, pubURL string, err error)
}
type ChannelEnsurer interface {
    EnsureChannel(channelType string) error
}
```

`channels.DBStore` implements every `*ConfigStore` + `ChannelEnsurer` and is the only place gorm is touched. A new transport adds one `<Name>ConfigStore` interface in `channel.go` plus a `LoadXxx` method on `DBStore`. Subpackage source files **never import gorm directly** — they take the store interface as a constructor argument.

This is the rule: gorm lives in `store.go`, not in slack/, telegram/, discord/.

## Step-by-step: adding a new channel

Concrete walkthrough using **Discord** as the example.

### 1. Config type

Add to `internal/agents/config/`:

```go
type DiscordChannelConfig struct {
    BotToken   string
    GuildID    string
    AllowedIDs string
    Workspace  string
}
```

### 2. Subpackage skeleton

```
internal/agents/channels/discord/
├── discord.go
└── source.go
```

**`discord.go`** — implement `Channel` + the setter/receiver interfaces the transport needs:

```go
package discord

import (
    agentchannels "github.com/yogasw/wick/internal/agents/channels"
    agentconfig "github.com/yogasw/wick/internal/agents/config"
    "github.com/yogasw/wick/internal/agents/event"
    "github.com/yogasw/wick/internal/agents/gate"
)

type Channel struct {
    cfg            agentconfig.DiscordChannelConfig
    sendFn         agentchannels.SendFunc
    sessions       agentchannels.SessionChecker
    onSessionStart agentchannels.SessionStartHook
    approveFn      agentchannels.ApproveFn
    // … bot client, turns map, etc.
}

func New(cfg agentconfig.DiscordChannelConfig) *Channel { /* … */ }

// Channel interface (mandatory):
func (d *Channel) Name() string                    { return "discord" }
func (d *Channel) Start(ctx context.Context) error { /* … */ }
func (d *Channel) Stop()                           { /* … */ }
func (d *Channel) IsConfigured() bool              { return d.cfg.BotToken != "" }

// Opt-in setters (only what Discord needs):
func (d *Channel) SetSendFunc(fn agentchannels.SendFunc)              { d.sendFn = fn }
func (d *Channel) SetSessionChecker(c agentchannels.SessionChecker)   { d.sessions = c }
func (d *Channel) SetSessionStartHook(fn agentchannels.SessionStartHook) { d.onSessionStart = fn }
func (d *Channel) SetApproveFn(fn agentchannels.ApproveFn)            { d.approveFn = fn }

// Opt-in receivers:
func (d *Channel) OnAgentEvent(sid string, ev event.AgentEvent)            { /* … */ }
func (d *Channel) OnApprovalRequest(sid string, req gate.ApprovalRequest)  { /* … */ }
func (d *Channel) OnApprovalResolved(sid, rid, decision string)            { /* … */ }

// Hot-reload entry point (called by ConfigSource.Reload):
func (d *Channel) Reload(ctx context.Context, cfg agentconfig.DiscordChannelConfig) { /* … */ }
```

Mirror Slack/Telegram for cross-cutting concerns: cfgMu around config swaps, runMu/runWg around the listen goroutine, in-memory `turns map` for per-session state, in-memory `pendingApprovals` map for in-flight approval messages.

**`source.go`** — pure glue:

```go
package discord

import (
    "context"
    agentchannels "github.com/yogasw/wick/internal/agents/channels"
    agentconfig "github.com/yogasw/wick/internal/agents/config"
)

type ConfigSource struct {
    store agentchannels.DiscordConfigStore
    ch    *Channel
}

func NewConfigSource(store agentchannels.DiscordConfigStore, ch *Channel) *ConfigSource {
    return &ConfigSource{store: store, ch: ch}
}

func (s *ConfigSource) Hash() string {
    cfg, _ := s.store.LoadDiscord()
    return cfg.BotToken + "|" + cfg.GuildID + "|" + cfg.AllowedIDs
    // include only fields that materially affect connection
}

func (s *ConfigSource) Reload(ctx context.Context) error {
    cfg, _ := s.store.LoadDiscord()
    s.ch.Reload(ctx, cfg)
    return nil
}
```

### 3. Store interface + DBStore method

**`channel.go`** — add the store interface:

```go
type DiscordConfigStore interface {
    LoadDiscord() (cfg agentconfig.DiscordChannelConfig, err error)
}
```

**`store.go`** — add the loader and the `DBStore` method:

```go
func LoadDiscordConfig(db *gorm.DB) (agentconfig.DiscordChannelConfig, error) {
    m, err := GetChannelConfigMap(db, "discord")
    if err != nil {
        return agentconfig.DiscordChannelConfig{}, err
    }
    return agentconfig.DiscordChannelConfig{
        BotToken:   m["bot_token"],
        GuildID:    m["guild_id"],
        AllowedIDs: m["allowed_ids"],
        Workspace:  m["workspace"],
    }, nil
}

func (s DBStore) LoadDiscord() (agentconfig.DiscordChannelConfig, error) {
    return LoadDiscordConfig(s.db)
}
```

### 4. Setup composer

**`setup/setup.go`** — add the composer + register in `All`:

```go
type DiscordStore interface {
    agentchannels.DiscordConfigStore
    agentchannels.ChannelEnsurer
}

// Extend the union:
type Store interface {
    SlackStore
    TelegramStore
    DiscordStore
}

func Discord(reg *agentchannels.Registry, store DiscordStore, sendFn agentchannels.SendFunc) *agentdiscord.Channel {
    if err := store.EnsureChannel("discord"); err != nil {
        log.Warn().Err(err).Msg("agents: discord channel ensure failed")
    }
    cfg, err := store.LoadDiscord()
    if err != nil {
        log.Warn().Err(err).Msg("agents: failed to load discord config")
    }
    ch := agentdiscord.New(cfg)
    ch.SetSendFunc(sendFn)
    reg.Add(ch, agentdiscord.NewConfigSource(store, ch))
    if ch.IsConfigured() {
        log.Info().Msg("agents: discord channel configured, will start with server")
    } else {
        log.Info().Msg("agents: discord channel not configured")
    }
    return ch
}

func All(reg *agentchannels.Registry, store Store, sendFn SendFnFactory) {
    Slack(reg, store, sendFn("slack"))
    Telegram(reg, store, sendFn("telegram"))
    Discord(reg, store, sendFn("discord"))  // ← new line
}
```

### 5. Server.go — DOES NOT CHANGE

The single `channelsetup.All(channelReg, agentchannels.NewDBStore(db), sendFnFor)` call already wires the new transport. This is the goal of the layout — `server.go` is a fixed point.

## Cross-cutting rules

### `context.Background()` for pool dispatch

Inbound message dispatch uses `context.Background()`, not the inbound goroutine's context:

```go
s.sendFn(context.Background(), threadTS, "main", "slack", "user", ev.Text)
```

The agent subprocess outlives the inbound goroutine — passing the request context cancels the agent the moment Slack drops the connection.

### Mutex discipline around config

Channels that hot-reload config (every transport, in practice) hold `cfgMu` only across the config snapshot read, never across an upstream API call:

```go
s.cfgMu.Lock()
api := s.api
s.cfgMu.Unlock()
// ... use local api variable, never s.api directly
```

Holding `cfgMu` across an HTTP call blocks `Reload()` for the duration of that call.

### Race on setter fields

`layout`, `onSessionStart`, `sessions` are written once at boot via setters before `Start`, then read on hot paths. We deliberately don't guard these reads — a torn read at worst causes one extra context injection or a missed hook call, neither of which is harmful. **Don't add locks** to these fields without a concrete reason.

### Session context injection

When a brand-new session arrives (no on-disk state yet), the channel sends a `role=system` turn with chat metadata **before** the user message:

```go
if !s.sessionOnDisk(threadTS) {
    if ctxText := s.buildSessionContext(ev, threadTS); ctxText != "" {
        s.sendFn(ctx, threadTS, "main", "slack", "system", ctxText)
    }
    if hook := s.onSessionStart; hook != nil {
        hook(threadTS, "slack", ctxText)
    }
}
s.sendFn(ctx, threadTS, "main", "slack", "user", ev.Text)
```

`sessionOnDisk` uses the injected `SessionChecker` (`os.Stat` on the session dir, no JSON parse). Best-effort — log warn on failure, never block the user message.

### Approval payload size

Some transports cap the callback / button payload (Telegram: 64 bytes). Don't try to embed the full `(requestID, sessionID, matchKey)` triple. Generate an 8-byte random token, store the full payload server-side under that token, embed only `gate|<decision>|<token>` in the button. Token is deleted on resolve from either the button or the web UI.

## Common anti-patterns

- ❌ Passing the inbound request `ctx` to `sendFn` — agent dies when the user disconnects.
- ❌ Holding `cfgMu` across the upstream HTTP call — blocks `Reload()`.
- ❌ Importing `gorm` in a subpackage — store loaders live in `channels/store.go` only; subpackages take the store interface.
- ❌ Reading `s.cfg` directly in a hot path — always snapshot under `cfgMu`.
- ❌ Adding a setter that the registry doesn't auto-wire — every dependency goes through the optional setter interfaces declared in `channel.go`.
- ❌ Mutating `turns` / `pendingApprovals` without `s.mu` — those are accessed from both the listen goroutine and the event/approval fan-out goroutines.
- ❌ Forgetting `bot.StopReceivingUpdates()` (Telegram) on `Stop` — leaks the long-poll connection.
- ❌ Logging the bot token / signing secret — every log line gets shipped; treat all auth fields as secrets.

## Verifying a new channel

1. **Compile:**
   ```bash
   go build ./internal/agents/channels/...
   ```
2. **Test:**
   ```bash
   go test ./internal/agents/channels/...
   ```
3. **Boot wick:**
   ```bash
   wick dev
   ```
   Confirm in logs: `agents: discord channel configured, will start with server` (or `not configured` if no token).
4. **Smoke test the channel:**
   - Add credentials in the manager UI.
   - Send a test message; confirm the agent's reply lands in the right thread/chat.
   - Trigger a destructive command to exercise the approval UI.
   - Edit a non-credential config field; confirm the watcher does **not** reconnect (cosmetic edit).
   - Edit the bot token; confirm `reload: restarting with new config` in the logs.
5. **First-message context:**
   - Delete the session dir for an existing chat.
   - Send a message; confirm the agent's first turn sees the channel/user metadata.

## Reference

- Public API: `internal/agents/channels/channel.go` — `Channel`, optional setters/receivers, `ConfigSource`, `*ConfigStore`
- Registry: `internal/agents/channels/registry.go` — `Add`, `Dispatch*`, `WatchConfigs`, `StartAll`/`StopAll`
- DB layer: `internal/agents/channels/store.go` — `EnsureChannel`, `Load*Config`, `DBStore`
- Setup composer: `internal/agents/channels/setup/setup.go`
- Reference transports: `internal/agents/channels/slack/`, `internal/agents/channels/telegram/`
- Server wiring: `internal/pkg/api/server.go` (`channelsetup.All(...)`)
