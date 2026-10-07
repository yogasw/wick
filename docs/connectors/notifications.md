---
outline: deep
---

# Notifications

`notifications` exposes wick's **in-process browser push service** as a fixed connector. Any logged-in user can subscribe a browser via the Account page; this connector lets the LLM (or any other connector consumer) notify **the caller** (`send_to_me`, no addressing at all) or **somebody else** by PN ID (`send_to_push_id`, admin only).

| | |
|---|---|
| **Source** | [`internal/connectors/notifications/`](https://github.com/yogasw/wick/tree/master/internal/connectors/notifications) |
| **Key** | `notifications` |
| **Icon** | 🔔 |
| **Tier** | runtime (registered inline at boot — needs `db` + `pwa.PushService`) |
| **Fixed** | ✅ — single row, auto-seeded by `Service.Bootstrap` |
| **Default tags** | `tags.Connector`, `tags.Communication` |

::: warning Notifying somebody ELSE requires admin
`send_to_push_id` guards on `requireAdmin` (calling user must have admin role), because addressing another person means resolving a PN ID against the user table.

`send_to_me` does not, and does not need to: the only reachable recipient is the person already on the other end of the call. It reads no user table, resolves no id, and cannot be pointed at anybody else — so the admin gate would buy nothing and would only stop an agent telling its own user that a long job had finished.

The connector stays minimal in the way that matters: no user listing, no user search, no device inspection. The LLM is meant to send a notification when explicitly directed, not to discover or enumerate users.
:::

## Configs

Intentionally empty (`type Configs struct{}`). The connector talks to the in-process `pwa.PushService` — VAPID keys and per-user push subscriptions live in their own tables, not on this connector row.

## Operations

### `send_to_me` — Send Notification To Me

Notify the person this call is running for. No id, no lookup, nothing to ask them for — wick already knows who the caller is from the credential the call arrived on, and which conversation it came from.

This exists because the alternative was a detour: to have wick notify you, you first opened Account → Notifications, copied your own PN ID, and pasted it back so wick could address you with information it already had.

| Input | Required | Notes |
|---|---|---|
| `title` | optional | Notification title. Defaults to `"Wick notification"`. |
| `body` | optional | Notification body. Browsers may truncate long text. |
| `link` | optional | Where the click lands: `conversation` (default), `home`, or `custom`. |
| `url` | optional | Relative app URL, used when `link=custom`. |

`link=conversation` sends you to `/tools/agents/sessions/{id}` for the session the call came from — **the same place the composer's bell sends you** when a session goes idle, so a notification raised by an agent and one raised by wick itself land in the same page. With no calling conversation (a REST call, a scheduled run) it falls back to `url` if one was given and to `/` otherwise, rather than refusing the send over its click target.

Returns `{ ok, sent, link, url, recipient? }`. **`sent: 0` is not a failure** — it means the account has no subscribed browser or device yet, and the response says so in a `note`. A call with no user behind it (a system or scheduled run) is refused with a message pointing at `send_to_push_id`.

### `send_to_push_id` — Send Notification To PN ID

Send a browser notification to every active device subscribed under one opaque PN ID — that is, to **somebody else**. For yourself, `send_to_me` needs no id at all.

| Input | Required | Notes |
|---|---|---|
| `push_id` | ✅ | Recipient PN ID. The end user copies this from **Account → Notifications**; it starts with `pn_` and is an HMAC of the user ID (does not expose the user record). |
| `title` | optional | Notification title. Defaults to `"Wick notification"`. |
| `body` | optional | Notification body. Browsers may truncate long text. |
| `url` | optional | Relative app URL to open when the notification is clicked (e.g. `/tools/agents`). Defaults to `/`. |

Returns `{ ok: bool, sent: int }` — `sent` is the count of devices the push was delivered to (a user can subscribe multiple browsers).

::: tip Why opaque IDs
PN IDs are HMAC-keyed off the encryption secret, so a notification recipient can share their PN ID without leaking their user record or any other PII. They survive password rotation but not encryption-key rotation. Each user's PN ID is shown alongside their device list at `/profile`.
:::

## Lifecycle pushes for agent sessions

The agent platform fans out **automatic** browser notifications on `idle` transitions — see [Agents → Lifecycle notifications](/guide/agents#lifecycle-notifications). Those pushes go through `pwa.PushService.SendToUser` directly (the connector isn't in the path) so the per-session `Subscribers` opt-in works regardless of whether the `notifications` connector is enabled for the calling user.

The connector is for the **explicit** path — when an LLM, workflow node, or another connector consumer should send an ad-hoc notification.

## Service worker behaviour

`web/public/js/sw.js` (the wick service worker) handles incoming pushes in two ways:

1. **At least one wick tab is open** — every same-origin client receives a `postMessage` with the payload. The page renders a custom in-app card (icon + title + body preview + click-to-open hint) and plays a short two-tone chime via Web Audio. The OS notification surface is closed immediately to avoid duplicating the in-app card.
2. **No wick tab is open** — fall back to a real OS notification with sound and a banner. Click navigates to the notification's `url`, opening wick in the existing tab if one exists or a new tab otherwise.

`userVisibleOnly: true` (the subscription flag wick uses) requires showNotification on every push or the browser may revoke the subscription. The service worker always calls it, then closes the resulting notification when wick is open so the user never sees a duplicate.

## Where the bell sits in the UI

Five places surface the same per-session subscribe model — pick the one closest to your context, the bell flips state via the shared `setBellState` JS helpers in `web/public/js/push.js`:

| Surface | Behavior |
|---|---|
| `/tools/agents` (new session composer) | Pre-subscribe bell. Toggling on doesn't subscribe to anything yet — the form's hidden `subscribe=1` field flips, and the server calls `Manager.SubscribeUser` right after `CreateSession`. Refresh clears the toggle. |
| `/tools/agents/sessions/{id}` (session composer) | Live per-session toggle. Click POSTs `/sessions/{id}/subscribe` or `/unsubscribe`. Green dot = on. |
| `/tools/agents/overview` (queue panel) | Per-row hover bell. Same per-session subscribe POST as the session composer bell, scoped to whichever queued session you're hovering. |
| `/profile` (Notification devices) | Master per-browser switch + per-device list + Send test + Copy PN ID. The bell affordances elsewhere all funnel back here when the master switch isn't set up yet. |
| Browser permission prompt | First click on any bell in the `setup` state fires `Notification.requestPermission()` plus a subscribe POST in one step. A reflex-Block here is permanent for the origin — once denied, the user must unblock via the browser's site settings; the bell flips to a slash icon and clicking it surfaces the unblock hint. |
