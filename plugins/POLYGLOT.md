# Writing a wick plugin in another language

wick plugins talk to the host over **gRPC**, driven by HashiCorp's
[go-plugin](https://github.com/hashicorp/go-plugin) handshake. The contract is
language-agnostic: any language with a gRPC server and the go-plugin handshake
can be a wick connector. Go is the easiest (the `wickplugin.Serve` SDK does
everything), but nothing in the wire protocol is Go-specific.

## TODO for a non-Go plugin

- [ ] Generate gRPC stubs from `connector.proto` (below) in your language
- [ ] Implement the `Connector` service (at minimum `Schema` + `Execute` + `Health`)
- [ ] On startup, read the magic-cookie env var and print the go-plugin handshake line
- [ ] Serve gRPC over the address you printed; support AutoMTLS (see below)
- [ ] Ship the binary + a hand-written `plugin.json` (you can't use `--dump-manifest`)

## 1. The handshake (this is the non-obvious part)

go-plugin does NOT just exec your binary and connect. The host:

1. Sets an env var as a magic cookie. The plugin must verify it and exit if absent:

   ```
   WICK_CONNECTOR_PLUGIN = b3f1c2a4-wick-connector-grpc
   ```

2. Expects the plugin to print ONE line to **stdout**, then keep serving:

   ```
   CORE-PROTOCOL-VERSION | APP-PROTOCOL-VERSION | NETWORK | ADDRESS | PROTOCOL | SERVER-CERT
   ```

   For a wick connector over a Unix socket with gRPC:

   ```
   1|1|unix|/path/to/socket|grpc|<base64-server-cert>
   ```

   - `CORE-PROTOCOL-VERSION` = `1` (go-plugin's own version, not ours)
   - `APP-PROTOCOL-VERSION` = the wick proto version your plugin speaks (`1` today;
     the host accepts the range it advertises — see proto versioning below)
   - `NETWORK`/`ADDRESS` = `unix` + socket path (or `tcp` + `host:port`)
   - `PROTOCOL` = `grpc`
   - `SERVER-CERT` = base64 DER of your TLS cert when AutoMTLS is on (see §3)

3. Reads that line, dials the address, and speaks gRPC.

Most languages have a go-plugin-compatible library that handles this line for
you (e.g. `python-hashicorp-plugin`, `go-plugin` ports). If not, printing the
line yourself is a few lines of code.

## 2. The service contract

Implement this proto (same file the Go SDK uses, `pkg/plugin/proto/connector.proto`):

```proto
syntax = "proto3";
package wick.connector.v1;

service Connector {
  rpc Schema           (SchemaRequest)    returns (SchemaResponse);
  rpc Execute          (ExecuteRequest)   returns (ExecuteResponse);
  rpc ExecuteStream    (ExecuteRequest)   returns (stream Chunk);     // optional (bulk)
  rpc CheckPermissions (PermsRequest)     returns (PermsResponse);    // optional
  rpc ResolveIdentity  (IdentityRequest)  returns (IdentityResponse); // optional
  rpc Health           (HealthRequest)    returns (HealthResponse);
}

message ExecuteRequest {
  string operation = 1;            // "issues.create"
  bytes  args_json = 2;            // the op's input, JSON-encoded
  map<string,string> creds = 3;    // host-decrypted credentials (plaintext)
  string request_id = 4;
  string session_id = 5;
}
message ExecuteResponse { bytes result_json = 1; Error error = 2; map<string,string> meta = 3; }
message SchemaResponse  { bytes manifest_json = 1; }  // the Module JSON (see plugin.json)
message Error { string code = 1; string message = 2; }
// ... full definitions in pkg/plugin/proto/connector.proto
```

- **`Schema`** returns your module as JSON — the same shape as `module` in
  `plugin.json` (Meta + Operations + Configs). The host calls it once at load.
- **`Execute`** is the workhorse: `operation` names the op, `args_json` is its
  input, `creds` carries host-decrypted credentials. Return `result_json`.
- **`Health`** is a liveness ping.

The same `Connector` service serves **all plugin kinds** (connector / tool / job)
— a tool's "run with input" and a job's "run on trigger" both map onto
`Execute(operation, args_json)`. The kind is declared in the manifest, not the
service.

## 3. AutoMTLS

The host enables AutoMTLS: it generates a client cert, passes it to your plugin
via the `PLUGIN_CLIENT_CERT` env var, and expects your server cert in the
handshake line. Your gRPC server must:

- Trust the client cert from `PLUGIN_CLIENT_CERT` (mutual TLS).
- Present a server cert and put its base64 DER in the handshake line's last field.

Libraries that implement the go-plugin handshake do this for you. Rolling it by
hand means standard mTLS setup keyed off those two values.

## 4. The manifest (`plugin.json`)

A non-Go plugin can't use `wick plugin build` / `--dump-manifest` (those compile
Go). Hand-write `plugin.json` matching the envelope — see `connector/_template`'s
generated output for the exact shape. The host verifies `sha256` of your binary
and (if signed) the signature before it ever spawns the process, so those fields
must be correct for the target binary.

```json
{
  "schema_version": 1,
  "kind": "connector",
  "version": "0.1.0",
  "proto_version": 1,
  "entry": "my-plugin",
  "os_arch": ["linux/arm64"],
  "sha256": "<hex sha256 of the binary>",
  "signature": "",
  "module": { "Meta": {...}, "Configs": [...], "Operations": [...] }
}
```

## Proto versioning

`proto_version` in the manifest declares which wire contract your plugin speaks.
The host accepts an inclusive range `[MinProtoVersion, ProtoVersion]` (both `1`
today) and rejects anything outside it at verify time with a clear error. A
breaking proto change bumps `ProtoVersion`; old plugins keep working until
`MinProtoVersion` is raised. Stamp the version your stubs were generated from.

## Reality check

This path works, but it's more effort than Go (where `wickplugin.Serve(Module())`
is the whole binary). Reach for it when a connector genuinely needs a non-Go
ecosystem (an existing Python/Rust SDK for the target API). For everything else,
copy `connector/_template`.
