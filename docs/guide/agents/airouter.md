---
outline: deep
---

# AI Router

**AI Router** embeds one or more local LLM router/proxy dashboards inside wick — accessible with no shell access and no exposed port. Today it ships two backends you can run side by side and switch between:

- **[9router](https://github.com/decolua/9router)** — an LLM router + proxy dashboard.
- **[OmniRoute](https://github.com/diegosouzapw/OmniRoute)** — an AI gateway that aggregates many providers behind one OpenAI-compatible endpoint.

Wick installs, manages, and reverse-proxies each one. Every router is reachable through the same wick URL with no extra tunnel or firewall rule. More routers can be added over time, and each provider instance can be pointed at whichever router you choose.

## Where to find it

Go to **Agents → More → AI Router** in the sidebar (`/tools/agents/airouter`). The page is **admin-only** — non-admin users do not see the entry.

## Switching routers

When more than one router is registered, a **switcher** row at the top of the page lists each one with a live status dot. Click a router to view its Dashboard, Requests, and Settings. Routers run **concurrently** — each on its own loopback port — so you can start several and flip between them to compare or check any of them without stopping the others.

Each router is proxied under its own prefix at the wick root: `/airouter/9router/`, `/airouter/omniroute/`, and so on.

## Dashboard tab

The **Dashboard** tab embeds the selected router's web UI in an iframe. It behaves like the standalone dashboard — configure routes, inspect logs, manage keys — without leaving wick. The iframe follows wick's active light/dark theme automatically.

## Requests tab

The **Requests** tab is a live stream of the calls proxied through the selected router's `/airouter/<id>/v1` API. Bodies are captured only while the tab is open and held in your browser — nothing is stored on the server.

## Settings tab

The **Settings** tab manages the selected router's process.

### Install / Update

Each router is an npm package (`9router`, `omniroute`, …). Wick installs it on demand — `npm` must be on the host PATH.

| Button | What it does |
|---|---|
| **Install** | Runs `npm install -g <pkg>@latest`. Appears when the router is not installed. |
| **Update** | Re-runs the install to pull the latest. |

### Process controls

| Button | What it does |
|---|---|
| **Start** | Launches the router subprocess on a free loopback port. |
| **Stop** | Terminates the subprocess. |
| **Restart** | Stop + Start in sequence. |

Both 9router and OmniRoute default to port `20128`; when the second one starts, wick remaps it to the next free loopback port so they don't collide. The port never leaks — wick proxies to it. The status badge reflects whether the dashboard port is actually reachable, so a router started outside wick (or one surviving a wick restart) still shows **Running**.

### Auto-start on boot

Each router has its own **Auto-start on boot** toggle. When any router is set to auto-start, wick launches it during boot and the "Booting…" splash waits for it to become ready.

### External API access

Each router has an **Allow external API access** toggle. Off (default), its `/airouter/<id>/v1` API answers only local spawns on this machine; off-machine callers (tunnel / public URL) get 403. On, remote callers reach the router with their real address so it enforces its own API key — local spawns still need no key.

### Logs

A live **Logs** panel streams the selected router's stdout/stderr.

## Routing providers through a router

Provider instances (`claude`, `codex`) can route their upstream LLM calls through an embedded router instead of hitting the provider's cloud API directly.

### Enabling the toggle

On the provider detail page (**Agents → Providers → open an instance**), open the **AI Router** section, enable **Route through AI Router**, and pick which router (9router / OmniRoute) from the dropdown. This is admin-only.

When on, wick injects the right env/args into the CLI's spawn environment so it talks to wick's own `/airouter/<id>/v1` proxy:

| Provider | Injected |
|---|---|
| `claude` | `ANTHROPIC_BASE_URL=http://127.0.0.1:<port>/airouter/<id>/v1`, `ANTHROPIC_AUTH_TOKEN=<key>` |
| `codex` | `OPENAI_API_KEY=<key>` (env), plus `-c` overrides: `model_provider=<id>`, `model_providers.<id>.base_url=…/airouter/<id>/v1`, `wire_api=…`, `auth_mode=apikey` |

`<port>` is `WICK_PORT`, so the CLI talks to wick's loopback and the request is forwarded to the selected router.

### Model slots

Each provider type exposes named model slots; the slots and their placeholders are defined by the selected router, so switching routers re-fetches them. All slots are optional — an unset slot is left to the router's own default.

| Provider | Slots | Maps to |
|---|---|---|
| `claude` | `opus`, `sonnet`, `haiku` | `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL` |
| `codex` | `model`, `subagent` | codex `--model` / `agents.subagent.model` overrides |

### Custom API key

By default wick uses the router's own default credential (9router accepts `sk_9router`; OmniRoute has none — copy a key from its dashboard). Enter a custom key in the **API Key** field; it is stored encrypted at rest, and the detail page shows only whether a key is set. Leave blank to keep the current value.

Only `claude` and `codex` instances support the toggle; `gemini` does not.

## Unauthenticated `/airouter/<id>/v1` proxy

Wick mounts an **unauthenticated** OpenAI-compatible API proxy per router at:

```
<wick-origin>/airouter/<id>/v1/
```

This path is exempt from the wick session cookie so spawned AI CLIs can reach it — they authenticate to the router with its API key instead. Over loopback it is also exempt from the host allowlist (like `/mcp`). The admin-gated dashboard proxy at `/airouter/<id>/` (no `/v1`) is a separate, cookie-protected mount.

## Master switch and settings

One field in **Settings → Agents** (group **AI Router**) controls the whole feature:

| Field | Default | What it does |
|---|---|---|
| `AirouterEnabled` | `true` | Master switch. Off = every dashboard, `/airouter/<id>/v1` proxy, auto-start, and all controls are disabled. |

Per-router auto-start and external-API toggles live on the AI Router page itself, stored under `airouter_<id>_autostart` / `airouter_<id>_external`.

## Adding a new router

Each router lives in its own folder under `internal/agents/airouter/<id>/` and registers a descriptor (npm package, port, launch args, and a spawn hook that supplies each agent type's env/args). Adding a router is "new folder + `Register`" — the core, server mounts, spawners, and UI adapt automatically. See `internal/planning/in-progress/airouter/design.md`.

## Requirements

- `npm` on the host PATH for Install/Update.
- The loopback interface must allow each router to bind its port.
- Admin access in wick.

## See also

- [Providers](./providers) — configure provider instances, including the Route through AI Router toggle.
- [AI Agents overview](../agents) — the Agents shell that hosts AI Router, Skills Manager, Channels, and more.
