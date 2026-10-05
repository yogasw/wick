---
name: wick-plugin-service
description: Use when building, changing, testing or debugging a wick SERVICE plugin — the always-on plugin wick supervises and serves at /x/{key}/* (webhook receivers, public or token APIs, pages for signed-in users, A2A / Team remote agents). Covers the pkg/service Module (Meta, Routes, Configs, CallbackScopes, Register, RemoteSource), route auth (public / token / wick-session) and longest-prefix matching, the headers wick strips and injects (X-Wick-Base, X-Wick-User-*), Env and live config, the callback token and /x/-/api/, access tokens, the supervisor lifecycle and backoff, the admin page, the Team remote agent source, a2aservice, testing with service.Handler, and pitfalls. For the other plugin kinds and packaging use wick-plugin-authoring; for installing and operating plugins use wick-plugins.
---

# Service plugins — always-on at `/x/{key}/*`

> **Scope:** the service plugin contract and the host that runs it. Picking a kind,
> templates and `wick plugin build` are in **wick-plugin-authoring**; installing,
> sources, signing and troubleshooting installs are in **wick-plugins**. Docs:
> `docs/plugins/authoring-service.md` and `docs/plugins/service-a2a.md`.

## Mental model

A service plugin is a plain `http.Handler` in its own process. wick:

1. starts it at boot (and after a reload) and keeps it running — a crash is
   restarted with backoff (`internal/services/plugin/supervisor.go`);
2. reverse-proxies `/x/{key}/*` to it over a unix socket, deciding auth **per
   route** from the manifest (`host.go` `ServeHTTP`);
3. pushes its config over the gRPC control service (`Configure`) after every
   spawn and on every save;
4. optionally drives it as a Team remote agent (`remote_source`).

It reuses the tool-plugin machinery (HTTP on `$WICK_PLUGIN_SOCKET` + the `Tool`
control service `Schema/Configure/Health`); it differs in lifecycle (never
idle-killed) and mount point.

| | Tool plugin | Service plugin |
|---|---|---|
| SDK | `pkg/plugin/toolplugin` + `pkg/tool` | `pkg/service` |
| Mounted at | `/tools/{key}` (wick layout, login) | `/x/{key}/*`, no wick layout |
| Auth | wick login + tool access; `WebhookGroup` public | per route: `public` / `token` / `wick-session` |
| Lifetime | spawned on first request, idle-killed | always on, supervised |
| Router | `tool.Router` / `tool.Ctx` | stdlib `*http.ServeMux` (Go 1.22 patterns) |

Pick a service when something **outside wick** must reach the code at any time
(A2A agent, webhook relay, bot bridge) or when it keeps in-memory state between
requests. A page for signed-in users → tool. Something the LLM calls → connector.

## Files

| Path | What |
|---|---|
| `pkg/service/service.go` | `Module`, `Meta`, `Route`, `Env`, `RemoteSource`, `Describer`, `BaseURL`, auth consts |
| `pkg/service/serve.go` | `ServeService`, `Manifest`, `Handler`, remote RPC mount, `--dump-manifest` |
| `pkg/service/a2aservice/` | `Mount(mux, Card, Reply)` — A2A agent card + JSON-RPC on top of a reply func |
| `pkg/plugin/service.go` | wire types: `ServiceModule`, `ServiceRoute`, `Auth*`, `CapRemoteSource`, `RemotePath*`, `RemoteTurn/Event/SendResult`, `EnvPluginToken`, `EnvBaseURL`, `MatchRoute` |
| `plugins/service/_template/` | starter (`main.go`, `service.go`, `VERSION`, `README.md`) |
| `plugins/service/example_a2a_repeater/` | full example: A2A + `RemoteSource`, with tests |
| `internal/services/plugin/host.go` | load, `/x/` proxy, auth, header injection |
| `internal/services/plugin/supervisor.go` | process lifecycle, backoff, ring log |
| `internal/services/plugin/config.go` | config store (owner `service_plugin:<key>`), `SetConfig` |
| `internal/services/plugin/tokens.go` | access tokens (persisted, hashed) + callback tokens (memory) |
| `internal/services/plugin/callback.go` | `/x/-/api/...` callback API, `HandleCallback`, `CallerFrom` |
| `internal/services/plugin/admin.go` | `/manager/api/service-plugins` admin API, `RemoteSources` |
| `internal/services/plugin/plugintest/` | builds + runs the real repeater under a `Host` |
| `internal/agents/remote/pluginremote/` | Team remote agent adapter over `/_wick/remote/*` |
| `internal/tools/agents/api_team_plugin_remote.go` | `/tools/agents/api/team/plugin-sources`, `/tools/agents/api/team/plugin-remote` |
| `fe/manager/src/lib/components/services/ServiceDetail.svelte` | admin page |
| `internal/pkg/api/server.go` (search `servicePlugins`) | wiring |

## The Module

```go
// plugins/service/<key>/main.go
func main() { service.ServeService(Module()) }

// plugins/service/<key>/service.go
func Module() service.Module {
	return service.Module{
		Meta: service.Meta{Key: "my_service", Name: "My Service", Description: "…", Icon: "🛰️"},
		Routes: []service.Route{
			{Prefix: "/hook", Auth: service.Public},
			{Prefix: "/api", Auth: service.Token},
			{Prefix: "/", Auth: service.Session},
		},
		Configs:        []entity.Config{{Key: "greeting", Value: "hello", Description: "Text the API answers with"}},
		CallbackScopes: nil, // wick REST scopes the callback token may use
		Register: func(mux *http.ServeMux, env *service.Env) {
			mux.HandleFunc("POST /hook", …)
			mux.HandleFunc("GET /api/hello", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]string{"message": env.Cfg("greeting")})
			})
		},
		RemoteSource: nil, // set to become a Team remote agent source
	}
}
```

| Field | Notes |
|---|---|
| `Meta` | `Key` must equal the folder name and pass `wickplugin.ValidateKey` (lowercase, digits, `_`, `-` not at the ends; use `_` by convention). `Name`/`Description` show in the admin list and the Team wizard. |
| `Routes` | `[]service.Route` = `wickplugin.ServiceRoute{Prefix, Auth}`. Paths are relative to `/x/{key}`. |
| `Configs` | `[]entity.Config` (or `entity.StructToConfigs(cfg{})`). Seeded with `EnsureOwned` under owner `service_plugin:<key>`; `IsSecret` rows are encrypted and never sent back to the browser. |
| `CallbackScopes` | copied into the manifest; the callback token carries exactly these. |
| `Register(mux, env)` | mount handlers; keep `env` if handlers need config later (config arrives after `Register`). |
| `RemoteSource` | non-nil → manifest gets capability `remote_source` and the SDK mounts the `/_wick/remote/*` RPC. |

`Manifest(mod)` turns this into `wickplugin.ServiceModule`; `--dump-manifest`
wraps it via `wickplugin.BuildSelfServiceManifest` (kind=`service`, sha256,
optional `--sign-key`). Nothing is hand-written in `plugin.json`.

## Routes and auth

`ServiceModule.MatchRoute(path)` picks the **longest** matching prefix
(`/api` matches `/api` and `/api/...`, not `/apix`; `/` matches everything).
No match → 404. Tested in `pkg/service/serve_test.go` `TestMatchRouteLongestPrefix`.

| SDK const | Manifest value | Host behaviour |
|---|---|---|
| `service.Public` | `public` | passes through |
| `service.Token` | `token` | needs `Authorization: Bearer <access token>` generated on the admin page; else `401` + `WWW-Authenticate: Bearer realm="wick"`. The header is **deleted** before proxying — the plugin never sees it. |
| `service.Session` | `wick-session` | needs a signed-in wick user, else `401 sign in to wick first`. |

Before proxying, the host (`injectHeaders`) deletes every client `X-Wick-*`
header, then sets `X-Wick-Base: /x/{key}` and, for session routes,
`X-Wick-User-Id`, `X-Wick-User-Email`, `X-Wick-User-Name`, `X-Wick-User-Role`
(`admin`/`user`). No tags header (unlike tool plugins). Other headers, cookies
included, are forwarded unchanged; `X-Forwarded-*` is set. The path the
plugin sees has `/x/{key}` stripped. Responses are flushed per write
(`FlushInterval: -1`), so SSE works.

Reserved: any path under `/_wick/` is 404 from outside (only wick reaches the
remote RPC), and key `-` is the callback API (`/x/-/api/...`).

Use `service.BaseURL(r)` to rebuild the public URL of the service
(`X-Forwarded-Proto/Host` + `X-Wick-Base`), e.g. for an agent card.

## Env and config

- `env.Key()` — the plugin key.
- `env.Cfg(k)` — value last pushed by the host. The host calls `Configure`
  right after each spawn (5 s timeout; failure kills the process and counts as
  a failed start) and again from `SetConfig` → `Supervisor.Reconfigure`. A
  rejected push restarts the plugin. The SDK's `Configure` always accepts, so
  read `env.Cfg` per request instead of caching it at `Register` time.
- Saving config (`POST /manager/api/service-plugins/{key}/config`, body
  `{"values":{…}}`): only manifest keys are accepted; an empty or `••••••••`
  secret keeps the stored value.
- The process env carries `WICK_PLUGIN_SOCKET`, `WICK_BASE_URL` (wick's
  `AppURL`) and `WICK_PLUGIN_TOKEN` (unless revoked).
- State on disk: `wickplugin.DataDir(key)` only recognises the
  `plugins/connectors/<key>/` layout (`pkg/plugin/datadir.go`); a service binary
  sits in `plugins/services/<key>/`, so `DataDir` falls back to
  `<os temp>/wick-plugins/<key>`, which the OS may wipe. Keep durable state in a
  path from `Configs` or an external store until `DataDir` learns the
  `services/` layout.

## Calling wick back

`env.Callback()` returns `(baseURL, token)`; `env.CallbackRequest(ctx, method,
path, body)` builds a request with `Authorization: Bearer <token>`. Both are
empty outside wick. **Never log the token.**

- A fresh callback token (`wick_plg_…`) is issued on every spawn
  (`Tokens.IssueCallback`), held only in memory, scoped to `CallbackScopes`.
- Callback API: `/x/-/api/...` (`callback.go`). `GET /x/-/api/whoami` always
  works and answers `{"plugin","scopes"}`. Other endpoints exist only when host
  code registers them with `Host.HandleCallback("METHOD /api/...", scope, h)`;
  a token without that scope gets `403`. Handlers read the caller with
  `CallerFrom(ctx)`.
- **Today only `whoami` is registered** — declaring other scopes has no effect
  until a host endpoint uses them. Add the endpoint host-side first.
- Admin can revoke (`callback-revoke`) — the live token dies at once and later
  spawns get no `WICK_PLUGIN_TOKEN` until `callback-allow` (applies on next
  start).

Access tokens (`wick_svc_…`, for `token` routes) are different: persisted
hashed in `<plugins root>/service-tokens.json`, plaintext shown once, can be
rotated or revoked, `last_used` tracked.

## Lifecycle (supervisor.go)

- `Host.Load` scans `plugins/services/`, skips invalid keys, disabled plugins,
  manifests without a `service` section, and anything failing `VerifyManifest`.
- `Host.Start` starts every supervisor once; `Shutdown` stops all in parallel
  before a reload hands over, so no plugin outlives its wick.
- States: `stopped`, `starting`, `running`, `backoff`.
- Restart backoff: `BackoffMin = 1s`, doubled per failure up to
  `BackoffMax = 30s`; reset to 1 s when the process stayed up ≥ 1 minute.
- Stop: SIGTERM, `StopGrace = 5s`, then kill; the socket is removed.
- Socket: `svc-<key>-<n>.sock` in the plugin run dir.
- Log: stderr kept as the last `LogLines = 200` lines, plus `[wick]` lines
  (running, process exited, restarting in …, config updated).
- Installing or updating a service (upload, link or source) loads it from its
  new manifest and starts it at once — no wick reload (`Host.Install`, called
  from `OnInstalled` in `server.go`). The old process is stopped first.

## RemoteSource (Team remote agent)

```go
type RemoteSource interface {
	Send(ctx, service.RemoteTurn) (service.RemoteSendResult, error) // start a turn → handle (+ context id)
	Receive(ctx, handle string) (<-chan service.RemoteEvent, error) // events; close the channel after done/error
	Done(handle string)                                              // release what Send set up
}
// optional: Describe() (name, detail string) — names the agent in the wizard
```

The SDK serves it on `/_wick/remote/send` (POST, 502 on error),
`/_wick/remote/events?handle=` (SSE `data: <json>`, 404 on unknown handle),
`/_wick/remote/done`, `/_wick/remote/describe`.

- `RemoteTurn{Text, SessionID, ContextID}` — `ContextID` is what the plugin
  returned earlier for this wick session (`""` first). `pluginremote` persists
  it in the session dir (`plugin-remote.json`) and sends it back each turn.
- `RemoteEvent.Kind`: `text_delta | text | status | attachment | done | error`
  (event schema v1, `RemoteEventSchema = 1`); end every stream with `done` or
  `error`, then close.
- `Done` is called on cancel and when the turn ends.
- Listed by `Host.RemoteSources()` → `GET /tools/agents/api/team/plugin-sources`;
  created via `POST /tools/agents/api/team/plugin-remote` (source **Plugin** in
  the Remote agent wizard).

For an A2A front door use `a2aservice.Mount(mux, a2aservice.Card{…}, reply)` —
card at `/.well-known/agent.json` and `/.well-known/agent-card.json`, JSON-RPC
at `POST /`, streaming chunks from `reply(ctx, contextID, text, chunks)`.

## Admin API and page

`RegisterAdmin` under `/manager/api/service-plugins` (admin only):
`GET` (list), `GET /{key}` (full view: configs, tokens, logs), `POST /{key}/config`,
`POST /{key}/{start|stop|restart|callback-revoke|callback-allow}`,
`POST /{key}/tokens` (`{"name"}` → `{token, secret}`),
`POST /{key}/tokens/{id}/rotate`, `DELETE /{key}/tokens/{id}`.

UI: *Service plugins* section on the manager Connectors page → `/services/{key}`
(`ServiceDetail.svelte`): status + Stop/Start/Restart, Configuration, Routes,
Access tokens, Callback to wick, Log.

## Testing

```go
// in-process, no wick: config seeded, remote RPC mounted
srv := httptest.NewServer(service.Handler(Module(), map[string]string{"greeting": "hi"}))
sm := service.Manifest(Module()) // routes, capabilities, scopes
r, ok := sm.MatchRoute("/api/hello")
```

`Handler` does no auth — the host does. Test auth/proxy behaviour against a
`Host` (`internal/services/plugin/host_test.go` uses a fake `spawn`).
End-to-end with a real binary: `plugintest.StartRepeater(t)` builds
`example_a2a_repeater` (skipped with `-short`) and `plugintest.Transport(h)`
feeds `pluginremote`. Run plugin tests from `plugins/`
(`cd plugins && go test ./service/...`) — `./...` at the repo root does not
cover the nested module.

## Pitfalls

- Reading config in `Register` — it is empty there; read `env.Cfg` per request.
- Expecting the bearer on `token` routes — it is stripped; the plugin cannot
  tell which token was used.
- A route list without `/` makes every other path 404; a `/` route as `Public`
  exposes everything not covered by a longer prefix.
- `Route.Auth` values are `public`, `token`, `wick-session` (not `session`)
  when written by hand; use the SDK consts.
- Serving under `/_wick/` — unreachable from outside by design.
- Logging `WICK_PLUGIN_TOKEN` or putting it in a response.
- Trusting `wickplugin.DataDir(key)` for durable state — for a service it
  resolves to OS temp (see Env and config).
- Socket path must stay under 108 bytes (`WICK_PLUGIN_SOCKET_DIR`).
- A plugin that exits right after start loops through backoff: read the Log
  card (or `GET /manager/api/service-plugins/{key}`) — `last_error`,
  `restarts`, `next_start`.
