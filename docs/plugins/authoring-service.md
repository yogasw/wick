# Authoring a service plugin

A service plugin is an always-on HTTP server that wick runs in its own process
and exposes at `/x/{key}/*`. wick starts it at boot, restarts it when it
crashes, checks who may reach each path, and forwards the request over a unix
socket. The plugin is plain `net/http`: register handlers on a mux, and wick
does the rest.

```go
package main

import "github.com/yogasw/wick/pkg/service"

func main() {
	service.ServeService(Module())
}
```

## When to use a service

| You need… | Use |
|---|---|
| Operations an agent or workflow calls (an API wrapper) | [connector](./authoring-connector.md) |
| A page inside wick for signed-in users | [tool](./authoring-tool.md) |
| Work on a schedule that returns a result | [job](./authoring-job.md) |
| An endpoint the outside world calls at any time (webhook, public or token API, A2A agent), streaming responses, or state kept in memory between requests | **service** |

Tools and connectors are spawned on demand and stopped when idle; a job lives
for one run. A service is the only kind that is always running and the only
one that can serve requests without a wick login.

## Quick start

From the folder that holds `connector/ tool/ job/ service/` (the `plugins/`
module in the wick repo, or a plugins repo laid out the same way):

```bash
cp -r service/_template service/my_service
# edit service/my_service/service.go: Meta.Key = "my_service", Routes, handlers
# set service/my_service/VERSION, e.g. 0.1.0
wick plugin build --kind service my_service --target linux/amd64
```

The template (`plugins/service/_template/service.go`) is a complete service:

```go
func Module() service.Module {
	return service.Module{
		Meta: service.Meta{Key: "my_service", Name: "My Service", Description: "What this service does.", Icon: "🛰️"},
		Routes: []service.Route{
			{Prefix: "/hook", Auth: service.Public},
			{Prefix: "/api", Auth: service.Token},
			{Prefix: "/", Auth: service.Session},
		},
		Configs: []entity.Config{{Key: "greeting", Value: "hello", Description: "Text the API answers with"}},
		Register: func(mux *http.ServeMux, env *service.Env) {
			mux.HandleFunc("POST /hook", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
			mux.HandleFunc("GET /api/hello", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"message": env.Cfg("greeting")})
			})
		},
	}
}
```

Install the zip (see [Packaging and install](#packaging-and-install)), then:

```bash
curl -X POST https://<wick>/x/my_service/hook                         # public
curl -H "Authorization: Bearer <token>" https://<wick>/x/my_service/api/hello
```

`plugins/service/example_a2a_repeater` is a fuller example: an A2A agent that
is also a Team remote source. The [A2A repeater walkthrough](./service-a2a.md)
builds it step by step.

## Module reference

`service.Module` (`pkg/service`):

| Field | Meaning |
|---|---|
| `Meta` | `Key`, `Name`, `Description`, `Icon`. `Key` must equal the plugin's folder name; lowercase letters, digits, `_` and `-` (not first or last), max 64 characters. Use `_` for new plugins. |
| `Routes` | path prefixes and who may reach them — see [Routes and auth](#routes-and-auth) |
| `Configs` | settings edited on the plugin's admin page (`[]entity.Config`, or `entity.StructToConfigs(Config{})` from a tagged struct). Secret fields are stored encrypted. |
| `CallbackScopes` | what the plugin may call back into wick for — see [Calling wick back](#calling-wick-back) |
| `Register(mux, env)` | mount handlers on a plain `*http.ServeMux`. Paths are relative to `/x/{key}`. |
| `RemoteSource` | optional: make the plugin a Team remote-agent source — see [Team remote source](#team-remote-source) |

Other entry points in `pkg/service`:

| Function | Use |
|---|---|
| `ServeService(mod)` | the whole `main()`. With `--dump-manifest` it prints the `plugin.json` half and exits (that is how `wick plugin build` writes the manifest). |
| `Handler(mod, cfg)` | the plugin's `http.Handler` with config applied, for tests |
| `Manifest(mod)` | the manifest `service` section, for tests |
| `BaseURL(r)` | the public URL of the service, rebuilt from a proxied request |

## Routes and auth

wick strips `/x/{key}` and matches the rest of the path against `Routes` by
**longest prefix**. A path that no route covers answers 404 without reaching
the plugin, so every handler you register needs a route that covers it.

| Mode | Manifest value | Who passes |
|---|---|---|
| `service.Public` | `public` | anyone — webhooks, A2A agent cards |
| `service.Token` | `token` | `Authorization: Bearer <token>` with an access token created on the plugin's admin page. wick checks it and removes the header, so the plugin never sees it. A missing or wrong token gets 401. |
| `service.Session` | `wick-session` | a signed-in wick user; anyone else gets 401 |

```go
Routes: []service.Route{
	{Prefix: "/.well-known/", Auth: service.Public}, // agent card
	{Prefix: "/hook", Auth: service.Public},         // webhook receiver
	{Prefix: "/api", Auth: service.Token},           // machine clients
	{Prefix: "/", Auth: service.Session},            // everything else: wick users
},
```

With these routes `/x/my_service/api/hello` matches `/api` (token) and
`/x/my_service/settings` matches `/` (session).

Headers wick sets on the forwarded request (any `X-Wick-*` header sent by the
client is dropped first):

| Header | When |
|---|---|
| `X-Wick-Base` | always: the mount path, `/x/{key}` |
| `X-Wick-User-Id`, `X-Wick-User-Email`, `X-Wick-User-Name`, `X-Wick-User-Role` | session routes. Role is `admin` or `user`. |

Read the user with `r.Header.Get("X-Wick-User-Email")`. `service.BaseURL(r)`
combines `X-Forwarded-Proto`, `X-Forwarded-Host` and `X-Wick-Base` into an
absolute URL. Use it for links you hand out, such as the `url` in an agent
card.

Responses are flushed on every write, so Server-Sent Events stream through the
proxy. Paths under `/_wick/` are wick-only RPC and answer 404 from outside.

## Env and config

`Register` receives a `*service.Env`:

| Method | Returns |
|---|---|
| `env.Key()` | the plugin key |
| `env.Cfg(k)` | the current value of config `k` (`""` if unset) |
| `env.Callback()` | wick's base URL and the plugin's callback token |
| `env.CallbackRequest(ctx, method, path, body)` | a request to wick carrying the callback token |

wick pushes config to the plugin right after every start and again whenever an
admin saves the Configuration card. The new values apply without a restart, so
read `env.Cfg` per request instead of copying it at startup. If the plugin
rejects a pushed config, wick restarts it.

On the admin page, secret values are shown masked. Saving the mask, or leaving
a secret empty, keeps the stored value.

## Calling wick back

Each time wick starts the plugin it sets two environment variables:

| Variable | Value |
|---|---|
| `WICK_BASE_URL` | wick's public base URL |
| `WICK_PLUGIN_TOKEN` | a callback token for this process |

`env.Callback()` reads both; `env.CallbackRequest` builds an authorized
request:

```go
req, err := env.CallbackRequest(ctx, http.MethodGet, "/x/-/api/whoami", nil)
```

The callback API lives under `/x/-/api/`. Today it has one endpoint,
`GET /x/-/api/whoami`, which answers `{"plugin": "<key>", "scopes": [...]}`
and needs no scope. `CallbackScopes` lists the scopes the plugin's token
carries for endpoints added later; a call to an endpoint whose scope is not
listed gets 403.

The callback token lives only in memory and is replaced on every start. An
admin can revoke it from the **Callback to wick** card; while revoked the
plugin starts without `WICK_PLUGIN_TOKEN` until callback is allowed again.
Never log the token.

## Team remote source

Set `Module.RemoteSource` to make the plugin a source in the Team *Remote
agent* wizard (source **Plugin**):

```go
type RemoteSource interface {
	Send(ctx context.Context, turn RemoteTurn) (RemoteSendResult, error)
	Receive(ctx context.Context, handle string) (<-chan RemoteEvent, error)
	Done(handle string)
}
```

- `Send` gets the turn (`Text`, `SessionID`, `ContextID`) and returns a
  `Handle` plus the `ContextID` to keep. wick stores the context id per
  session and sends it back on the next turn, so a remote conversation keeps
  its thread.
- `Receive` streams events for the handle. Event kinds: `text_delta`, `text`,
  `status`, `attachment`, `done`, `error`. Close the channel after `done` or
  `error`.
- `Done` releases what `Send` set up. wick calls it when the turn ends or is
  cancelled.
- Optionally implement `Describe() (name, detail string)` to name the agent in
  the wizard.

wick calls these over the plugin's internal `/_wick/remote/` paths; you never
register them yourself. For an A2A agent, `a2aservice.Mount(mux, card, reply)`
(`pkg/service/a2aservice`) serves the agent card at `/.well-known/agent.json`
and `/.well-known/agent-card.json` and the JSON-RPC endpoint at `POST /`. The
[A2A repeater walkthrough](./service-a2a.md) uses both.

## Lifecycle and supervision

- wick loads every enabled service plugin at boot and starts it. A plugin that
  is disabled, has no `service` section, or fails manifest verification is
  skipped.
- A crashed plugin is restarted with backoff: 1 s, doubling up to 30 s. The
  backoff resets once the plugin has stayed up for a minute.
- Stop sends SIGTERM and kills the process after 5 s. Handle SIGTERM and keep
  startup fast.
- The state is one of `stopped`, `starting`, `running`, `backoff`. While the
  plugin is not running, requests get 503; if it does not answer, 502.
- The last 200 lines of the plugin's output are kept for the admin page.

## Testing

Unit-test the handlers without wick:

```go
func TestHello(t *testing.T) {
	h := service.Handler(Module(), map[string]string{"greeting": "hi"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hello", nil))
	if !strings.Contains(rec.Body.String(), "hi") {
		t.Fatalf("got %s", rec.Body.String())
	}
}
```

`service.Handler` does **not** enforce route auth — that happens in wick — so
check `Routes` separately (for example against `service.Manifest(Module())`).

```bash
cd plugins && go test ./service/...
```

`plugins/service/example_a2a_repeater/main_test.go` tests the A2A and
`RemoteSource` paths. Inside the wick repo,
`internal/services/plugin/plugintest` builds that example and runs it under a
real host (`plugintest.StartRepeater(t)`, skipped with `-short`).

## Packaging and install

```bash
wick plugin build --kind service my_service --target linux/amd64   # or --all
```

This writes `bin/my_service-<version>-linux-amd64.zip` with the binary and a
`plugin.json` generated from the binary. Build for the OS and architecture
wick runs on. Signing and releasing are covered in
[Building and releasing](./building.md).

Install the zip in one of two ways:

- **Admin → Plugins → Add new plugin**: upload the zip, or add a link or a
  GitHub release as a plugin source ([Plugin sources](./sources.md)).
- the app CLI: `<app> plugin install bin/my_service-0.1.0-linux-amd64.zip`.

Installing through **Add new plugin** (upload, link or source) loads the
service from its manifest and starts it right away — no wick reload. An
update stops the old process and starts the new binary with the new
`Routes`, `Configs` and `CallbackScopes`. The `<app> plugin install` CLI
only writes the files: the running wick picks the service up on its next
reload, and until then the Installed tab shows `not loaded (reload wick)`.

## Admin page

Services are listed in the **Service plugins** section of the manager
Connectors page; each opens at `/services/{key}`. The page has:

| Card | Content |
|---|---|
| status bar | state, pid, restarts, last error; Start / Stop / Restart |
| Configuration | the `Configs` fields; Save pushes them to the running plugin |
| Routes | each prefix with its auth mode and the full URL |
| Access tokens | create, rotate, revoke tokens for `token` routes. The token is shown once. |
| Callback to wick | callback token state and scopes; Revoke / Allow |
| Log | the last 200 output lines |

The same actions are available to admins over the API:

```
GET    /manager/api/service-plugins
GET    /manager/api/service-plugins/{key}
POST   /manager/api/service-plugins/{key}/config          {"values": {...}}
POST   /manager/api/service-plugins/{key}/start|stop|restart
POST   /manager/api/service-plugins/{key}/callback-revoke|callback-allow
POST   /manager/api/service-plugins/{key}/tokens          {"name": "..."}
POST   /manager/api/service-plugins/{key}/tokens/{id}/rotate
DELETE /manager/api/service-plugins/{key}/tokens/{id}
```

Access tokens start with `wick_svc_` and are stored hashed; callback tokens
start with `wick_plg_`.

## Troubleshooting

| Symptom | Cause |
|---|---|
| 404 on `/x/{key}/...` | no route prefix covers the path, the plugin is not loaded (installed with the CLI and wick not reloaded yet, disabled, failed verification), or the path is under `/_wick/` |
| 401 `invalid or missing token` | `token` route without a valid `Authorization: Bearer` access token for this plugin |
| 401 `sign in to wick first` | `wick-session` route called without a wick login |
| 503 `Service "…" is not running` | the plugin is stopped or in backoff; read the Log card |
| 502 `Service "…" did not answer` | the plugin is up but the request failed (crash, closed connection) |
| Config change has no effect | the handler copied `env.Cfg` at startup instead of reading it per request |
| Callback gets 401 | callback was revoked on the admin page, or the token from a previous process was cached |
| State keeps returning to `backoff` | the plugin exits on start; the Log card shows its last lines |
