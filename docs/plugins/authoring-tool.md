# Authoring a tool plugin

A tool plugin runs an existing `tool.Module` (the same `Register(r
tool.Router)` a built-in tool uses) as its own process. Only `main.go` is new.

```go
package main

import (
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/plugin/toolplugin"
	"github.com/yogasw/wick/pkg/tool"

	textcounter "example.com/yourtools/text-counter"
)

func main() {
	toolplugin.ServeTool(tool.Module{
		Meta: tool.Tool{Key: "text_counter", Name: "Text Counter", Icon: "#",
			Category: "Text", DefaultVisibility: entity.VisibilityPublic},
		Configs:  entity.StructToConfigs(textcounter.Config{InitText: "hello world"}),
		Register: textcounter.Register,
	})
}
```

## How the host runs it

- The first request to `/tools/{key}` spawns the plugin (single-flight) and
  waits up to 10 s for it to be ready; after that the user gets a `503`.
  Measured cold start for small Go tools: ~40–50 ms.
- Every request is proxied over the plugin's unix socket. The host keeps the
  login and access check (`RequireToolAccess`) and adds `X-Wick-User-Id`,
  `X-Wick-User-Role`, `X-Wick-User-Tags` and `X-Wick-Base`. Incoming
  `X-Wick-*` headers from the browser are stripped, so the plugin can trust
  them.
- `c.HTML` pages are wrapped in the wick layout; JSON and HTMX fragments pass
  through unchanged. Static files from `r.Static` are proxied too.
- Webhook routes (`WebhookGroup`) are reachable at `/tools/{key}/webhook/*`
  without a wick session, exactly like a built-in tool; the manifest lists
  them so the host knows which paths are public.
- A webhook request that carries a wick credential reaches the plugin with
  the identity the host verified as `X-Wick-Token-User-Id`, `-Email`,
  `-Name`, `-Role`, `-Tags` and `X-Wick-Token-User-Source`. In the handler,
  `c.TokenUser()` returns the person (`pat` or `sso`), `c.TokenCaller()`
  also returns wick's `system` identity, and
  `toolplugin.TokenUserFromHeaders` reads the headers directly. A PAT is
  checked first; an invalid bearer does not hide a valid JWT.
  - `pat` — `Authorization: Bearer <wick Personal Access Token>`.
  - `sso` — wick's own SSO JWT in `X-Wick-User`, sent by a custom MCP
    connector with the `sso` scheme whose URL is this tool on this wick.
    The host checks:
    - the signature against wick's SSO key (cached in memory; verifying
      never touches the database);
    - `iat`/`exp` (30 s skew) and a lifetime of at most 15 minutes;
    - `iss` = the base URL wick mints with;
    - `aud` = one of wick's own hosts: the base URL's host, or
      `127.0.0.1` / `localhost` / `[::1]` on wick's own listen port;
    - `wick_url` — the URL wick actually sent the token to, set by wick and
      not editable — on one of those hosts and under this tool's
      `/tools/{key}/`;
    - the subject is an approved user.

    `aud` alone proves nothing: whoever registers a connector chooses its
    audience. `wick_url` is what binds the token. A token wick sent to
    another server names that server, and a token for `/tools/a` names
    `/tools/a`, so neither works on this tool. Tokens without `wick_url`
    are refused. A connector that reaches wick by a LAN IP or internal
    hostname is refused too (fail-closed): register it at the base URL or
    at loopback on wick's port.
  - `system` — the same JWT minted for wick itself (subject `system:wick`),
    used to list the tool's MCP tools at boot or reload: ID `system:wick`,
    role `system`, no email. The host cannot tell which MCP method it
    calls, so **treat `system` as read-only**. `c.TokenUser()` returns false
    for it. The plugin's Host API calls for that request run without a user
    (plugin calls, else `wick_pat`).

  Client-sent `X-Wick-*` headers, including `X-Wick-Token-User-*` and the
  raw `X-Wick-User`, are always dropped first. Host API calls made while
  serving a `pat` or `sso` request run as that user. Check `User.Source`
  when a PAT and an SSO call deserve different rights.
- The process is idle-killed. It is never killed while a request (including
  an SSE stream) is in flight.
- Webhooks that must answer within a few seconds (e.g. Slack `trigger_id`):
  `toolplugin.ServeTool(mod, toolplugin.KeepWarm())` keeps the process alive.

## Template, build, test

```bash
cp -r tool/_template tool/<key> && echo 0.1.0 > tool/<key>/VERSION
wick plugin build --kind tool <key> --target linux/amd64
<app> plugin install bin/<key>-0.1.0-linux-amd64.zip
```

The tool then shows on the home grid with a "plugin" badge.

## Calling wick (Host API)

A handler can call back into wick with `toolplugin.Host(c.Context())`; calls run as the user behind the request. The sample connector `plugins/connector/httpbin` (op `whoami_host`) shows the calls end to end. See [Host API](./host-api.md) for the scopes, the `host_scopes` /
`host_resources` manifest keys (declared with
`wickplugin.DeclareHostAccess` in `main`) and who each call runs as.

## Moving a built-in tool

1. Keep the tool package as is.
2. Write the `main.go` above in your plugins repo (key with `_`).
3. Remove the `app.RegisterTool(...)` line from the app's `main.go` once the
   plugin is installed. Users' bookmarks change from `/tools/a-b` to
   `/tools/a_b`.
