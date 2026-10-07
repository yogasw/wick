---
name: wick-plugin-authoring
description: Use when the user asks you to BUILD a wick plugin — write the code for a new connector, tool, job, or service plugin, wrap existing code as one, or turn an HTTP app, webhook receiver or remote agent into a service plugin. Covers picking the kind, starting from plugins/<kind>/_template, the Module contract and main.go entry per kind, testing without wick, `wick plugin build` packaging, installing through Add new plugin, verifying, and service specifics (routes and auth, config, callback, RemoteSource for Team). For installing, updating or troubleshooting an existing plugin use wick-plugins instead.
---

# Building a wick plugin

A plugin is a Go `package main` that wraps ONE module value and calls the kind's serve function. wick runs the binary in its own process. Using plugins (sources, Add new plugin, update, disable, troubleshooting) is covered by the **wick-plugins** skill — read it for anything past "the zip is built".

## 1. Pick the kind

| The user wants… | Kind | Lifetime |
|---|---|---|
| Operations an agent or workflow calls (an API wrapper) | **connector** | spawned on demand, idle-killed |
| A page in wick, under `/tools/{key}`, for signed-in users | **tool** | spawned on first request, idle-killed |
| Something that runs on a schedule and returns a markdown result | **job** | spawned per run, killed when `Run` returns |
| An always-on HTTP server: webhooks, a public API, SSE, a remote agent | **service** | always on, restarted on crash; may sleep while idle when it declares `AutoOff` (see wick-plugin-service) |

Pick the lightest kind that fits. A service is the only kind that keeps in-memory state between requests and the only one reachable without a wick login.

## 2. Start from the template

Work in a folder that holds `connector/ tool/ job/ service/` (the wick repo's `plugins/` nested module, or a plugins repo laid out the same way). Copy the template; folders starting with `_` are skipped by the tooling.

```bash
cp -r service/_template service/my_service   # or connector/, tool/, job/
```

Then:

- Set `Meta.Key` to the **folder name**. `wick plugin build` fails if they differ. Keys are lowercase letters, digits, `_` and `-` (not first or last), max 64 characters; use `_` for new plugins.
- Set `VERSION` (e.g. `0.1.0`). It is the plugin version.
- Edit the module file (`connector.go`, `tool.go`, `job.go` or `service.go`). `main.go` normally stays as is.

## 3. The contract per kind

| Kind | Module | `main.go` |
|---|---|---|
| connector | `connector.Module` (`pkg/connector`): `Meta`, `Configs`, `Operations` | `wickplugin.Serve(Module())` |
| tool | `tool.Module` (`pkg/tool`): `Meta tool.Tool`, `Configs`, `Register func(r tool.Router)` | `toolplugin.ServeTool(Module())` |
| job | `job.Module` (`pkg/job`): `Meta job.Meta` (incl. `DefaultCron`), `Configs`, `Run func(ctx) (string, error)` | `wickplugin.ServeJob(Module())` |
| service | `service.Module` (`pkg/service`): `Meta`, `Routes`, `Configs`, `CallbackScopes`, `Register func(mux *http.ServeMux, env *service.Env)`, `RemoteSource` | `service.ServeService(Module())` |

Config fields come from a struct with `wick:"..."` tags via `entity.StructToConfigs(Config{})` (`desc=…`, `secret`, `required`, `url`; the field name becomes the snake_case key). Admins edit them in wick; never hard-code credentials.

- **connector** — one input struct per operation; mark writes as destructive. Read config from the op's `Ctx`.
- **tool** — same `tool.Router` as a built-in tool (`r.GET("/", …)`, `c.HTML`, `c.Cfg`, `c.User()`). wick wraps `c.HTML` pages in its layout.
- **job** — read config with `job.FromContext(ctx).Cfg("key")`, log progress with `job.Logf(ctx, …)` (it lands in the run history), return markdown. Honour `ctx`; nothing survives between runs.
- **service** — see section 6.

## 4. Test without wick

```bash
cd plugins && go test ./service/my_service/   # or ./tool/..., ./job/..., ./connector/...
```

- tool: `toolplugin.Handler(Module(), cfg)` returns the `http.Handler`; drive it with `httptest`.
- job: call `Run(context.Background())` directly — a bare context gives a no-op Ctx whose `Cfg` returns `""`.
- service: `service.Handler(Module(), cfg)` returns the handler with config applied. It does **no** route auth, so test auth in wick itself.

`plugins/tool/example_counter` and `plugins/service/example_a2a_repeater` have `main_test.go` files to copy from.

## 5. Build, install, verify

```bash
wick plugin build --kind service my_service --target linux/amd64   # --kind defaults to connector
wick plugin build --kind tool my_tool --all                         # every OS/arch
```

The zip lands in `bin/` (`--output`) as `<key>-<version>-<os>-<arch>.zip`, holding the binary and a `plugin.json` the build generates by running the binary with `--dump-manifest`. Never hand-edit `plugin.json`. Build for the OS/arch wick runs on; signing flags (`--sign-key`, `--cosign-key`) and releases are in **wick-plugins**.

Install: Admin → Plugins → **Add new plugin** → upload the zip (or add a link / GitHub release source). wick verifies the manifest (os/arch, protocol version, sha256, signature if a trusted key is set) before installing.

Verify:

- connector: it appears in the connector list; add an instance, fill config, run an op.
- tool: open `/tools/{key}`.
- job: open it on the Jobs page, fill config, **Run now**, read the run result.
- service: on the manager Connectors page, open it under **Service plugins** (detail page `/services/{key}`). State should be `running`; the Log card shows the plugin's stderr. Fill the Configuration card; call `/x/{key}/…`.

## 6. Service specifics

The full service reference (headers, callback API, admin API, Team remote agent, testing, pitfalls) is the **wick-plugin-service** skill; the essentials:

**Routes and auth.** wick mounts the service at `/x/{key}/*`, strips that prefix, and matches the rest against `Routes` by **longest prefix**. A path no route covers is 404. Register handlers on the same paths:

```go
Routes: []service.Route{
    {Prefix: "/hook", Auth: service.Public},   // anyone: webhooks, agent cards
    {Prefix: "/api", Auth: service.Token},     // Authorization: Bearer <token>
    {Prefix: "/", Auth: service.Session},      // a signed-in wick user
},
```

- `Token`: an admin creates the token in the **Access tokens** card on the service page. wick checks it and strips the `Authorization` header; the plugin never sees it.
- `Session`: wick checks the login and adds `X-Wick-User-Id`, `-Email`, `-Name`, `-Role`.
- Every request gets `X-Wick-Base` (the mount path `/x/{key}`); `service.BaseURL(r)` turns it into an absolute URL for links you hand out. Client-sent `X-Wick-*` headers are dropped.
- Paths under `/_wick/` are internal and answer 404 from outside.

**Config.** `env.Cfg("key")` reads the current value. wick pushes config after each start and again on every save, without a restart — read it per request, do not copy it at startup.

**Callback.** `env.Callback()` returns the wick base URL and a per-process token; `env.CallbackRequest(ctx, method, path, body)` builds an authorized request. Today the only callback endpoint is `GET /x/-/api/whoami`. Never log the token.

**Lifecycle.** wick starts every enabled service at boot and restarts a crashed one with backoff (1 s doubling to 30 s; reset after a minute of uptime). Stop sends SIGTERM and kills after 5 s. Keep startup fast and handle SIGTERM. Installing or updating a service loads its new manifest and starts it right away — no wick reload.

**RemoteSource (Team remote agent).** Set `Module.RemoteSource` to a value implementing `Send(ctx, service.RemoteTurn) (service.RemoteSendResult, error)`, `Receive(ctx, handle) (<-chan service.RemoteEvent, error)` and `Done(handle)`. Optionally implement `Describe() (name, detail string)` to name the agent in the wizard. The plugin then shows up as a **Plugin** source when adding a Team remote agent. Event kinds are `text_delta`, `text`, `status`, `attachment`, `done`, `error`; close the channel after `done` or `error`. Return `ContextID` from `Send` to keep a conversation thread; wick sends it back on the next turn. For an A2A agent, `a2aservice.Mount(mux, card, reply)` (`pkg/service/a2aservice`) serves the agent card and JSON-RPC endpoint — `plugins/service/example_a2a_repeater` does both.

## Pitfalls

- `Meta.Key` ≠ folder name → build fails. Changing the key later makes it a different plugin.
- Building for the wrong OS/arch → install refuses with "has no build for …".
- A service route without a matching `Routes` prefix is unreachable (404), and a `Public` prefix exposes everything under it — keep public prefixes narrow.
- `wickplugin.DataDir` only resolves a persistent directory for connectors; a tool, job or service gets a temp directory, so keep durable state in a configured path or an external store.
- A job keeps nothing in memory between runs; a tool may be stopped when idle. Use a service when state must live in the process.
