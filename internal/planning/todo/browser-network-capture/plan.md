# Browser Network Capture → Replay

## TODO
- [ ] Decide capture trigger UX (manual "done" signal vs URL-pattern match vs timeout) — see Open Questions
- [ ] Decide scope/filter defaults (domain allowlist vs capture-all + client-side filter)
- [ ] Add `network_capture` op to `playwright_browser` connector (uses existing live-session infra)
- [ ] Add `CapturedRequest` struct + HAR parsing (or direct CDP `Network.*` listener) in repo.go
- [ ] Replay path: generic HTTP connector (or new helper) fires a `CapturedRequest` as a real request
- [ ] Retry/fallback wiring: replay fails (403/redirect-to-login) → re-invoke `network_capture` → update stored request
- [ ] Docs: `docs/connectors/playwright_browser.md` new op section
- [ ] Decide storage: where captured requests persist (per-session file? returned to caller only, no persistence?)

## Problem

User (Yoga) wants to: open a browser once (e.g. login to Facebook manually), capture the **full outgoing HTTP request(s)** the browser made — method, URL, headers, cookies, body — as a list, then replay those requests directly via plain HTTP (no browser) going forward. On failure (expired session, 403, redirect-to-login), fall back to opening the browser again to re-capture, instead of the caller having to understand cookie/token refresh mechanics itself.

This is the "DevTools Network tab → Copy as cURL / Save as HAR" workflow, done programmatically and pluggable into a connector.

## Why this approach (vs. storage_state/localStorage dump)

`playwright.BrowserContext.storageState()` only gives cookies + localStorage — good for *re-authenticating a browser*, but the user explicitly wants the **raw request** (so no need to understand what fields matter, no need to know about refresh-token mechanics). Full request capture is ground truth: whatever the browser sent is exactly what gets replayed.

## Design

### New op: `network_capture`

Runs in a **live session** (reuses `session_open`/`session_id` — see [livesession.go](../../../plugins/connector/playwright_browser/livesession.go)), so the user can log in manually in a headed session, then call this op to pull results.

Two ways to gather requests, pick one:

1. **Playwright event listeners** (`page.on("request")` / `on("requestfinished")`) — simpler, pure Go via playwright-go bindings already vendored. Gives method/url/headers/postData directly. Misses raw wire-level detail (redirects collapse, some headers computed by browser not exposed).
2. **HAR recording** (`BrowserNewContextOptions.RecordHar`) — Playwright-native, full fidelity (headers, cookies, timing, redirects chain), industry-standard format. Requires parsing the HAR JSON after context close. Playwright-go supports `RecordHarPath` on context creation.

**Recommendation: HAR.** Standard format, most complete, and other tools (curl converters, HAR viewers) already exist for it if the user wants to inspect manually.

### Flow

```
1. session_open (existing op) — headed, HAR recording enabled on the context
2. user manually interacts in the visible browser (login, click through flow)
3. network_capture(session_id, url_filter?) op:
     - stop/flush HAR recording for that context
     - parse HAR entries
     - filter by url_filter (regex/substring) if given — else return all XHR/fetch entries, skip static assets
     - return []CapturedRequest{method, url, headers, cookies, body, status}
4. caller (agent/workflow) picks the relevant CapturedRequest, replays it via
   plain http.Client / existing http connector — no browser needed
5. on replay failure (403 / redirect to login page) → caller re-runs
   session_open + manual login + network_capture, gets fresh request, retries
```

Steps 4-5 (replay + retry-fallback) are the *consumer's* responsibility — this connector's job stops at "give me the captured requests." Keeps the op composable: any workflow/agent can wire its own retry policy on top, rather than baking a specific site's auth logic into the connector.

### CapturedRequest shape

```go
type CapturedRequest struct {
    Method  string            `json:"method"`
    URL     string            `json:"url"`
    Headers map[string]string `json:"headers"`
    Cookies string            `json:"cookies"`  // Cookie header value, already flattened
    Body    string            `json:"body,omitempty"`
    Status  int               `json:"status"`   // response status, for filtering successful calls
}
```

### Config / Input additions

- `network_capture` input: `session_id` (required), `url_filter` (optional substring/regex), `include_assets` (bool, default false — skip images/css/font/js by default)
- No new instance-level Config needed — HAR recording toggles per-session-open call, not persistent config.

## Open Questions (need user decision before implementing)

1. **Capture trigger**: does `network_capture` just snapshot "everything since session_open," or do we need a start/stop pair (`network_capture_start` / `network_capture_stop`) so the user can bound exactly which interactions get captured? → Leaning start/stop pair for precision, but adds two ops instead of one.
2. **Filter defaults**: capture-all-then-filter (simple, but noisy — trackers/analytics pollute) vs. require explicit domain allowlist upfront (cleaner but more setup). 
3. **Persistence**: does the caller get requests back in the tool response only (ephemeral, agent must save them itself), or does the connector also persist to a file under the session dir for later retrieval without re-opening the browser?
4. **Replay helper**: build a matching `network_replay` op in this same connector (fires the CapturedRequest via plain HTTP, still inside this plugin), or leave replay entirely to the generic `http` connector / caller's own code? Simpler to leave it external — avoids duplicating an HTTP client connector that already exists.

## Non-goals

- Not solving CSRF/dynamic-token endpoints that can't be statically replayed — that's caller's problem to detect (e.g. check response for a re-auth redirect) and re-capture.
- Not fingerprint-matching (TLS/JA3) between the captured browser session and the replay client — flagged as a real risk in discussion but out of scope for this connector; replay happens over plain Go http.Client which won't match Chromium's TLS fingerprint exactly.
