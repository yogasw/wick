# Live Browser Panel — Design

A right-side slide-over panel in the agents/conversation UI that shows a **live view of a playwright_browser live session** — watch the page, and (in Full mode) click / type / log in manually. Mirrors the existing "Scheduled" panel chrome.

## Status: implemented (v1)

- [x] **Plugin** — `session_endpoints` op: returns `cdp_url` + per-tab `ws_debugger_url` (discovery only; read from Chrome's `/json`, no frame relay through gRPC). `livesession.go` + `connector.go`.
- [x] **Plugin** — playwright_browser VERSION bumped to 0.5.0.
- [x] **Core** — WebSocket proxy `GET /manager/api/connectors/{key}/{id}/browser/ws?session=<id>&tab=<idx>`: executes `session_endpoints`, dials the loopback CDP ws, pumps both ways. `connectors_browser_proxy.go`.
- [x] **Core** — unary routes `GET .../browser/sessions`, `POST .../browser/open`, `POST .../browser/sessions/{session}/close`.
- [x] **Access** — gated per-row by `canConfigureRow` (admin → owner tag → AllowOthersConfigure), NOT admin-only (per user). key must be `playwright_browser`.
- [x] **FE** — `BrowserPanel.svelte`: instance dropdown → session list → live `<canvas>` from `Page.startScreencast`; Full / View-only toggle; Full forwards mouse/key/wheel as `Input.dispatch*`. Tab switcher for multi-tab sessions.
- [x] **FE** — registered in `DetailView.svelte` (RailTab `browser`, globe rail icon, desktop + mobile branches).
- [x] **FE** — `api/browser.ts` (Effect unary helpers + `wsURL` builder). Routes are under `/manager/api/...` (absolute), not the agents base.
- [x] **Docs** — panel documented in `docs/connectors/playwright_browser.md` (Live session section).
- [x] **Tests** — `pickTabWS` unit test (Go); `api/browser.ts` tests (listInstances mapping, listSessions unwrap, wsURL builder).

### Deferred (not in v1)

- [ ] Count badge on the rail for live-session count (skipped — panel is idle-until-opened; not worth a per-render session poll).
- [ ] `/` composer command to open the panel.
- [ ] Screencast quality/fps as a panel setting (hardcoded jpeg q60, everyNthFrame 1).
- [ ] Multi-viewer input arbitration (v1 = last-writer-wins, documented risk below).
- [ ] Full handler-level integration test (package `manager` has no httptest harness yet; Handler has heavy deps). Covered manually + via the extracted `pickTabWS` unit.

## Decision summary (from user)

- **Two modes, toggleable in the panel:** Full (interactive click/type/login) and View-only (watch only). Full is a superset — build screencast once, gate input on the toggle.
- **Instance selection:** dropdown listing active `playwright_browser` connector instances. Panel idle until opened.

## Why this architecture

The plugin↔core boundary is exactly 6 gRPC RPCs (`pkg/plugin/proto/connector.proto`) — core can only call declared Operations and get JSON back, no arbitrary channel. BUT a live session is a **detached Chromium** whose CDP endpoint is a plain `127.0.0.1:<port>` loopback listener on the **same host** as core (go-plugin runs the plugin as a local child). So:

- **Plugin's job = discovery only.** A new `session_endpoints` op returns the CDP base URL + per-tab WebSocket debugger URLs (read from `http://127.0.0.1:<port>/json`). This crosses gRPC once, cheap.
- **Core's job = the pipe.** Core opens its own WebSocket to the loopback CDP ws URL and proxies it to the browser client. Screencast frames (`Page.startScreencast` → `Page.screencastFrame`, base64 JPEG) and input events (`Input.dispatchMouseEvent`/`dispatchKeyEvent`) ride the raw CDP WebSocket — NOT relayed frame-by-frame through gRPC (which is 1MiB-chunked server-streaming, wasteful at video frame rates).

Rejected alternatives:
- *Poll `screenshot` op* — quick but each frame re-connects over CDP (`connectSession` = full playwright Run + ConnectOverCDP per call); slow, not interactive. Only viable as a throwaway MVP.
- *Relay screencast through a streaming plugin op (`ExecuteStream`)* — keeps the browser private to the plugin (needed only if core/plugin ever split across hosts), but doubles the hops and the gRPC chunking overhead. Not worth it for the default single-host deploy. Revisit if remote plugins land.

## Security

- The CDP port has **no auth** — anything on host loopback can drive the browser. The proxy route MUST be admin-authed (same guard as the connector `/test` route) and MUST validate `{key}` is a `playwright_browser` instance and `session` belongs to it before dialing. Core never exposes the raw CDP port to the browser; the client only ever talks to core's same-origin WS.
- No new inbound surface on the CDP port — core dials it as a loopback client, exactly like the plugin's own `cdpAlive` probe does today.

## Touch-points (concrete)

### Plugin — `plugins/connector/playwright_browser/`
- `livesession.go`: add `sessionEndpoints(c, id)` — reconnect meta, GET `<cdp>/json`, return `{cdp_url, tabs:[{index,target_id,ws_debugger_url,url,title}]}`. Register as op `session_endpoints` in `connector.go` (Live sessions category). Config-only? No — it's an agent-invisible maintenance-style read; put it in the Live sessions category but it's harmless read-only.
- `VERSION`: 0.4.0 → 0.5.0.

### Core — `internal/manager/`
- Router is `net/http` `mux.Handle("GET /manager/api/connectors/{key}...", auth(...))` (connectors.go RegisterConnectors, ~line 43-63) — NOT chi. New routes registered the same way, admin-authed.
- Dropdown source already exists: `GET /manager/api/connectors/{key}` → `apiConnectorRows` (connectors.go:43) lists instances of a key. FE filters/uses this; no new list endpoint needed.
- New file `connectors_browser_proxy.go`:
  - `apiBrowserWS` — `mux.Handle("GET /manager/api/connectors/{key}/{id}/browser/ws", admin(...))`; upgrade with **gorilla/websocket v1.5.3 (already in go.mod)**; execute `session_endpoints` to get the loopback CDP ws URL; dial it (gorilla client); bidirectional copy.
  - `apiBrowserOpen/Close` — thin wrappers over `h.connectors.Execute(...)` with ops `session_open`/`session_close`, like `testConnectorOperation` (connectors.go:728). Session listing reuses the existing `/test` path against `session_list`, or a tiny GET wrapper — decide during impl.
- Validate `{key}=="playwright_browser"` and the session belongs to `{id}` before dialing CDP.

### FE — `fe/agents/conversation/src/lib/`
- `components/BrowserPanel.svelte` (new): instance `<select>`, session list + "New session" button, `<canvas>` screencast target, Full/View toggle, close-session control. On Full: attach pointer/keyboard listeners → send `Input.dispatch*` over the panel's WS. Guard double-mount (panel renders in both desktop + mobile branches of DetailView).
- `api/browser.ts` (new): Effect helpers for sessions/open/close; the WS is opened directly in the component (not via the Effect http client).
- `components/DetailView.svelte`: extend `RailTab` union (line 141) with `"browser"`; add rail icon entry (railTabs ~899-925); add `{:else if railTab === "browser"}` branch in BOTH desktop (~1205) and mobile (~1328) blocks; optional count badge (sessions alive).

## Open questions / risks

- **playwright_browser is a PLUGIN, not in-tree.** The panel only works if that plugin is installed + has ≥1 active instance. Panel should show an empty/CTA state ("no playwright_browser instance") otherwise. Dropdown lists instances via a connector-instance lookup — confirm the manager has an API to list instances of a given connector key.
- **Chromium-only** (ties to the cloak fix just shipped): screencast needs CDP, so the session must be chromium/cloakbrowser. Firefox/webkit instances won't have live sessions at all, so they simply won't appear.
- **Frame rate / bandwidth**: `Page.startScreencast` with `format:jpeg, quality:~60, everyNthFrame:1` is fine for LAN/localhost. Expose quality as a panel setting later if needed.
- **Multi-viewer**: two panels on the same session both driving input = chaos. v1: last-writer-wins is acceptable; note it, don't solve it.
- **Tab focus**: screencast is per-target (per-tab). Panel needs a tab switcher (reuse `session_list` tabs). v1 can pin to tab 0 and add the switcher next.

## Rollout

1. Plugin op + VERSION bump (isolated, releasable on its own).
2. Core proxy + unary routes (behind admin auth; no FE yet — testable with wscat).
3. FE panel (View-only path first, then wire the Full-mode input toggle).
4. Docs + tests.
