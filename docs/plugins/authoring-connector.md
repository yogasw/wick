# Authoring a connector plugin

A connector plugin is the same `connector.Module` a built-in connector uses,
served over gRPC by `wickplugin.Serve`. The deeper reference for the module
itself is [Connector plugins](../guide/connector-plugins.md) and
[Connector module](../guide/connector-module.md).

## Start from the template

```bash
cd plugins                      # the folder holding connector/ tool/ job/ service/
cp -r connector/_template connector/<key>
echo 0.1.0 > connector/<key>/VERSION
```

- `main.go` calls `wickplugin.Serve(Module())` — leave it.
- `connector.go` holds `Meta` (`Key` = folder name, `_` not `-`), `Configs`
  (credentials, encrypted by wick) and the operations.

## Build and inspect

```bash
wick plugin build --kind connector <key> --target linux/amd64
# → bin/<key>-0.1.0-linux-amd64.zip  { <key>, plugin.json }

go build -o /tmp/<key> ./connector/<key> && /tmp/<key> --dump-manifest
```

`--dump-manifest` prints the `plugin.json` that `plugin build` puts in the
zip; check `module.meta.key`, the operations and configs there.

## Test locally

```bash
<app> plugin install bin/<key>-0.1.0-linux-amd64.zip   # or a folder / URL
<app> plugin list
```

or upload the zip from **Admin → Plugins → Add new plugin → Upload**. The
connector then appears in `wick_list` like a built-in one.

## Run one op from source (`--run-op`)

Every connector binary can run a single operation in-process and print the
result, without being installed and without wick spawning it. Because the op
runs in the same process, `go run` and `dlv debug` work directly — set a
breakpoint in the op handler and go:

```bash
# Offline, with your own (dummy/staging) credentials:
go run ./plugins/connector/<key> --run-op <op> \
  --params '{"field":"value"}' --config ./cfg.json

# Against a real instance's config, after an admin approves it:
export WICK_BASE_URL=https://wick.example.com WICK_PAT=wick_pat_...
go run ./plugins/connector/<key> --run-op <op> --params-file p.json --instance "Instance label"

# Same thing under the debugger:
dlv debug ./plugins/connector/<key> -- --run-op <op> --params-file p.json --instance "Instance label"
```

| Flag | Meaning |
|---|---|
| `--run-op <op>` | Operation key to run (required). |
| `--params '<json>'` / `--params-file <f>` | Op input as a JSON object. |
| `--config <f.json>` | Config keys as a JSON object. Never talks to wick. |
| `--instance <id\|label>` | Borrow a wick instance's config (see below). |
| `--wick-url` | Defaults to `$WICK_BASE_URL`. The token comes from `$WICK_PAT`. |
| `--raw` | Compact JSON output. |

Values the op passes to `c.Mask` print as `[masked]`. Exit codes: 0 ok,
1 op or wick error, 2 usage.

**Borrowing a real instance (`--instance`).** There is no separate request
command: just run. The first run files a *plugin test request* and exits with
a message saying so; it does not wait. The request shows up on **Admin →
Plugins → Development mode**, where an admin approves it for 15 minutes to 2
hours (or rejects it). Every run while it is pending, or after it was
rejected, is refused. Once approved, the next run goes through: with a PAT the
binary receives the instance's **plaintext** config and runs the op locally;
with an agent session (`WICK_CLI_TOKEN`) wick runs the binary itself and only
the masked result comes back. Guards:

- the page's **Allow plugin test requests** switch is off by default;
- a request is bound to the token that filed it, the runner's
  `<os user>@<hostname>`, a secret kept in the user cache dir, and the client
  IP wick saw. Every use must match all four; another token (even of the same
  user), machine or network files its own request;
- the requester must be able to see the instance;
- ops marked destructive are refused with `--instance` (use `--config`);
- every use is counted on the request row; **Revoke** or turning the switch
  off cuts it immediately. The page keeps 7 days of requests.

The credential itself does not expire with the approval: once handed out it
stays valid until rotated. Only allow connectors whose credentials are safe on
a developer machine (read-only ones such as Metabase or Loki).

## Calling wick (Host API)

An operation can call back into wick with `wickplugin.Host(c.Context())` — look up users, read tickets, run another connector's declared op as the person who called this one. `c.UserName(id)` already resolves names through it. The sample `connector/httpbin` op `whoami_host` calls `Whoami` + `GetUser` and declares `users.read` in `main`. See [Host API](./host-api.md) for the scopes, the `host_scopes` /
`host_resources` manifest keys (declared with
`wickplugin.DeclareHostAccess` in `main`) and who each call runs as.

## Release

Bump `VERSION` and follow [Release](./release.md).
