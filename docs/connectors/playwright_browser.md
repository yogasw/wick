---
outline: deep
---

# Playwright Browser

`playwright_browser` drives a real browser (Chromium / Firefox / WebKit, plus [CloakBrowser](#cloakbrowser)) to screenshot, scrape, render PDF, evaluate JS, and run scripted interaction flows. Each op launches an isolated browser inside the plugin's own process — no shared state, safe to run concurrently — except in [live-session mode](#live-session), where a browser is kept open across calls.

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
| Custom binary *(collapsed)* | `ExecutablePath` | Path to a custom browser binary instead of the bundled one. |
| Custom binary | `Channel` | Branded channel (`chrome`, `chrome-beta`, `msedge`, …) for the chosen browser. |
| CloakBrowser *(collapsed)* | `CloakRepo` | GitHub `owner/repo` hosting CloakBrowser release assets. Default `CloakHQ/CloakBrowser`. |
| CloakBrowser | `CloakExecutablePath` | Path to an already-downloaded CloakBrowser binary — set to skip the GitHub download. |

**The browser picker** is a [`html=` widget](/reference/config-tags#html-—-server-rendered-widget) — the connector renders its own status card per engine (installed / not installed, version, a Download button) and the widget wires clicks back to `browser_status` / `browser_install`. This is the reference implementation for `html=` in the docs.

## Operations

Four op groups.

### Page tasks

Ephemeral: open a URL, do one thing, close the browser.

| Op | Input | What it does |
|---|---|---|
| `screenshot` | `url`, `full_page`, `selector`, `wait_for` | PNG screenshot as base64. `full_page` captures the whole scrollable page; `selector` scopes to one element. |
| `get_content` | `url`, `selector`, `as_text` (default true), `wait_for` | Rendered content after JS runs — visible text by default, or HTML when `as_text` is false. |
| `pdf` | `url`, `wait_for` | Page rendered to PDF as base64. **Chromium only** — errors on firefox/webkit/cloakbrowser instances. |
| `scrape` | `url`, `fields` (JSON map of key → CSS selector), `wait_for` | Structured extraction — each selector's inner text is returned under its key; a selector matching nothing returns `""`. |
| `eval` | `url`, `script` | Evaluates a JS expression in the page, returns the JSON-serialized result. Marked **destructive** — arbitrary JavaScript can submit forms and change remote state. |

### Scripted flow

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `run` | yes | `actions` (JSON array), `session_id` (optional) | Runs an ordered list of browser actions in one session and returns a result per step; stops at the first failure. Pass `session_id` to run against a persistent [live session](#live-session) instead of a throwaway browser. |

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

Persistent browsers that survive across calls — and plugin restarts — until closed. The browser runs as a **detached OS process** reached over CDP, so it outlives the idle-swept plugin subprocess; only `session_close` ends it.

> **Chromium-based engines only.** Live sessions require the Chromium DevTools protocol, which only `chromium` and `cloakbrowser` (patched Chromium) expose. `session_open` errors on a `firefox` / `webkit` instance — use the ephemeral ops (`run`, `screenshot`, …) for those, or set `Browser=chromium`.

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `session_open` | yes | — | Launches a persistent browser, returns its `session_id`. Respects `MaxLiveSessions`. |
| `session_list` | no | — | Lists every live session and its open tabs (index, url, title). Dead sessions are swept automatically. |
| `tab_new` | no | `session_id`, `url` | Opens a new tab in a live session, optionally navigating it. |
| `tab_close` | yes | `session_id`, `index` | Closes the tab at `index` (from `session_list`). |
| `session_close` | yes | `session_id` | Kills the session's browser and frees its resources. **Always close sessions you opened** — an abandoned one holds a browser process open until closed or reboot. |
| `session_endpoints` | no | `session_id` | Returns the session's raw CDP details: `cdp_url` plus one entry per tab with `target_id` + `ws_debugger_url`. Read-only; backs the live-browser panel (below). Not meant for agent use. |

Pass a live session's `session_id` to `run` (or any task op) to reuse the same open browser instead of launching a throwaway one.

### Live browser panel

The agents conversation UI has a right-side **Browser** panel (the globe icon on the rail) that shows a live view of a `playwright_browser` live session — watch the page, and in **Full** mode click / type / log in manually.

- **Pick an instance.** The panel lists your active `playwright_browser` connector instances in a dropdown (a single usable instance is auto-selected). Nothing runs until you open the panel and pick one.
- **Open or reuse a session.** "New session" spawns a live browser (`session_open`); existing ones appear in the session dropdown. Multi-tab sessions get a tab switcher.
- **Two modes.** *View only* streams the screen (CDP `Page.startScreencast`) with no input. *Full* additionally forwards your mouse and keyboard to the browser (CDP `Input.dispatch*`) — this is what lets you log in by hand or drive a page the agent can't. Click the view first to capture keyboard.
- **Resize / pop out.** The rail panel is narrow, so the view has expand controls: **Pop out to window** floats it as a draggable, resizable mini-screen (drag the header to move, the corner to resize), and **Fullscreen** fills the viewport. **Dock** returns it inline. The stream and input keep working across all three.

**How it works:** a live session is a detached Chromium with an unauthenticated CDP port on `127.0.0.1`. Wick core (not the browser) dials that loopback port and proxies a same-origin WebSocket to the panel — the raw CDP port is never exposed to your browser. Access is gated per-instance by the same rule as editing the connector's credentials (admin, owner tag, or "allow others to configure"). Chromium-based engines only, like all live sessions.

### Extensions

The connector detail page (manager UI) has an **Extensions** section to load Chrome extensions into this connector's live sessions:

- **Upload** — drag-drop or pick a `.zip` or `.crx`. It's unpacked and stored under the connector's session dir.
- **From the Web Store** — paste an extension's 32-character Web Store id; wick downloads its `.crx` and installs it.
- **Remove** — deletes an installed extension.

Two things to know:

- **Applies to new sessions.** Chrome loads extensions only at launch, so installing/removing affects the *next* `session_open`, not sessions already running.
- **Forces headed.** `--load-extension` only works in a headed browser, so any installed extension makes new sessions run headed regardless of the Headless config.

### Maintenance

Backs the manager's browser picker. **Not meant for agent use** — seed these ops `AdminOnly` (see the [`html=` widget](/reference/config-tags#html-—-server-rendered-widget) reference) so the LLM can't call them.

| Op | Destructive | Input | What it does |
|---|---|---|---|
| `browser_status` | no | — | Reports which engines (`chromium`, `firefox`, `webkit`, `cloakbrowser`) are installed and their versions. Returns HTML for the `html=` widget. |
| `browser_install` | yes | `browser` | Downloads one engine's binary. Idempotent — installing an already-present engine returns fast. Chromium/Firefox/WebKit install synchronously; CloakBrowser downloads in the background and reports progress back through `browser_status` (polled by the widget). |

## CloakBrowser

`cloakbrowser` is a fourth engine option alongside chromium/firefox/webkit — a patched, stealth Chromium published by [CloakHQ](https://github.com/CloakHQ/CloakBrowser). It is **not** a Playwright-managed browser: there's no `playwright.Install` for it, so the connector downloads the right release asset for the host OS/arch straight from GitHub, extracts it, and launches it via `ExecutablePath` with anti-automation flags. Use `CloakRepo` to point at a fork/mirror, or `CloakExecutablePath` to skip the download entirely and use an already-downloaded binary.

Because `cloakbrowser` never launches a Playwright-managed Chromium, opening a session with `Browser=cloakbrowser` skips Playwright's own ~150MB Chromium download — only the (much smaller) Playwright node driver is fetched. Reconnecting to an already-open live session (over CDP) skips it too, for any engine, since reconnecting never launches a new browser.

## Quirks worth knowing

- `pdf` only works on Chromium instances — set `Browser` to `chromium` (the default) if you need PDF rendering.
- Live-session browsers are detached OS processes (not tied to the plugin subprocess), reconnected over CDP on each call — verified to work around Windows' fixed-debug-port restrictions via a dynamic `--remote-debugging-port=0`.
- CloakBrowser installs run in the background and can take a while (~200MB download); poll `browser_status` (or watch the picker) for progress rather than expecting `browser_install` to block until done.
- `Device` overrides `ViewportWidth` / `ViewportHeight` when set.

## See also

- [Connector Plugins](/guide/connector-plugins) — install / update / uninstall flow.
- [Config tags reference — `html=`](/reference/config-tags#html-—-server-rendered-widget) — the widget contract this connector's browser picker is built on.
- [Connector Module](/guide/connector-module) — module contract.
