# Host API

The Host API (`wick.host.v1`) is how a plugin calls back **into** wick: look
up users, read sessions and tickets, run another connector's operation, read
a config value, and — as a safety net — call wick's REST API or its
database. Every call is checked against what the plugin declared in its
manifest and what an admin allowed, runs as a real person, and lands in the
audit log.

It is optional. A plugin that never calls it behaves exactly as before, and
a plugin that does call it degrades cleanly on a wick that predates it
([Compatibility](#compatibility)).

## Two doors, one API

| Door | For | Transport | How the plugin finds it |
|---|---|---|---|
| **gRPC broker** | connector, tool, job | gRPC over the go-plugin `GRPCBroker` connection wick already holds to the process | `WICK_HOST_API_BROKER=1` in the env; the Go SDK dials it by itself |
| **HTTP door** | every kind, service included (and any non-Go plugin) | plain JSON over HTTP, `Authorization: Bearer $WICK_PLUGIN_TOKEN` | `WICK_HOST_API_URL` (private loopback address); `WICK_BASE_URL` is only the fallback on a wick that sets no `WICK_HOST_API_URL` |

Both doors are served by the same server, so permissions, identity, audit
and limits are identical. The HTTP door lives under `/x/-/api/` (the
callback mount; `-` is never a plugin key). `WICK_PLUGIN_TOKEN` is issued
per process (`wick_hst_…`; a service gets its callback token `wick_plg_…`), lives only in memory and is revoked when the
process stops or an admin revokes the plugin.

**Call id.** wick stamps every request it sends a plugin (a connector
`Execute`, a proxied tool or service request) with a call id —
`X-Wick-Call-Id` on HTTP, `wick-call-id` gRPC metadata — and remembers who
that request runs for. The plugin echoes the id on its Host API calls and
wick maps it back to the person. The id is only a lookup key: it dies when
the request ends and an id given to another plugin is ignored, so it cannot
be used to borrow an identity. The Go SDK carries it for you as long as you
pass the request's context (`c.Context()`, `r.Context()`); a non-Go plugin
copies the `X-Wick-Call-Id` header of the incoming request onto its calls.

## Scopes

A plugin lists the scopes it uses in `host_scopes`. **Default** is the
decision when no admin has touched the scope; *declaration* is the
`host_resources` block the scope cannot work without.

| Scope | RPC (HTTP door) | Default | Marked | Needs declaration |
|---|---|---|---|---|
| `whoami` | `Whoami` (`GET /api/whoami`), `ListMethods` (`GET /api/methods`) | allow | – | – |
| `users.read` | `ListUsers` (`GET /api/users`), `GetUser` (`GET /api/users/{id}`), `GetUserByEmail` (`GET /api/users/by-email/{email}`) | allow | – | – |
| `accounts.read` | `ConnectedAccounts` (`GET /api/accounts`) | allow | – | – |
| `sessions.read` | `ListSessions` (`GET /api/sessions`), `GetSessionMeta` (`GET /api/sessions/{id}`) | allow | – | – |
| `sessions.notify` | `SendToSession` (`POST /api/sessions/{session_id}/send`) | allow | destructive | – |
| `tickets.read` | `GetTicket` (`GET /api/tickets/{id}`), `ListTickets` (`GET /api/tickets`) | allow | – | – |
| `tickets.write` | `CreateTicket` (`POST /api/tickets`), `UpdateTicket` (`PATCH /api/tickets/{id}`), `AttachSession` (`POST /api/tickets/{ticket_id}/sessions`) | allow | destructive | – |
| `config.read_other` | `GetConfig` (`GET /api/config`) for another owner's value; secrets stay empty | allow | – | – |
| `config.read_secret` | `GetConfig` for another owner's **secret** (needs `config.read_other` too) | **OFF** | sensitive | `config` |
| `connectors.read` | `ListConnectorInstances` (`GET /api/connectors`) | allow | – | – |
| `connectors.execute` | `ExecuteConnector` (`POST /api/connectors/execute`) | allow | per op (follows the op's destructive flag) | `connectors` |
| `rest.read` | `Rest` with GET/HEAD (`ANY /api/rest/<wick path>`) | allow | – | `rest` |
| `rest.write` | `Rest` with POST/PUT/PATCH/DELETE | **OFF** | destructive | `rest` |
| `db.read` | `Query` (`POST /api/query`, `write=false`) | **OFF** | – | `db` |
| `db.write` | `Query` with `write=true` | **OFF** | destructive | `db` |
| `datadir` | none — declarative: keeps a persistent data folder | allow | – | – |
| `net.listen` | none — declarative: opens a local port | allow | – | – |

`Invoke` (`POST /api/invoke`) calls a method by name and uses **that
method's** scope; there is no separate "invoke" scope. `ListMethods` lists
every RPC with its scope.

Notes that matter in practice:

- A plugin's **own** config never needs a scope: `GetConfig` with an empty
  owner (or its own) just works.
- `wick_pat` is never returned to anyone, not even the plugin that owns it;
  `GetConfig` reports it `found`, `secret`, empty. wick resolves it itself.
- An **unknown** scope (a plugin built for a newer wick) does not fail the
  install: wick logs it, the admin page shows it as `unknown`, and every
  call under it is denied.
- A scope the plugin uses but did not declare shows as `undeclared` on the
  admin page.

## Declaring access in the manifest

`host_scopes` and `host_resources` sit at the top level of `plugin.json`:

```json
{
  "kind": "job",
  "host_scopes": [
    "users.read", "tickets.write",
    "connectors.execute", "config.read_other", "config.read_secret",
    "rest.read", "db.read"
  ],
  "host_resources": {
    "connectors": [
      { "connector": "slack", "ops": ["lookup_user_by_email", "get_user_info"] }
    ],
    "config": [
      { "owner": "job:notion-ticket-sync", "key": "token" }
    ],
    "rest": [
      { "method": "GET", "path": "/api/tickets/*" },
      { "method": "GET", "path": "/api/sessions" }
    ],
    "db": [
      { "table": "tickets", "mode": "read" }
    ]
  }
}
```

A Go plugin does not write this by hand: call
`wickplugin.DeclareHostAccess` in `main` before `Serve` and
`--dump-manifest` / `wick plugin build` put it in `plugin.json` (an invalid
declaration fails the build, not the install):

```go
func main() {
	wickplugin.DeclareHostAccess(
		[]string{wickplugin.ScopeHostUsersRead, wickplugin.ScopeHostConnectorsExec},
		&wickplugin.HostResources{Connectors: []wickplugin.HostConnectorResource{
			{Connector: "slack", Ops: []string{"lookup_user_by_email"}},
		}},
	)
	wickplugin.Serve(Module())
}
```

A non-Go plugin writes the same two keys in the `plugin.json` it ships.

### Declare as narrowly as you can

**No declaration, no access** for `rest.*`, `db.*`, `connectors.execute` and
`config.read_secret` — the scope alone opens nothing. Each declaration
becomes an **item** the admin can block on its own:

| Kind | Declared as | Admin item | Rules |
|---|---|---|---|
| `connectors` | connector module key + the ops it may run | `<connector>.<op>`, e.g. `slack.send_message` | an op outside the list is `PermissionDenied` (`not declared in host_resources.connectors`); which **instance** runs is decided at runtime (one the acting user may see), so the same plugin works on every install |
| `rest` | `method` + `path` pattern | `<METHOD> <pattern>`, e.g. `GET /api/tickets/*` | path must start with `/api/` or `/manager/api/`; `*` = exactly one non-empty segment, `**` only as the last segment (one or more); HEAD counts as GET; no `?`, `#`, `%`, `\` or dot segments; plugin-management paths (`/manager/api/` + a segment containing `plugin`) and wildcards right under `/manager/api/` cannot be declared |
| `db` | `table` + `mode` (`read` or `write`) | `<table>:read`, `<table>:write` (a `write` declaration yields both) | the SQL is parsed (fail closed): one statement, declared tables only, no DDL/`SET`/`COPY`/`EXPLAIN`, only allow-listed functions; a write also checks every table it **reads** under `db.read` |
| `config` | `owner` + `key` | `<owner>/<key>`, e.g. `job:notion-ticket-sync/token` | one exact value per entry — there is **no wildcard for secrets**; the audit records the item, never the value |

Name the exact connector op, table, path or config key you need. A broad
pattern (`GET /api/**`) is legal but is what an admin will refuse first.

## Identity: who a call runs as

RPCs fall into two groups:

- **Plugin-level** — `Whoami`, `ListMethods`, `ListUsers`, `GetUser`,
  `GetUserByEmail`, `ConnectedAccounts`, `GetConfig`. They touch no data
  owned by a person and need no identity.
- **User-level** — `ListSessions`, `GetSessionMeta`, `SendToSession`,
  `GetTicket`, `ListTickets`, `CreateTicket`, `UpdateTicket`,
  `AttachSession`, `ListConnectorInstances`, `ExecuteConnector`, `Rest`,
  `Query` (and `Invoke` of one of them). They **always** run as a person,
  with that person's rights:

| Order | Identity | Where it comes from |
|---|---|---|
| 1 | the person who opened or triggered the plugin (`source=caller`) | the call id of the request being served: the user behind a connector execute, a tool request (`X-Wick-User-Id`), or a triggered job |
| 2 | the owner of the PAT in the plugin's `wick_pat` config (`source=pat`) | an admin pastes a personal access token (usually of a bot or admin account) into the plugin's config |
| — | neither | `FailedPrecondition: no user identity — set wick_pat in the plugin config or run on behalf of a user` |

There is no synthetic `plugin:<key>` identity and no "as admin" mode: admin
rights come only from an admin's own PAT, and the audit shows whose. A
person who does not exist or is not approved is refused the same way. A call
id whose request has already ended is refused instead of silently falling
back to `wick_pat`.

**Service plugins** follow the route being served:

| Route `Auth` | Identity |
|---|---|
| `wick-session` (`service.Session`) | the signed-in user opening the route |
| `token` (`service.Token`) | the owner of the access token: the admin who generated or last rotated it (`X-Wick-Token-Owner-Id`); a token with no recorded owner falls back to `wick_pat` |
| `public` (`service.Public`), and background work outside any request | `wick_pat`; empty → refused |

A service that only calls plugin-level RPCs needs no identity at all.

## Errors

| Situation | gRPC code | HTTP door | Go SDK |
|---|---|---|---|
| scope not granted, item not declared or blocked, plugin revoked | `PermissionDenied` | `403` + `{"code","message","scope","item","plugin"}` | `*host.PermissionDeniedError` (`errors.Is(err, host.ErrPermissionDenied)`) |
| no identity / acting user unknown or not approved | `FailedPrecondition` | `412` | status error |
| this wick has no Host API, or not this RPC | `Unimplemented` | response without `X-Wick-Host-Api` | `host.ErrHostAPIUnavailable` |

Every HTTP door response carries `X-Wick-Host-Api: <version>`; a failed call
also carries `X-Wick-Host-Api-Error: <gRPC code name>`. For `Rest` the
proxied API's own status (404, 422…) is the result, not an error — only a
response carrying `X-Wick-Host-Api-Error` is a Host API failure.

## Using it from Go

`host.Host` (`github.com/yogasw/wick/pkg/plugin/host`) is one flat
interface, one method per RPC, each taking its request message (`nil` =
empty request), so a new proto field never changes a signature.

**Connector** — names need nothing: `c.UserName(id)` resolves through
`GetUser` automatically (and returns `""` on a wick without the Host API).
For anything else take the host with the op's context:

```go
func doWhoami(c *connector.Ctx) (any, error) {
	h, err := wickplugin.Host(c.Context())
	if errors.Is(err, host.ErrHostAPIUnavailable) {
		return nil, fmt.Errorf("this wick has no Host API")
	} else if err != nil {
		return nil, err
	}
	who, err := h.Whoami(c.Context())
	if err != nil {
		return nil, err
	}
	return map[string]any{"plugin": who.GetPluginKey(), "scopes": who.GetScopesAllowed(),
		"caller": c.UserName(c.CallerUserID())}, nil
}
```

**Tool** — `toolplugin.Host(ctx)` with the request context, so user-level
calls run for the person behind the request:

```go
r.GET("/api/me", func(c *tool.Ctx) {
	h, err := toolplugin.Host(c.Context())
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	u, err := h.GetUser(c.Context(), &hostpb.GetUserRequest{Id: c.R.Header.Get(wickplugin.HeaderUserID)})
	var pd *host.PermissionDeniedError
	if errors.As(err, &pd) {
		c.JSON(http.StatusForbidden, map[string]string{"scope": pd.Scope, "item": pd.Item})
		return
	}
	// ...
})
```

**Job** — the same `wickplugin.Host(ctx)` with the `Run` context. A job
fired by cron has no caller, so user-level calls run as `wick_pat`.

**Service** — `host.Default()` picks the HTTP door from the env (a service
has no broker). Pass `r.Context()`: the SDK wraps handlers with
`host.CallIDHandler`, so the call id of the route's request rides along.

```go
h, err := host.Default()
if errors.Is(err, host.ErrHostAPIUnavailable) { /* older wick: degrade */ }
users, err := h.ListUsers(r.Context(), &hostpb.ListUsersRequest{Q: "ad"})
```

On a wick that predates the Host API a service still holds a callback
token, so `host.Default()` succeeds and each RPC returns
`ErrHostAPIUnavailable`; `Whoami` answers with `HostApiVersion` 0. Use
`wickplugin.Host(ctx)` (which treats version 0 as unavailable) when that
matters.

Running another connector's op (declared as `slack` →
`lookup_user_by_email`):

```go
args, _ := json.Marshal(map[string]string{"email": "someone@example.com"})
res, err := h.ExecuteConnector(ctx, &hostpb.ExecuteConnectorRequest{
	Connector: "slack", Op: "lookup_user_by_email", ArgsJson: args,
})
```

## Using it over HTTP (any language)

The body codec is plain JSON: keys are the proto field names; a bytes field
named `X_json` travels inline as `X` (`args_json` → `"args": {...}`,
`result_json` → `"result"`); `QueryRequest.args` travels as `"params"`; GET
requests send fields as query parameters.

```bash
H="Authorization: Bearer $WICK_PLUGIN_TOKEN"
C="X-Wick-Call-Id: $CALL_ID"           # copied from the request you are serving

# who am I, what may I call (also returns the legacy "plugin"/"scopes" keys)
curl -s -H "$H" "$WICK_HOST_API_URL/x/-/api/whoami"

# users.read
curl -s -H "$H" "$WICK_HOST_API_URL/x/-/api/users?q=ad&limit=20"
curl -s -H "$H" "$WICK_HOST_API_URL/x/-/api/users/by-email/someone%40example.com"

# Invoke: any method by name, with that method's scope
curl -s -H "$H" -H "$C" -H 'Content-Type: application/json' \
  -d '{"method":"ListTickets","args":{"project_id":"p1"}}' \
  "$WICK_HOST_API_URL/x/-/api/invoke"

# ExecuteConnector (connectors.execute, declared op, as the caller)
curl -s -H "$H" -H "$C" -H 'Content-Type: application/json' \
  -d '{"connector":"slack","op":"lookup_user_by_email","args":{"email":"someone@example.com"}}' \
  "$WICK_HOST_API_URL/x/-/api/connectors/execute"

# Query (db.read; declared tables only; $1.. placeholders on Postgres)
curl -s -H "$H" -H "$C" -H 'Content-Type: application/json' \
  -d '{"sql":"SELECT id, title FROM tickets WHERE project_id = $1","params":["p1"],"max_rows":50}' \
  "$WICK_HOST_API_URL/x/-/api/query"

# Rest (rest.read): the wick path follows /api/rest verbatim, bodies pass through raw
curl -s -H "$H" -H "$C" "$WICK_HOST_API_URL/x/-/api/rest/api/tickets/42"
```

[POLYGLOT.md](https://github.com/yogasw/wick/blob/master/plugins/POLYGLOT.md)
covers the rest of a non-Go plugin (handshake, socket, manifest).

## Admin: Plugins → Host access

The **Host access** tab lists every plugin that declares a scope, used one in
the last 7 days, or was blocked/revoked. Per plugin it shows each scope with
its label, status, items and 24 h / 7 day call, failure and denial counts.

| Status | Meaning |
|---|---|
| `allowed` | calls go through (default or admin allowed) |
| `blocked` | an admin blocked the scope |
| `off-by-default` | a default-OFF scope (`db.*`, `rest.write`, `config.read_secret`) nobody allowed yet |
| `undeclared` | used but missing from the manifest |
| `unknown` | declared, but this wick does not know the scope; denied |

What an admin can do — every change applies at once, no plugin restart:

- **Allow / block a scope**, or reset it to its default. Declarative scopes
  (`datadir`, `net.listen`) have nothing to toggle.
- **Block one item** (a table, a path, a connector op, a config key) while
  the rest of the scope stays allowed. Destructive ops show in red.
- **Block all host access** — one switch that denies every scope of the
  plugin; the per-scope choices are kept for when it is lifted.
- **Revoke** — drops the plugin's process tokens and broker access over both
  doors, and keeps them dropped across restarts until **Restore** (the plugin's
  next process gets a token again).
- **Audit** — every call (plugin, RPC, scope, item, acting user and source,
  result code, duration) is kept for **7 days**; the tab lists the latest
  calls per plugin, with a failed-only filter. `Query` records its SQL and
  (capped) params; config items are recorded, never their values.

A manifest update that adds an item to a scope the admin already allowed
inherits that decision.

## Compatibility

- **Add-only.** New RPCs, new fields (new numbers), new scopes, new `Invoke`
  methods. Renaming, renumbering or removing is a `wick.host.v2` that runs
  next to v1. A guard test diffs the proto against a snapshot.
- **`ProtoVersion` does not change** for the Host API: installed plugins load
  without a rebuild.
- **Older wick:** no broker id, no token, or a door without
  `X-Wick-Host-Api` → `ErrHostAPIUnavailable`, never a panic;
  `c.UserName` returns `""` as before.
- **Newer plugin on older wick:** unknown scopes are logged and denied, the
  plugin still loads.
- The HTTP `GET /api/whoami` keeps the original `{"plugin","scopes"}` keys
  next to the new fields, so service plugins written against the old
  callback door keep working.

## Adding a scope

A scope (or RPC, resource kind, admin label) is added in **one** place and
documented in three; a guard test fails until all agree:

1. `pkg/plugin/hostscopes.go` — `HostScopes`, `HostMethods`,
   `HostResources` (and `host.proto` / `host.Routes` for a new RPC,
   add-only).
2. **This page** — the scope table, and the declaration table if it needs
   a resource.
3. The built-in skills `wick-plugin-authoring` (scope table) and
   `wick-plugins` (admin section), under
   `internal/agents/skillsync/builtin/`.
4. `go test ./pkg/plugin/ -run TestHostAPIDocsInSync` — it fails, naming the
   file, when a scope or RPC from step 1 is missing from step 2 or 3.
