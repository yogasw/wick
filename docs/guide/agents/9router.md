---
outline: deep
---

# 9router

**9router** is an embedded LLM router and proxy dashboard, accessible from inside wick without shell access or an exposed port.

It is powered by the [9router](https://github.com/decolua/9router) npm package. Wick installs, manages, and proxies it — the dashboard is reachable through the same wick URL with no extra tunnel or firewall rule.

## Where to find it

Go to **Agents → More → 9router** in the sidebar (`/tools/agents/9router`). The page is **admin-only** — non-admin users do not see the entry.

## How it works

9router runs as a local subprocess on loopback port `20128`. That port is never exposed externally. Wick reverse-proxies it under the wick origin at `/9router/` and rewrites root-absolute URLs in HTML, JS, and CSS responses so the dashboard loads correctly under one origin — making it accessible through any existing wick tunnel without opening a second port.

## Dashboard tab

The **Dashboard** tab embeds the 9router web UI in an iframe. It behaves the same as the standalone 9router dashboard — configure routes, inspect request logs, and manage provider keys — but without leaving wick.

**Theme**: the iframe follows wick's active light/dark theme automatically. Wick is the source of truth; changing wick's theme updates the embedded dashboard.

## Settings tab

The **Settings** tab is the management surface for the 9router process itself.

### Install / Update

9router is an npm package. Wick installs it on demand — `npm` must be available on the host PATH.

| Button | What it does |
|---|---|
| **Install** | Runs `npm install -g 9router`. Appears when 9router is not yet installed. |
| **Update** | Runs `npm install -g 9router@latest`. Appears when 9router is already installed. |

### Process controls

| Button | What it does |
|---|---|
| **Start** | Launches the 9router subprocess. |
| **Stop** | Terminates the subprocess gracefully. |
| **Restart** | Stop + Start in sequence. |

### Auto-start on boot

Toggle **Auto-start on boot** to have wick launch 9router automatically every time wick starts.

When enabled, 9router startup is added as a **boot-gate step** — the wick "Booting…" splash screen waits for 9router to become ready before the dashboard opens. This ensures the dashboard is immediately available on first page load.

When disabled, 9router must be started manually from the Settings tab each time wick restarts.

### Logs

A live **Logs** panel streams 9router's stdout/stderr output. Use it to diagnose startup errors, inspect request routing, or confirm that 9router reached ready state.

## How the embedding works (and its limits)

9router is a Next.js single-page app with **no base-path support** — it emits root-absolute URLs (`/login`, `/_next/...`, `fetch("/api/...")`, `url(/_next/static/media/...)` in CSS, and OAuth `redirect_uri = origin + "/callback"`). On their own those would resolve against the wick root and 404, because wick only serves the dashboard under `/9router/`.

Wick's reverse proxy rewrites the response on the fly to keep everything under that prefix:

- **Redirects** — the `Location` header is prefixed (`/dashboard` → `/9router/dashboard`).
- **HTML** — a `<base href="/9router/">` tag is injected, and root-absolute `href`/`src`/`action` values (including the escaped forms inside the React flight stream) are prefixed.
- **JS bundles** — quoted absolute path literals are prefixed, including template-literal forms like `` `${origin}/callback` `` used by the OAuth flow.
- **CSS** — `url(/_next/static/media/...)` font/image references are prefixed.

The service worker skips `/9router/*` and the rewritten responses are sent `Cache-Control: no-store`, so a stale pre-rewrite copy is never cached.

### When a 9router path still 404s

This rewriting is best-effort. A path that 9router assembles from fragments at runtime, in a shape the rewriter doesn't recognise, can slip through and hit the wick root as a 404. If that happens:

1. Open the browser **Network** tab and find the 404 request. Its path (e.g. `/callback`, `/some-new-route`) is the missing prefix.
2. Add that prefix to `jsPathPrefixes` in `internal/tools/agents/9router/rewrite.go` and rebuild.

Paths that point at a **different local service** (for example 9router's own OAuth helper on a hardcoded `localhost:<port>`) are intentionally left untouched — they are not served by wick and must reach their own port directly.

## Requirements

- `npm` must be on the host PATH for Install/Update to work.
- The host's loopback interface must allow the 9router process to bind port `20128`.
- Admin access in wick.

## Access control

All controls — Install, Update, Start, Stop, Restart, Auto-start toggle, and Logs — are gated to admin users. The proxy endpoint at `/9router/` is also behind admin auth, so the dashboard is never reachable by non-admin sessions.

## See also

- [Web Terminal](../webtty) — another embedded process proxied through wick (gotty).
- [AI Agents overview](../agents) — the Agents shell that hosts 9router, Skills Manager, Channels, and more.
