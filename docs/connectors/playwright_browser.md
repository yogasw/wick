---
outline: deep
---

# Playwright Browser

`playwright_browser` drives a real browser (Chromium / Firefox / WebKit, plus [CloakBrowser free and Pro](#cloakbrowser)) to screenshot, scrape, render PDF, evaluate JS, and run scripted interaction flows. Each op launches an isolated browser inside the plugin's own process — no shared state, safe to run concurrently — except in [live-session mode](#live-session), where a browser is kept open across calls.

| | |
|---|---|
| **Source** | [`plugins/connector/playwright_browser/`](https://github.com/yogasw/wick/tree/master/plugins/connector/playwright_browser) |
| **Key** | `playwright_browser` |
| **Icon** | 🎭 |
| **Tier** | plugin — install with `<app> plugin install playwright_browser` |

> Install as a plugin:
>
> ```bash
> <app> plugin install playwright_browser
> ```
>
> See [Connector Plugins](/guide/connector-plugins) for the full install flow.

::: warning Runtime dependency
Playwright ships a Node-based driver and downloads browser binaries on first use. The connector installs these lazily, so the **first** call to a task op (or a `browser_install` from the picker) may take a while and needs outbound network access. Use the [Maintenance](#maintenance) ops to pre-install a browser instead of waiting on the first real call.

If the default download CDN is unreachable, the connector automatically retries once against `cdn.playwright.dev` before failing. A failed install is **not** cached — the next call retries from scratch, so a transient network outage doesn't require a plugin restart. Set `PLAYWRIGHT_DOWNLOAD_HOST` yourself to pin a specific mirror; when set, the automatic fallback is skipped.
:::

## Configs

Config fields are grouped into cards on the instance's Settings page. **Browser** is open by default; the rest start **collapsed** since most setups never touch them.

| Group | Field | Notes |
|---|---|---|
| Browser | `Browser` | Engine to launch — rendered as a picker widget (see below) showing install status per engine; click an installed row to select it, or **Download** a missing one right there. Default `chromium`. |
| Browser | `Headless` | Run without a visible window. Default on; turn off only for local debugging. |
| Display *(collapsed)* | `ViewportWidth` / `ViewportHeight` | Viewport size in pixels. Ignored when `Device` is set. Defaults `1280x800`. |
| Display | `UserAgent` | Override the `User-Agent` header. |
| Display | `Device` | Emulate a named device (e.g. `iPhone 15`, `Pixel 7`) — sets viewport, UA, and touch; overrides the viewport fields. |
| Network *(collapsed)* | `ProxyServer` | Route browser traffic through a proxy, e.g. `http://proxy.abc.com:3128` or `socks5://proxy.abc.com:1080`. |
| Network | `ProxyBypass` | Comma-separated domains to bypass the proxy. |
| Timeouts & limits *(collapsed)* | `ActionTimeoutMs` | Per-action timeout (click, fill, wait_for). Default `5000`. |
| Timeouts & limits | `NavigationTimeoutMs` | Page navigation timeout (`goto`). Default `30000`. |
| Timeouts & limits | `MaxTab` | Max pages (tabs) a single `run` may open. Default `5`. |
| Live sessions *(collapsed)* | `SessionDir` | Where live-session metadata, browser profiles, and downloaded engines (e.g. CloakBrowser) are stored. Default: the plugin's persistent data dir under the app tree (`~/.<app>/plugins/playwright_browser`) — set this only to override that location. |
| Live sessions | `MaxLiveSessions` | Max persistent browsers alive at once. Default `1`, `0` = unlimited. |
| Live sessions | `MaxTabsPerSession` | Max tabs within one live session (`tab_new` cap). Each open tab keeps a live page in RAM, so multi-tab is opt-in — default `1`, `0` = unlimited. |
| Live sessions | `BrowserIdleTimeoutMin` | Minutes a live session may sit with no activity before it is closed automatically. Any op touching the session resets the clock. Named-profile sessions get 8x this value. Default `1`, `-1` = never auto-close. See [Idle auto-close](#idle-auto-close). |
| Live sessions | `BrowserKillOrphans` | Also terminate browser processes under the session directory that no live session claims — orphaned children or re-forked PIDs the normal cleanup misses. Recommended on servers. Default off. See [Orphan sweeping](#orphan-sweeping). |
| Live sessions | `DefaultProfile` | Named profile used when `session_open` is called without a `profile` argument. Set this so logins persist by default instead of being swept on close. Empty = anonymous throwaway sessions (the original default). See [Named profiles](#named-profiles). |
| Live sessions | `ForceDefaultProfile` | Always use `DefaultProfile`, ignoring any `profile` argument. Guarantees every session shares one identity. Requires `DefaultProfile` to be set. |
| Custom binary *(collapsed)* | `ExecutablePath` | Path to a custom browser binary instead of the bundled one. |
| Custom binary | `Channel` | Branded channel (`chrome`, `chrome-beta`, `msedge`, …) for the chosen browser. |
| CloakBrowser *(collapsed)* | `CloakRepo` | GitHub `owner/repo` hosting CloakBrowser release assets (free tier). Default `CloakHQ/CloakBrowser`. |
| CloakBrowser | `CloakExecutablePath` | Path to an already-downloaded CloakBrowser binary. Set this to skip the GitHub download (free tier) or the CLI resolution (pro tier). Applies to both cloak engines. |
| CloakBrowser | `CloakLicenseKey` | CloakBrowser Pro license key (`cb_...`). Required by the `cloakbrowser-pro` engine; leave empty for the free `cloakbrowser` engine. Passed to the browser at launch so it runs at its licensed tier. |
| CloakBrowser | `CloakCLIPath` | Path to the `cloakbrowser` CLI used by the `cloakbrowser-pro` engine. Default: resolved on `PATH`. Only needed for a non-`PATH` install. |

**The browser picker** is a [`html=` widget](/reference/config-tags#html-—-server-rendered-widget) — the connector renders its own status card per engine (installed / not installed, version, a Download button) and the widget wires clicks back to `browser_status` / `browser_install`. This is the reference implementation for `html=` in the docs.

## Operations

Four op groups.

### Page tasks

Ephemeral: open a URL, do one thing, close the browser.

| Op | Input | What it does |
|---|---|---|
| `screenshot` | `url`, `full_page`, `selector`, `wait_for` | PNG screenshot as base64. `full_page` captures the whole scrollable page; `selector` scopes to one element. |
| `get_content` | `url`, `selector`, `as_text` (default true), `wait_for` | Rendered content after JS runs — visible text by default, or HTML when `as_text` is false. |
| `pdf` | `url`, `wait_for` | Page rendered to PDF as base64. **Chromium only** — errors on firefox/webkit/cloakbrowser/cloakbrowser-pro instances. |
| `scrape` | `url`, `fields` (JSON map of key → CSS selector), `wait_for` | Structured extraction — each selector's inner text is returned under its key; a selector matching nothing returns `""`. |
| `eval` | `url`, `script` | Evaluates a JS expression in the page, returns the JSON-serialized result. Marked **destructive** — arbitrary JavaScript can submit forms and change remote state. |

### Scripted flow

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `run` | yes | `actions` (JSON array), `session_id` (optional), `tab` (optional), `record_request` (optional) | Runs an ordered list of browser actions in one session and returns a result per step; stops at the first failure. Pass `session_id` to run against a persistent [live session](#live-session) instead of a throwaway browser, and `tab` (0-based, from `session_list`) to target a specific tab in a multi-tab session — ignored without `session_id`, defaults to the first tab. Set `record_request` to capture the HTTP calls the script triggers — see [Recording network requests](#recording-network-requests). |

`run` supports 32 actions in one script:

- **Navigation** — `goto`, `go_back`, `go_forward`, `reload`, `wait_for_load_state`, `wait_for_url`
- **Interaction** — `click`, `dblclick`, `hover`, `tap`, `focus`, `fill`, `type`, `press`, `check`, `uncheck`, `select_option`, `set_input_files`, `drag_and_drop`, `scroll`
- **Wait** — `wait_for`, `wait`
- **Read** — `screenshot`, `content`, `eval`, `get_attribute`, `text_content`, `inner_html`, `is_visible`, `is_checked`, `count`, `title`, `url`

```json
[
  {"action": "goto", "url": "https://abc.com"},
  {"action": "fill", "selector": "#q", "value": "hi"},
  {"action": "click", "selector": "button[type=submit]"},
  {"action": "wait_for", "selector": ".result"},
  {"action": "screenshot", "full_page": true}
]
```

`run` is marked **destructive** because a script can submit forms and change remote state — off by default, opt in per row.

### Live session

Persistent browsers that survive across calls — and plugin restarts — until closed. The browser runs as a **detached OS process** reached over CDP, so it outlives the idle-swept plugin subprocess. It ends either explicitly via `session_close` or automatically once it goes unused (see [Idle auto-close](#idle-auto-close)).

> **Chromium-based engines only.** Live sessions require the Chromium DevTools protocol, which only `chromium` and the two cloak engines (`cloakbrowser` / `cloakbrowser-pro`, patched Chromium) expose. `session_open` errors on a `firefox` / `webkit` instance — use the ephemeral ops (`run`, `screenshot`, …) for those, or set `Browser=chromium`.

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `session_open` | yes | `profile` (optional) | Launches a persistent browser, returns its `session_id`. Respects `MaxLiveSessions` (and the CloakBrowser free-tier 1-session lock — see [CloakBrowser](#cloakbrowser)). Pass `profile` to run against a [named profile](#named-profiles) instead of a throwaway anonymous session; with `DefaultProfile` configured, omitting it uses that profile. |
| `session_list` | no | — | Lists every live session and its open tabs (index, url, title), plus `max_tabs` (the effective `MaxTabsPerSession` cap, `0` = unlimited) so callers can tell when a session is full. Each row also carries `idle_seconds` and `auto_close_in_seconds` — call this before `session_open` to reuse an existing session, or to choose between closing one now and waiting for it to expire. Dead sessions are swept automatically. |
| `tab_new` | no | `session_id`, `url` | Opens a new tab in a live session, optionally navigating it. Rejected once the session hits `MaxTabsPerSession` — close a tab or raise the cap first. |
| `tab_close` | yes | `session_id`, `index` | Closes the tab at `index` (from `session_list`). |
| `session_close` | yes | `session_id` | Kills the session's browser and frees its resources. **Always close sessions you opened** — the idle timeout is a safety net, not a substitute, and an abandoned session keeps holding a slot (and its RAM) until that timeout elapses. |
| `session_endpoints` | no | `session_id` | Returns the session's raw CDP details: `cdp_url` plus one entry per tab with `target_id` + `ws_debugger_url`. Read-only; backs the live-browser panel (below). **Manager-only** — hidden from the MCP tool surface (see [Manager-only operations](#manager-only-operations)). |
| `profile_list` | no | — | Lists named persistent profiles: name, created/last-used time, and whether a live session is currently using it. See [Named profiles](#named-profiles). |
| `profile_delete` | yes | `name` | Deletes a named profile and its stored login/cookies for good. Refused while a live session is using it — close that session first. |
| `get_request` | no | `profile` or `name` | Reads back HTTP requests recorded by an earlier `run` with `record_request=true`. See [Recording network requests](#recording-network-requests). |

Pass a live session's `session_id` to `run` (or any task op) to reuse the same open browser instead of launching a throwaway one.

### Named profiles

A live session already launches Chromium with its own `--user-data-dir`, but by default that dir is named after the random session id and removed once the session closes — so a login never survives past `session_close`.

Passing `profile` to `session_open` gives the session a **stable, named** profile dir instead:

```json
{"action": "session_open", "profile": "acme-account-a"}
```

- Login, cookies, and localStorage under that profile persist across sessions **and plugin restarts** — closing the session and reopening it with the same `profile` reuses the same login, no re-auth.
- A profile name is letters/digits/dash/underscore only (no path separators).
- A named profile is single-owner: opening it while another live session already holds it is rejected — close that session first.
- `profile_list` shows every named profile on disk (even with no browser currently running against it) and which session, if any, currently owns it.
- `profile_delete` is the only way to remove a named profile; it refuses while a session is using it.

Anonymous sessions (no `profile` passed) keep the original behavior: a per-session dir swept on `session_close`.

#### Making a profile the default

Having to pass `profile` on every call is easy to forget, and forgetting it means the session is anonymous and the login is gone at `session_close`. Two instance configs move that default:

- **`DefaultProfile`** — the profile used when `session_open` is called with no `profile` argument. An explicit argument still wins, so callers can opt into a different identity.
- **`ForceDefaultProfile`** — pins *every* session to `DefaultProfile`; a `profile` argument is ignored rather than rejected. Use this when all sessions on the instance must share one identity.

With `DefaultProfile = "acme-account-a"`, a bare `session_open` behaves as if `profile` had been passed:

```json
{"action": "session_open"}
```

Turning on `ForceDefaultProfile` without a `DefaultProfile` set fails at `session_open` with a clear error rather than silently falling back to anonymous.

::: warning Login still needs a clean shutdown
A named profile keeps its dir, but Chromium only flushes cookies and session storage to disk on a graceful exit. `session_close` kills the browser by PID, so a login completed moments earlier can still be lost even with a profile set. Give the browser a little time after logging in before closing the session.
:::

### Idle auto-close

A live session is a detached browser, so nothing ends it when the caller goes away — an agent that crashes, times out, or simply forgets `session_close` would otherwise leave a browser resident indefinitely, holding both a session slot and its memory.

`BrowserIdleTimeoutMin` closes sessions nobody is using. Every operation that touches a session (`run`, `screenshot`, `tab_new`, `session_list`, …) resets its clock; once a session sits untouched past the timeout it is terminated and its metadata swept.

| Session kind | Default idle window |
|---|---|
| Anonymous (no `profile`) | 1 minute |
| Named profile | 8 minutes (8x the configured value) |

Named profiles get the longer leash because closing one ends a logged-in browser. Set `BrowserIdleTimeoutMin` to `-1` to disable auto-close entirely and restore the old "lives until `session_close`" behavior.

`session_list` reports where each session stands:

```json
{"session_id": "pw-123-456", "idle_seconds": 22, "auto_close_in_seconds": 38}
```

When the session cap blocks a `session_open`, the error names the sessions holding the slots and how long until each frees itself, so a caller can decide between closing one and waiting:

```
live session limit reached (1/1). Holding the slot: pw-123-456 (idle 22s,
auto-closes in 38s). Either call session_close on one of them to free a slot
now, or wait for the timeout.
```

::: tip Sweeps ride on activity
The plugin subprocess is itself idle-killed after a few minutes, so there is no always-on timer. Sweeps run when the plugin is awake handling a browser op — an abandoned session is reclaimed the next time anything browser-related happens. On a completely idle host nothing sweeps, which costs nothing: if no work is running, nothing is competing for the memory either.
:::

### Orphan sweeping

The idle reaper works from session metadata, which records **one** PID per session — but a browser is a process tree it builds itself. Chrome forks renderer, GPU and utility children that appear in no metadata; other engines may fork and let the originally-recorded PID exit, so the stored PID can even be the wrong one. Anything that escapes the recorded parent is invisible to a PID-based sweep while still holding memory.

`BrowserKillOrphans` closes that gap. When enabled, each sweep also scans running processes for a `--user-data-dir` under the session directory and terminates any whose profile no live session claims.

Two properties make it safe to leave on:

- **Ownership is the profile directory, not the process name.** A browser you launched yourself keeps its profile elsewhere, so it can never match — the sweep cannot touch it no matter what engine it is.
- **Active sessions are protected as a whole tree.** Protection is keyed on the user-data dir, so every child of a session still inside its idle window is spared along with its parent.

Off by default because the scan enumerates every process on the host: worth it on a server where a stray browser is pure cost, needless on a developer machine.

### Recording network requests

Pass `record_request: true` to a `run` script to capture every HTTP request the browser makes while the script runs (XHR/fetch calls, by default — static assets are skipped as noise):

```json
{
  "actions": [{"action": "goto", "url": "https://abc.com/dashboard"}],
  "record_request": true,
  "record_name": "dashboard-load",
  "record_url_pattern": "/api/",
  "record_include_assets": false
}
```

- `record_url_pattern` (optional) — a substring or regex the request URL must match to be recorded; leave empty to record everything (minus assets).
- `record_include_assets` — set true to also capture images/CSS/fonts/JS.
- `record_name` — name for the saved capture (default `captured`); when a `profile` is also set on the run, the capture is stored under that profile instead of by name.
- Duplicate requests (same method + URL + body — typical of SPA polling) are collapsed to one entry.

Each recorded request carries method, URL, headers, cookies, body, and the response status + headers — read it back afterwards with `get_request` (give the same `profile` or `name` the run used). This is meant for inspecting or replaying calls over plain HTTP later, not for driving the browser itself.

### Live browser panel

The agents conversation UI has a right-side **Browser** panel (the globe icon on the rail) that shows a live view of a `playwright_browser` live session — watch the page, and in **Full** mode click / type / log in manually.

- **Pick an instance.** The panel lists your active `playwright_browser` connector instances in a dropdown (a single usable instance is auto-selected). Nothing runs until you open the panel and pick one.
- **Open or reuse a session.** "New session" spawns a live browser (`session_open`); existing ones appear in the session dropdown. Multi-tab sessions get a tab switcher.
- **Two modes.** *View only* streams the screen (CDP `Page.startScreencast`) with no input. *Full* additionally forwards your mouse and keyboard to the browser (CDP `Input.dispatch*`) — this is what lets you log in by hand or drive a page the agent can't. Click the view first to capture keyboard.
- **Clipboard.** In Full mode, Ctrl/Cmd+C and +V flow straight to the remote browser, so in-page copy/paste works natively. Wick also bridges your local clipboard across the boundary: focusing the view pushes your PC clipboard into the remote (so a paste inside gets what you copied outside), and leaving the view pulls the remote's copied text/selection back to your PC clipboard. Right-click opens a small Copy/Paste menu on the live view itself, since the remote's native context menu can't be streamed.
- **Resize / pop out.** The rail panel is narrow, so the view has expand controls: **Pop out to window** floats it as a draggable, resizable mini-screen (drag the header to move, the corner to resize), and **Fullscreen** fills the viewport. **Dock** returns it inline. The stream and input keep working across all three.

**How it works:** a live session is a detached Chromium with an unauthenticated CDP port on `127.0.0.1`. Wick core (not the browser) dials that loopback port and proxies a same-origin WebSocket to the panel — the raw CDP port is never exposed to your browser. Access is gated per-instance by the same rule as editing the connector's credentials (admin, owner tag, or "allow others to configure"). Chromium-based engines only, like all live sessions.

The connector's detail page in the manager UI has a matching **Active sessions** section: it lists every live session on that instance, lets you inspect a session's open tabs, jump straight to its live view (opens the conversation panel above, pre-pointed at that session), or kill it to free the browser process.

### Extensions

The connector detail page (manager UI) has an **Extensions** section to load Chrome extensions into this connector's live sessions:

- **Upload** — drag-drop or pick a `.zip` or `.crx`. It's unpacked and stored under the connector's session dir.
- **From the Web Store** — paste an extension's 32-character Web Store id; wick downloads its `.crx` and installs it.
- **Remove** — deletes an installed extension.

Two things to know:

- **Applies to new sessions.** Chrome loads extensions only at launch, so installing/removing affects the *next* `session_open`, not sessions already running.
- **Forces headed.** `--load-extension` only works in a headed browser, so any installed extension makes new sessions run headed regardless of the Headless config.

`extension_list` / `extension_install` / `extension_remove` are **manager-only** (see below): installing an extension changes how every later session launches, which is an admin decision rather than something an agent should reach for mid-task.

### Manager-only operations

Some operations exist to back the manager UI, not to be called by an agent. They are registered `ConfigOnly`, which hides them from the whole MCP surface (`wick_list`, `wick_search`, `wick_get`) while the manager still runs them through its `/test` path:

| Op | Why it is hidden |
|---|---|
| `session_endpoints` | Raw CDP plumbing (`ws_debugger_url`) for the live-browser panel. An agent drives a session through `run` / `screenshot` instead. |
| `extension_list`, `extension_install`, `extension_remove` | Installing an extension reconfigures *every* later session (headed, `--load-extension`) for everyone using the connector. |
| `browser_status`, `browser_install`, `browser_update`, `browser_uninstall` | Engine management. `browser_status` returns HTML for the picker widget, and installs block on downloads that reach hundreds of megabytes. |

Hiding them also keeps the agent's tool list focused: what remains is the set an agent actually drives — page tasks, scripted runs, live sessions, profiles, and recorded requests.

### Maintenance

Backs the manager's browser picker. **Manager-only** — hidden from the MCP tool surface (see [Manager-only operations](#manager-only-operations)), so the LLM cannot call them.

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `browser_status` | no | — | Reports which engines (`chromium`, `firefox`, `webkit`, `cloakbrowser`, `cloakbrowser-pro`) are installed and their versions. Returns HTML for the `html=` widget. |
| `browser_install` | yes | `browser` | Downloads one engine's binary. Idempotent — installing an already-present engine returns fast. Chromium/Firefox/WebKit install synchronously; free CloakBrowser downloads in the background and reports progress back through `browser_status` (polled by the widget); CloakBrowser Pro installs synchronously via the `cloakbrowser` CLI (requires `CloakLicenseKey`). |
| `browser_update` | — | `browser` | Re-fetches an engine's binary to the newest available build. Backs the picker's ⋮ **Update** menu item — see [Installing, updating, uninstalling](#installing-updating-uninstalling). |
| `browser_uninstall` | — | `browser` | Removes an engine's downloaded binary. Backs the picker's ⋮ **Uninstall** menu item. |

## CloakBrowser

`cloakbrowser` and `cloakbrowser-pro` are two engine options alongside chromium/firefox/webkit — a patched, stealth Chromium published by [CloakHQ](https://github.com/CloakHQ/CloakBrowser). Neither is a Playwright-managed browser (no `playwright.Install`); they differ in how the binary is obtained and what limits apply:

| | `cloakbrowser` (free) | `cloakbrowser-pro` |
|---|---|---|
| Binary source | Downloaded straight from a GitHub release for the host OS/arch (`CloakRepo`, default `CloakHQ/CloakBrowser`) | Managed by the official `cloakbrowser` CLI (`cloakbrowser install` / `update`), which resolves the binary for your license's tier |
| License key | Not required | Required — set `CloakLicenseKey` (`cb_...`); passed to the browser at launch as `CLOAKBROWSER_LICENSE_KEY` |
| Concurrent live sessions | **Locked to 1**, regardless of `MaxLiveSessions` — this is CloakBrowser's free-plan limit. A second `session_open` fails to get a slot rather than silently queuing. | Follows `MaxLiveSessions` once the license key resolves to a paid tier (checked via the CLI's `info` output); falls back to the same 1-session lock if the tier can't be confirmed (e.g. CLI unreachable) |
| Requires | Nothing extra — wick manages the download | The `cloakbrowser` CLI installed and reachable on `PATH` (or point `CloakCLIPath` at it) |

`CloakExecutablePath` overrides binary resolution for **either** engine — set it to skip the GitHub download or the CLI lookup entirely and launch an already-downloaded binary directly.

Because a cloak engine never launches a Playwright-managed Chromium, opening a session with `Browser=cloakbrowser` or `cloakbrowser-pro` skips Playwright's own ~150MB Chromium download — only the (much smaller) Playwright node driver is fetched. Reconnecting to an already-open live session (over CDP) skips it too, for any engine, since reconnecting never launches a new browser.

### Installing, updating, uninstalling

The browser picker (the `html=` widget described above) lists both cloak engines alongside chromium/firefox/webkit. An installed row carries a **⋮ menu** with:

- **Update** — re-fetches the binary. For the free engine this pulls the latest GitHub release (shown as an amber "update" pill on the row when a newer tag is available); for the pro engine it runs `cloakbrowser update` to refresh to the licensed tier's newest build.
- **Uninstall** — removes the downloaded binary so the row flips back to "not installed". For the free engine this deletes wick's own download cache; for the pro engine it clears the CLI's cache. A binary reached via an explicit `CloakExecutablePath` override is never deleted — wick only removes what it downloaded.

Clicking a not-yet-installed row's **Download** button installs it: the free engine downloads in the background with a progress bar (poll `browser_status`, or just watch the picker); the pro engine requires `CloakLicenseKey` to be set first and installs synchronously via the CLI.

## Quirks worth knowing

- `pdf` only works on Chromium instances — set `Browser` to `chromium` (the default) if you need PDF rendering.
- Live-session browsers are detached OS processes (not tied to the plugin subprocess), reconnected over CDP on each call — verified to work around Windows' fixed-debug-port restrictions via a dynamic `--remote-debugging-port=0`.
- CloakBrowser (free) installs run in the background and can take a while (~200MB download); poll `browser_status` (or watch the picker) for progress rather than expecting `browser_install` to block until done.
- `Device` overrides `ViewportWidth` / `ViewportHeight` when set.
- `wait_for_load_state` with `state=networkidle` is capped at 10s — sites that poll in the background never reach "0 in-flight requests", so a timeout there is treated as good enough (the step continues with a note) instead of stalling the run for minutes.
- A browser killed without a clean exit (OOM, `kill -9`, host restart) leaves Chromium's singleton files (`SingletonLock`, `SingletonSocket`, `SingletonCookie`, `DevToolsActivePort`) pointing at a dead PID, which makes the *next* launch against that profile fail. `session_open` clears those automatically when the PID they name is gone — a lock owned by a live process is never touched.

## See also

- [Connector Plugins](/guide/connector-plugins) — install / update / uninstall flow.
- [Config tags reference — `html=`](/reference/config-tags#html-—-server-rendered-widget) — the widget contract this connector's browser picker is built on.
- [Connector Module](/guide/connector-module) — module contract.
