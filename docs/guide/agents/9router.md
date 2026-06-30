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

## Requirements

- `npm` must be on the host PATH for Install/Update to work.
- The host's loopback interface must allow the 9router process to bind port `20128`.
- Admin access in wick.

## Access control

All controls — Install, Update, Start, Stop, Restart, Auto-start toggle, and Logs — are gated to admin users. The proxy endpoint at `/9router/` is also behind admin auth, so the dashboard is never reachable by non-admin sessions.

## See also

- [Web Terminal](../webtty) — another embedded process proxied through wick (gotty).
- [AI Agents overview](../agents) — the Agents shell that hosts 9router, Skills Manager, Channels, and more.
