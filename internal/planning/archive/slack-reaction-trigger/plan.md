# Slack Reaction Auto-Reply Switch — Plan

When a Slack channel thread is started by `@bot`, the agent **automatically arms auto-reply** and
drops a 🤖 marker on the parent (top) message. While the 🤖 stays, every new reply in that thread
goes to the agent **without an @mention**. The user **removes 🤖 to stop** auto-reply, and can
**re-add 🤖 to resume**. The operator never has to react manually — arming is automatic; the marker
is the on/off switch. The flag is persisted in the session meta so it **survives a wick restart**.
Threads are still started by `@bot` mention only — auto-arm never creates a session on its own.

> Paired mockup: [`mockup.html`](mockup.html) — update both when either changes (user reads the
> mockup, this MD is the dev contract).

## Status: IMPLEMENTED

- [x] **Config** — `SlackChannelConfig` (`internal/agents/config/slack.go`)
  - [x] `ReactionTriggerEnabled bool` — master on/off, default off
  - [x] `ReactionChannelsMode string` — dropdown `all|whitelist`, default `whitelist`
  - [x] `ReactionChannels string` — picker `slack.channels`, `visible_when=reaction_channels_mode:whitelist`, **independent** of the access whitelist
  - [x] `const reactionTrigger = "robot_face"` in `slack.go`
  - [x] `group=` tags added across all Slack fields (Connection / Access Control / Reaction Auto-Reply / Approval Gates / Routing)
- [x] **Auto-arm** — on a brand-new channel thread (after the send creates the session) the channel sets auto-reply ON + posts the 🤖 marker on the parent, gated by `ReactionTriggerEnabled` + `reactionChannelAllowed`
- [x] **Switch state (persisted)** — `session.Meta.AutoReply` (meta.json) is the source of truth; `Pool.AutoReplyOn` / `Pool.SetAutoReply` read/write it. The channel keeps an in-memory `autoReply` map as a fast cache that falls back to the persisted flag on a miss (survives restart).
- [x] **Reaction events** — `handleReactionAdded` (manual re-arm) / `handleReactionRemoved` (stop) wired into `handleEventsAPI`
- [x] **Message hook** — channel replies bypass the DM-only gate when `autoReplyOn(sessionKey(thread))`
- [x] **Tests** — switch state, `isBotUser`, reaction guards, `reactionChannelAllowed` (`slack_test.go`)
- [x] **Slack app manifest + docs** — `reactions:read` scope + `reaction_added`/`reaction_removed`/`message.channels` events added to `slack-app-manifest.json`, config helper text, and `channels.md`
- [ ] **Smoke test** then kill port 8080
- [ ] **Known-minor**: `botUserID` may be empty for the first second after Socket Mode connect (resolved async via `refreshBotUserID`), so a bot self-reaction in that window isn't recognised by `isBotUser`. Harmless — the lifecycle ⏳ is filtered by the emoji check, and a self 🤖 re-arm is idempotent.

> Framework side-quest shipped alongside this: `group=Title|Description` config tag — see
> "Config grouping" below. Done in Go reflector + templ + Svelte SPA + tests.

## Decisions (locked)

| Question | Answer |
|---|---|
| Arming | **Automatic** on a new `@bot` channel thread — the bot posts the 🤖 marker itself; the user never reacts to enable. The marker is the on/off switch. |
| Mechanism | **Switch on the parent**: 🤖 present = thread auto-replies to every new reply; removed = off; re-added = on. Not per-bubble. |
| Persistence | `session.Meta.AutoReply` in meta.json — **survives restart**. In-memory map is just a cache. |
| Config scope | **Separate** channel list (`ReactionChannels`), not the access whitelist. `ReactionChannelsMode` = `all` (any channel the bot is in) or `whitelist` (listed only); default `whitelist` (fail-closed), mirrors `ChannelsMode`. |
| Trigger emoji | Hardcoded `robot_face` 🤖 — configurable later if needed |
| Session scope | Channel threads only; arm happens on the new-session path. DMs are already always-on (no marker, no switch). |
| Who may re-arm | Manual re-add (`handleReactionAdded`) requires the reactor to pass access-control (`allowedCfg`) and the thread to already have a session; the bot's own reaction is skipped. |
| Item must be parent | 🤖 on a reply bubble is ignored for manual re-arm — the switch lives on the thread root only. |
| Stop semantics | **No abort.** Removing 🤖 turns off auto-reply; a turn already running finishes and answers. Only the *next* reply is dropped. |

## Why this works (existing scaffolding)

| Need | Mechanism present |
|---|---|
| Emoji add/remove events | Slack `reaction_added` / `reaction_removed` (`slackevents.Reaction{Added,Removed}Event`) |
| Reply in thread, no mention | the existing `message` handler + `handleMessage`; we just lift the DM-only gate for armed threads |
| Resolve thread root from a reacted ts | `conversations.history` (latest=ts, limit 1) → `thread_ts` or own ts |
| Per-instance session isolation | `sessionKey(threadTS)` namespacing |

No `CancelFn` / `pool.Kill` was needed — the switch model has no abort, so the trigger is the
existing message path, and reactions only flip the persisted flag.

## Event flow

### new `@bot` thread → AUTO-ARM (the primary path)

```
app_mention → handleMessage (new channel session)
  ├─ sendFn(system ctx) + sendFn(user)   ← creates the session on disk
  └─ isNewSession && channelType==channel && ReactionTriggerEnabled && reactionChannelAllowed:
        ├─ setAutoReply(sessionID, true)          ← persists Meta.AutoReply=true + caches
        └─ setReaction(robot_face, parentTS)      ← bot drops the 🤖 marker on the parent
```

### new reply while armed (auto-reply, no mention)

```
message event (channel, not a mention)
  ├─ channelType != im/mpim ?
  │     └─ autoReplyOn(sessionKey(threadKey(...))) ?   no → return (today's DM-only behaviour)
  │           (cache hit → use it; miss → read Meta.AutoReply, warm cache — survives restart)
  └─ yes → handleMessage(ctx, ev, files)   ← appends the reply as a user turn to the live session
            (handleMessage re-checks allowedCfg for the reply author)
```

### reaction_removed → switch OFF

```
reaction_removed (robot_face)
  ├─ emoji == reactionTrigger ?  no → ignore
  ├─ reactor == botUserID ?      yes → ignore
  └─ setAutoReply(sessionKey(item.ts), false)   ← clears cache + persists Meta.AutoReply=false; no abort
```

### reaction_added → manual RE-ARM (after a user removed it)

```
reaction_added (robot_face), reactor ≠ bot
  ├─ ReactionTriggerEnabled / reactionChannelAllowed gates
  ├─ reactionThreadParent(channel, ts):  parent = thread_ts=="" || thread_ts==ts  (reply bubble → ignore)
  ├─ sessionOnDisk(sessionKey(parentTS)) ?  no → ignore   (reply-only — never boots a session)
  ├─ allowedCfg(reactor) ?                  no → ignore
  └─ setAutoReply(sessionID, true)
```

`reaction_removed` carries only `item.ts`; for the parent that ts **is** the thread root, so
`sessionKey(item.ts)` matches the armed key — no history fetch needed.

## Files touched

| File | Change |
|---|---|
| `internal/agents/config/slack.go` | reaction fields (`ReactionTriggerEnabled`, `ReactionChannelsMode`, `ReactionChannels`) + `group=` tags on all fields |
| `internal/agents/channels/slack/slack.go` | `reactionTrigger` const, `autoReply` cache + persist helpers, auto-arm on new thread, `handleReactionAdded/Removed`, `isBotUser`, `reactionThreadParent`, message-path bypass |
| `internal/agents/channels/channel.go` | `SessionChecker` gains `AutoReplyOn` / `SetAutoReply` |
| `internal/agents/session/session.go` | `Meta.AutoReply bool` |
| `internal/agents/pool/pool.go` | `Pool.AutoReplyOn` / `Pool.SetAutoReply` (read/write meta.json) |
| `internal/agents/channels/slack/slack_test.go` | switch state + guard tests |
| `pkg/entity/config.go` + `config_reflect.go` | `Config.Group` + reflector reads `group=` (framework) |
| `internal/manager/view/configs.templ` + `config_helpers.go` | grouped section cards (templ) |
| `fe/manager/src/lib/components/fields/ConfigsForm.svelte` + `options.ts` + `types.ts` | grouped section cards (SPA) + tests |

## Edge cases

- **Self-react loop** — bot's own reaction ignored on both add and remove (`isBotUser`).
- **🤖 on a reply bubble, not the parent** — ignored; switch lives on the root only.
- **Thread has no session** (never @mentioned) — ignored; the switch never boots a thread.
- **Remove while a turn is running** — that turn finishes & answers; only the next reply is dropped. No abort.
- **Disabled / channel not listed / reactor not allowed** — silently ignored (a reaction is ambient).
- **API failure resolving the parent** — `reactionThreadParent` returns `isParent=false`, so we never arm the wrong thread.
- **Bot restart** — `Meta.AutoReply` is persisted in meta.json, so armed threads stay armed across restart; the in-memory map is just a cache that re-warms on the next reply.
- **Mention inside an armed thread** — a channel `@bot` arrives as BOTH `AppMentionEvent` and `message.channels`. `mentionsBot(text)` skips the `MessageEvent` copy so the mention is dispatched once (via AppMention), not twice. Only mention-free replies flow through the auto-reply gate.
- **Scope missing** — without `reactions:read` + `message.channels`, Slack never delivers the events; surface in the Status panel / setup docs.

## Config grouping (framework side-quest)

`wick:"group=Title"` or `wick:"group=Title|Description"` groups a module's simple config fields
into titled section cards instead of one flat "Configuration" block. The description is written
**once on the group** so individual fields stop repeating shared context.

- `entity.Config.Group` (raw `Title|Description`), reflected from the tag; not persisted, JSON-exposed.
- `view.parseGroup` / `view.groupSimpleRows` (Go) and `parseGroup` / `groupSimpleFields` (TS) both
  partition by title in first-seen order, first non-empty description wins, ungrouped → default
  "Configuration" card. Behaviour mirrored 1:1 across templ and the Svelte SPA.

## Out of scope (later)

- Configurable trigger emoji (string field) — hardcoded for now.
- Multiple trigger emojis / per-emoji behaviour.
- Persisting the auto-reply switch across restarts (currently in-memory; re-react to re-arm).
- Cleaning `autoReply` entries when a session dies (map is tiny; not a leak in practice).
