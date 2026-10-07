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

## Moving a built-in tool

1. Keep the tool package as is.
2. Write the `main.go` above in your plugins repo (key with `_`).
3. Remove the `app.RegisterTool(...)` line from the app's `main.go` once the
   plugin is installed. Users' bookmarks change from `/tools/a-b` to
   `/tools/a_b`.
