# Authoring a service plugin

A service plugin is an always-on HTTP server that wick exposes at
`/x/{key}/*`. Use it when something outside wick must reach your code at any
time (an A2A agent, a webhook relay) or when it keeps state between requests.

```go
package main

import "github.com/yogasw/wick/pkg/service"

func main() {
	service.ServeService(Module())
}
```

`service.Module`:

| Field | Meaning |
|---|---|
| `Meta` | key (`_`, no `-`), name, description |
| `Routes` | path prefixes and who may reach them (below) |
| `Configs` | settings shown on the plugin's admin page (secrets encrypted) |
| `CallbackScopes` | what the plugin may call back into wick for |
| `Register(mux, env)` | mount handlers on a plain `http.ServeMux` |
| `RemoteSource` | optional: make the plugin a Team remote-agent source |

## Route auth

| Mode | Who passes |
|---|---|
| `service.Public` | anyone — webhooks, A2A agent cards |
| `service.Token` | `Authorization: Bearer <token>`; the token is generated on the plugin's admin page and checked by wick (stored hashed, can be rotated or revoked). The plugin never sees it. |
| `service.Session` | a signed-in wick user; identity arrives as `X-Wick-User-*` headers |

## Lifecycle

- Started when wick starts (and after a reload), stopped on shutdown.
- On crash: restarted with backoff from 1 s up to 30 s.
- The admin page (**Manager → Services → {key}**) shows status, the last 200
  log lines, config, the access token, and Stop/Start/Restart.
- SSE responses are flushed per event through the proxy.

## Calling wick back

`env.Callback()` gives `WICK_BASE_URL` and `WICK_PLUGIN_TOKEN` (scoped to
`CallbackScopes`, revocable from the admin page). Never log the token.

## Team remote source

Set `Module.RemoteSource` (`Send` / `Receive` / `Done`, event schema v1) and
the plugin shows up as source **Plugin** in the Team *Remote agent* wizard.
That agent can then be exposed again through the A2A/REST connection like any
other agent.

## Template, build, test

```bash
cp -r service/_template service/<key>        # VERSION already included
wick plugin build --kind service <key> --target linux/amd64
<app> plugin install bin/<key>-0.1.0-linux-amd64.zip
curl http://localhost:<port>/x/<key>/...
```

See [A2A repeater walkthrough](./service-a2a.md) for a complete example.
