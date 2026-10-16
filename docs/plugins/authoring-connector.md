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
- An op that only some instance configs can serve sets
  `op.VisibleWhen = "<configKey>:<a|b>"` (e.g. `"browser:moli"`): instances
  whose config does not match never show it to agents and refuse to run it.
  `--dump-manifest` and `plugin install` reject a predicate naming an unknown
  or secret config key. See
  [Connector module](../guide/connector-module.md#hiding-an-op-by-instance-config-visiblewhen).

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

# Against a real instance's config, after an admin approves it (a person):
export WICK_BASE_URL=https://wick.example.com WICK_PAT=wick_pat_...
go run ./plugins/connector/<key> --run-op <op> --params-file p.json --instance "Instance label"

# Same, from an agent session on the wick host (no PAT, no plaintext config):
# mint a token with the wick_cli_token MCP tool, then
export WICK_BASE_URL=http://127.0.0.1:<port> WICK_CLI_TOKEN=wick_cli_...
go build -o ./candidate ./plugins/connector/<key>
./candidate --run-op <op> --params '{}' --instance "Instance label"

# Same thing under the debugger:
dlv debug ./plugins/connector/<key> -- --run-op <op> --params-file p.json --instance "Instance label"
```

| Flag | Meaning |
|---|---|
| `--run-op <op>` | Operation key to run (required). |
| `--params '<json>'` / `--params-file <f>` | Op input as a JSON object. |
| `--config <f.json>` | Config keys as a JSON object. Never talks to wick. |
| `--instance <id\|label>` | Borrow a wick instance's config (see below). |
| `--wick-url` | Defaults to `$WICK_BASE_URL`. The token comes from `$WICK_PAT` (a person) or `$WICK_CLI_TOKEN` (an agent session on the wick host). |
| `--raw` | Compact JSON output. |

Values the op passes to `c.Mask` print as `[masked]`. Exit codes: 0 ok,
1 op or wick error, 2 usage.

**Borrowing a real instance (`--instance`).** There is no separate request
command: just run. The first run files a *plugin test request* and exits with
a message saying so; it does not wait. The request shows up on **Admin →
Plugins → Development mode**, where an admin approves it for 15 minutes to 8
hours (a preset or a custom number of minutes, or rejects it). Every run while it is pending, or after it was
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

**Development mode, step by step.**

1. An admin turns on **Allow plugin test requests** on Admin → Plugins →
   Development mode (once).
2. Run the op with `--instance`. The first run prints
   `plugin test request <id> filed (<key> · <instance>, <user@host>). Not run: …`
   and exits 1.
3. The admin sees the request with the **SSO account** (name + email), the
   instance (linked to its settings page, with the `conn:<id>` that
   `wick_list` shows), what it is bound to (IP · machine · token: PAT name
   …last4 or `agent session <id>`) and the op, then approves it for 15 min –
   8 h (preset or custom minutes) or rejects it.
4. Run the same command again. Within the approval window every rerun from the
   same token, machine and IP works without asking again.

A person (PAT) gets the config into their own process, so breakpoints work.
An agent session never sees the config: wick spawns the candidate binary
itself, runs the op through the normal pipeline (access checks, masking, run
log tagged `plugin_test`) and returns the masked result. That path only works
from the wick host (loopback), since wick runs the binary file you name. wick
also sets `WICK_PLUGIN_DATA_DIR` to the installed plugin's data dir, so
`wickplugin.DataDir` in the candidate finds what that plugin already
downloaded instead of falling back to OS temp.

The credential itself does not expire with the approval: once handed out it
stays valid until rotated. Only allow connectors whose credentials are safe on
a developer machine (read-only ones such as Metabase or Loki).

## Returning a file instead of data

An op whose answer is a file (an export, a report) should not put thousands of
rows in its result. Attach the file with `connector.WithFile`; wick writes it
to `artifacts/<name>.<format>` in the calling agent session's working
directory (creating `artifacts/` when missing, sanitizing the name, adding a
`-2`, `-3` … suffix instead of overwriting) and returns a `file` reference in
its place:

```go
return connector.WithFile(map[string]any{"rows": len(rows)}, connector.FileOutput{
    Name: c.Input("file_name"), Format: "csv", Content: csvText,
}), nil
// the caller gets: {"rows": 1234, "file": {"path": "artifacts/apps.csv", "format": "csv", "bytes": 52311}}
```

- Text formats go in `Content`: `csv`, `tsv`, `json`, `ndjson`, `txt`; 60 MB at
  most.
- Binary files (a downloaded image, PDF, video, archive) go in `Data` (raw
  bytes) or `ContentBase64` (standard or URL-safe base64). wick writes the raw
  bytes; the agent only gets the path. `Data` is encoded as
  `content_base64` automatically when it crosses the plugin transport. Built-in
  binary formats: `png`, `jpg`, `jpeg`, `gif`, `webp`, `pdf`, `mp4`, `mov`,
  `webm`, `mp3`, `ogg`, `wav`, `zip`, `docx`, `xlsx`, `pptx`; 50 MB at most by
  default, checked before decoding. A text format may also come as bytes (a
  CSV that is not UTF-8); those bytes are written unchanged. Set exactly one of
  `Content`, `ContentBase64` and `Data`.
- Plugins talk to wick over gRPC with a 64 MB message limit, so a plugin's
  file tops out around 48 MB of bytes (base64 adds a third) whatever the admin
  limit says.
- The admin can change the allowed formats (`file_io.allowed_formats`) and
  the binary limit (`file_io.max_binary_mb`, at most 200) on the app config
  page, live. Scripts and executables (`exe`, `sh`, `py`, `js`, `php`, …) are
  never written. For a format the admin added, content whose type wick can
  recognise must match the extension, and a built-in binary format whose
  content is HTML is refused. Masking does not reach inside bytes
  (`ContentBase64`, `Data`), so use them for files you downloaded, not for
  content you built from secrets.
- The content passes the normal masking sweep before it is written, so values
  given to `c.Mask` are masked inside the file too. For large files, mask
  cells yourself with a fixed placeholder instead of `c.Mask` on every value.
- It needs an agent session (the file goes into that session's folder). A
  call without one (a workflow, a schedule, a stdio MCP client such as Claude
  Desktop) fails with an error that says so; return a smaller result inline
  for those callers if they matter to your op.
- Make the format and file name required inputs, so the agent has to decide
  where the output goes.

## Calling wick (Host API)

An operation can call back into wick with `wickplugin.Host(c.Context())` — look up users, read tickets, run another connector's declared op as the person who called this one. `c.UserName(id)` already resolves names through it. The sample `connector/httpbin` op `whoami_host` calls `Whoami` + `GetUser` and declares `users.read` in `main`. See [Host API](./host-api.md) for the scopes, the `host_scopes` /
`host_resources` manifest keys (declared with
`wickplugin.DeclareHostAccess` in `main`) and who each call runs as.

## Release

Bump `VERSION` and follow [Release](./release.md).
