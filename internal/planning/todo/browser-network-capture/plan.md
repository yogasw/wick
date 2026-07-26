# Named Profiles + Network Capture → Replay

**Current scope: the `playwright_browser` connector only (profiles + capture).** HTTP replay is deferred — see [Deferred](#deferred-later-not-now). Two connected additions to the browser connector, built on infra that already mostly exists:

1. **Named persistent profiles** — a profile (login/cookies/storage) that survives across sessions and plugin restarts, picked by name. "Lock" a browser to one identity (e.g. `fb-akun-A`), log in once, reuse forever. Today profiles are anonymous + per-session + deleted on close; this makes them named + persistent.
2. **Scoped network capture** — while a profiled session is live, record the HTTP requests matching a URL pattern, save them under the profile. The captured requests are the artifact; *how* they get replayed is deferred.

Profiles are the foundation; capture rides on top. Replay (and its auth/secrets handling) is intentionally out of scope for now — capture just produces the saved requests.

## TODO

### Profiles (foundation) — DONE
- [x] Add `Profile string` to `sessionMeta`; persist on open
- [x] `session_open(profile?)` — named profile reuses `profile-<name>` dir; empty = anonymous (no break)
- [x] Stop deleting the profile dir for **named** profiles in `removeSession` — session `.json` goes, profile dir stays → login persists
- [x] `profile_list` op — scan profile dirs (persistent), cross-check `listSessions` for `live`/`session_id`
- [x] `profile_delete(name)` op — the ONLY way a named profile is removed; refuses while in use
- [x] Extend `session_list` output with a `profile` field
- [x] Profile-name validation (`[A-Za-z0-9_-]+`, rejects path separators) — `profiles.go`
- [x] Guard: reject `session_open(profile=)` when that profile already has a live session (single-owner `--user-data-dir`)
- [x] Unit tests + manifest smoke check (ops registered, `session_open` gains `profile` input)

### Network capture
- [ ] Decide capture backend: Playwright `page.on("requestfinished")` listeners vs HAR recording — see Design
- [ ] `capture_start(session_id, url_pattern)` / `capture_stop(session_id)` ops (bounded, scoped to URL pattern)
- [ ] `CapturedRequest` struct + serialization
- [ ] Save captured requests to `profiles/<name>/captured.json` (append/replace decision)
- [ ] `capture_list(profile)` op — read back saved requests without opening a browser
- [ ] Optional: "Copy as cURL" helper for a saved request (DevTools-parity)
- [ ] Docs: `docs/connectors/playwright_browser.md` — Profiles + Capture sections

### Replay — DEFERRED (see [Deferred](#deferred-later-not-now))
Not this pass. Kept in the doc so the design is on record, but no work starts here until profiles + capture land and the user picks it up.

## Problem

User (Yoga) wants to:
1. Keep a browser **locked to a named identity** — log in to a site once, have that login persist so future runs reuse it without re-authenticating. Multiple identities kept separate (`fb-akun-A` vs `fb-akun-B`).
2. While logged in, **capture the outgoing HTTP request(s)** for a specific URL pattern, save them, and **replay/retry** them later over plain HTTP — fast, no browser per call.

Retry is a first-class goal: save the request, fire it again whenever, and on failure (session expired) re-open the profiled browser and re-capture.

## What already exists (don't rebuild)

- **Persistent Chromium profile** — live sessions already launch with `--user-data-dir=<sessionDir>/profile-<id>` ([livesession.go:165-169](../../../plugins/connector/playwright_browser/livesession.go#L165-L169)). Real Chromium profile: cookies, login, localStorage, IndexedDB all persist to that dir. This IS a browser profile — it's just (a) named by random session id, and (b) deleted on session close.
- **`sessionMeta`** ([livesession.go:39](../../../plugins/connector/playwright_browser/livesession.go#L39)) already stores `UserData` (the profile dir path) — just not a friendly name.
- **`listSessions` / `session_list`** ([livesession.go:267](../../../plugins/connector/playwright_browser/livesession.go#L267), [:297](../../../plugins/connector/playwright_browser/livesession.go#L297)) already scan session files, sweep dead browsers, and describe tabs. Extend, don't replace.
- **Live browser panel** — headed manual interaction (log in by hand) already works; a profiled session slots straight into it.

The gap is small: profiles are anonymous + ephemeral. Make them named + persistent, then hang capture off them.

## Design — Named profiles

### Profile vs session

A **profile** is a persistent identity on disk (`profile-<name>`); it outlives every session. A **session** is a browser process currently running against a profile — ephemeral, may or may not exist.

```
Profile "fb-akun-A"   (on disk forever until profile_delete)
  └── live session xyz   (browser running now — ephemeral)
Profile "fb-akun-B"   (on disk, browser NOT running — login still saved)
  └── (no live session)
```

One profile ↔ at most one live session at a time (a Chromium `--user-data-dir` can't be shared by two processes). `profile_list` reports which profiles currently have a live session.

### Changes to existing code

1. **`sessionMeta` + `Profile string`** — persisted on `session_open`, surfaced in `session_list`.
2. **`session_open(profile?)`** — `profile` optional. Given → dir is `profile-<name>` (reused if it exists, so login carries over). Empty → falls back to `profile-<sessionid>` anonymous, exactly today's behavior. No breaking change.
3. **`removeSession` skips the dir for named profiles** ([livesession.go:505](../../../plugins/connector/playwright_browser/livesession.go#L505)) — currently it deletes the profile dir when a session ends/dies. For a named profile, delete only the session `.json`; keep the profile dir so login survives. Anonymous profiles keep being cleaned up as now.

### New ops

| Op | Input | What it does |
|---|---|---|
| `profile_list` | — | Every named profile on disk: `name`, `created`, `last_used`, `live` (bool), `session_id` (if live). Persistent — shows profiles whose browser is closed. |
| `profile_delete` | `name` | Removes a named profile dir. The only way a named profile is deleted. Refuses if a live session is using it (close first). |

`profile_list` output:
```json
{
  "profiles": [
    {"name": "fb-akun-A", "created": "...", "last_used": "...", "live": true,  "session_id": "xyz"},
    {"name": "fb-akun-B", "created": "...", "last_used": "...", "live": false, "session_id": ""}
  ]
}
```

`session_list` gains `profile` per session (cross-reference so the panel/UI can show which identity a live session is driving):
```json
{"session_id": "xyz", "profile": "fb-akun-A", "browser": "chromium", "created": "...", "tabs": [...]}
```

## Design — Network capture

### Backend: Playwright event listeners vs HAR

1. **`page.on("request")` / `on("requestfinished")`** — pure Go via vendored playwright-go. Gives method/url/headers/postData + response status directly, easy to filter by URL pattern live. Misses some wire-level detail (redirect chain collapses, browser-computed headers). **Recommended** — simpler, and URL-scoped filtering is natural on the event.
2. **HAR recording** (`RecordHarPath`) — full fidelity, standard format, but records everything then needs post-hoc parsing + filtering. Heavier for a URL-scoped capture.

**Recommendation: event listeners**, scoped to `url_pattern` at capture time so noise (trackers/analytics) never gets recorded.

### Ops

| Op | Input | What it does |
|---|---|---|
| `capture_start` | `session_id`, `url_pattern` | Attach request listeners filtered by `url_pattern` (substring/regex). Bounded — records until `capture_stop`. |
| `capture_stop` | `session_id` | Detach listeners; write matched requests to `profiles/<name>/captured.json`; return them. |
| `capture_list` | `profile` | Read back saved requests without opening a browser. |

### Flow

```
1. session_open(profile="fb-akun-A")   — reuses saved login; log in by hand only the first time
2. capture_start(session_id, url_pattern="/api/graphql")
3. user (or agent) triggers the action in the browser
4. capture_stop(session_id)  → []CapturedRequest, saved to profiles/fb-akun-A/captured.json
5. capture_list(profile)  → read the saved requests back later, no browser needed
```

That's where the current scope stops — the saved requests are the deliverable. Replaying them (and re-capturing on expiry) is [deferred](#deferred-later-not-now). The profile persisting is what makes a future re-capture cheap: login is already there.

### CapturedRequest shape

```go
type CapturedRequest struct {
    Method  string            `json:"method"`
    URL     string            `json:"url"`
    Headers map[string]string `json:"headers"`
    Cookies string            `json:"cookies"`  // Cookie header value, flattened
    Body    string            `json:"body,omitempty"`
    Status  int               `json:"status"`   // response status, for picking successful calls
}
```

### Save mechanics (in scope — this is the capture artifact)

- **Save** → `profiles/<name>/captured.json` under the session dir. Survives browser close; `capture_list` reads it back with no browser.
- Retry/replay of these saved requests is **deferred** — see [Deferred](#deferred-later-not-now).

## Open Questions (need user decision) — browser scope only

1. **Profile-name rules**: restrict to `[a-z0-9-_]`, reject path separators (dir-name safety). Case-sensitive?
2. **captured.json write mode**: append (keep history of every capture) vs replace (latest only)? Leaning replace + a `capture_start(append=true)` opt-in.
3. **Capture bound**: strictly `capture_start`/`capture_stop`, or also allow "snapshot last N requests since session_open"? Leaning start/stop only — simpler, explicit.

## Deferred (later, not now)

Parked at the user's call — capture produces the saved requests; everything below is a separate future pass, on record so the design isn't lost.

### HTTP replay via a `request` op on `httprest`

When replay is picked up: extend the existing built-in `httprest` connector with a `request` op rather than a new connector. The 5 existing ops (GET/POST/…) are built for "call *my* API" — path relative to a per-instance `BaseURL`, one auth header in config ([connector.go:24-29](../../../internal/connectors/httprest/connector.go#L24-L29)). Replay needs the opposite (absolute URL, arbitrary headers, UA override, cookie header — all **per-call**, not config). A new op covers that and reuses `doRequest` ([repo.go](../../../internal/connectors/httprest/repo.go)); a whole new connector would duplicate the verbs + URL building + HTTP client.

```go
type RequestInput struct {
    Method  string `wick:"required;desc=GET, POST, PUT, PATCH, DELETE"`
    URL     string `wick:"required;desc=Full absolute URL (ignores base_url)"`
    Headers string `wick:"textarea;desc=JSON object of headers, incl. User-Agent"`
    Cookies string `wick:"desc=Cookie header value, flattened"`
    Body    string `wick:"textarea;desc=Request body"`
}
```
`CapturedRequest` → `RequestInput` is 1:1, so replay would be a direct feed.

### Auth & secrets (blocks replay, not capture)

- **Self-contained auth** — replay uses the cookies/headers *inside* the captured request; the `request` op has no auth config of its own. "Which token" = the token captured, i.e. the profile that was logged in.
- **Captured cookies/tokens are credentials** — `captured.json` holds live session cookies. Must be encrypted at rest (see the encrypted-fields skill) before this is safe to ship.
- **Expiry → re-capture by profile** — when a token expires, re-capture against the same named profile (login persists), so "which identity" resolves through the profile.

### Anti-bot reality (why replay isn't always the answer)

Capture-replay is safe to **retry mechanically** — the request is stored whole and can be fired again any number of times. Whether each retry is **accepted** depends on the target:

| Target | Works? |
|---|---|
| API with cookie/token auth, no anti-bot | ✅ retry freely until session expires, then re-capture |
| Dynamic CSRF / one-time nonce endpoints | ⚠️ re-capture each time — token expires fast |
| TLS-fingerprint anti-bot (Cloudflare / Akamai / DataDome / PerimeterX) | ❌ often blocked even with valid cookies — Go's TLS/HTTP2 fingerprint ≠ Chromium's |

For anti-bot-guarded targets, **staying in the live-session browser** (real Chromium fingerprint) beats HTTP replay. Capture-replay wins on **speed/simplicity**, not stealth. Confirm target type before relying on replay for a fingerprinted site.

## Non-goals

- Not matching TLS/JA3 fingerprint between the captured browser and the Go replay client — out of scope; use live-session browser for fingerprinted targets.
- Not solving CSRF/dynamic-token endpoints that can't be statically replayed — caller detects (re-auth redirect in response) and re-captures.
- Not sharing one profile across two concurrent live sessions — a Chromium `--user-data-dir` is single-owner; `profile_list` shows the current owner.
- Not migrating existing anonymous sessions to named profiles — named profiles are opt-in via `session_open(profile=)`.
