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

The status badge reflects whether the dashboard port is actually reachable, not just whether wick itself spawned the process — so a 9router started outside wick (or one still running across a wick restart) correctly shows **Running**. The installed version is cached for up to an hour and refreshed in the background; on the very first check after a fresh page load it briefly shows **Checking…** instead of a stale or blank value.

The Dashboard tab's iframe uses the same reachability check, so it stays consistent with the badge: an externally-started (or restart-surviving) 9router process serves the embedded dashboard normally instead of showing "not running — start it first" while the badge says Running.

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

## Master switch and settings

Two fields in **Settings → Agents** (or `/admin/advanced` → group **9router**) control the feature globally:

| Field | Default | What it does |
|---|---|---|
| `Router9Enabled` | `true` | Master switch. Off = dashboard, `/9router/v1` proxy, auto-start, and all controls are disabled. Access visibility is managed separately under **Admin → Tools**. |
| `Router9Autostart` | `false` | Auto-start 9router on boot. When on, 9router joins the boot-gate sequence and the wick "Booting…" screen waits for 9router to be ready before opening. |

## Routing providers through 9router

Provider instances (`claude`, `codex`) can be told to route their upstream LLM calls through the embedded 9router proxy instead of hitting the provider's own API directly. This lets you apply 9router's routing rules, rate limits, and key management to every agent spawn without touching the provider's global config.

### Enabling the toggle

On the provider detail page (**Agents → Providers → open an instance**), scroll to the **9router** section. Enable the **Use 9router** toggle. This is **admin-only** — the section is not visible to non-admin users.

When the toggle is on, wick injects the appropriate env vars into the CLI's spawn environment so it talks to wick's own `/9router/v1` proxy instead of the provider's cloud endpoint:

| Provider | Injected env vars |
|---|---|
| `claude` | `ANTHROPIC_BASE_URL=http://127.0.0.1:<port>/9router/v1`, `ANTHROPIC_AUTH_TOKEN=<key>` |
| `codex` | `OPENAI_API_KEY=<key>` (env), plus `-c` config overrides: `model_provider=9router`, `model_providers.9router.base_url=http://127.0.0.1:<port>/9router/v1`, `model_providers.9router.wire_api=responses`, `auth_mode=apikey` |

The `<port>` is `WICK_PORT` (wick's own HTTP port), so the CLI talks to wick's loopback address and the request is forwarded through the `/9router/v1` subtree to the running 9router process.

### Model slots

Each provider type exposes named model slots. You can pick a concrete 9router model ID for each slot — all slots are optional; an unset slot is left to 9router's own default.

| Provider | Slots | Maps to |
|---|---|---|
| `claude` | `opus`, `sonnet`, `haiku` | `ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL` |
| `codex` | `model` (primary), `subagent` | codex `-c model=…` and `-c subagent_model=…` config overrides |

The model picker in the form shows the available slots for the instance's type. Use a 9router route ID (e.g. `cc/claude-opus-4-6`) that 9router resolves to the real upstream model.

### Custom API key

By default, wick uses `sk_9router` as the 9router auth token. To use a custom key (useful when 9router is configured with a real secret), enter it in the **Custom API Key** field. The value is stored encrypted at rest; the detail page shows only whether a key is set, never the plaintext.

Leave the field blank to revert to the default token.

### Requirements

- 9router must be running (started from the Settings tab or auto-started on boot) before any spawn that has **Use 9router** enabled — otherwise the CLI will fail to reach the proxy.
- Only `claude` and `codex` instances support the toggle. `gemini` instances do not show the 9router section.

## Unauthenticated `/9router/v1` proxy

Wick mounts an **unauthenticated** OpenAI-compatible API proxy at:

```
<wick-origin>/9router/v1/
```

This path is intentionally exempt from the wick session cookie requirement. Spawned AI CLIs must reach this endpoint without a browser session — they authenticate to 9router using the 9router API key instead of a wick session. The proxy is also exempt from the host allowlist check when accessed over loopback (`127.0.0.1`), matching the same exemption `/mcp` has.

The admin-gated dashboard proxy at `/9router/` (no `/v1`) is a separate mount and remains admin-cookie-protected.

## See also

- [Providers](./providers) — configure provider instances, including the Use 9router toggle.
- [Web Terminal](../webtty) — another embedded process proxied through wick (gotty).
- [AI Agents overview](../agents) — the Agents shell that hosts 9router, Skills Manager, Channels, and more.
