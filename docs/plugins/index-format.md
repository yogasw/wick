# Index format: `plugins.json` v2

Every source — a link, a GitHub release, the wick catalog — describes its
plugins with the same file, `plugins.json`. It is always an **array**, even
for one plugin.

```json
[
  {
    "key": "a2a_botx",
    "kind": "service",
    "name": "A2A adapter BotX",
    "description": "…",
    "version": "0.3.1",
    "proto_version": 1,
    "assets": {
      "linux/amd64": {
        "url": "a2a_botx-0.3.1-linux-amd64.zip",
        "zip_sha256": "9f2c…",
        "signature": "base64…"
      },
      "linux/arm64": { "url": "a2a_botx-0.3.1-linux-arm64.zip", "zip_sha256": "…" }
    },
    "default_tags": []
  }
]
```

| Field | Rule |
|---|---|
| `key` | `[a-z0-9_]`, max 64, unique in the file |
| `kind` | `connector` (default when empty), `tool`, `job`, `service` |
| `version` | semver, `v` optional |
| `assets` | `"<os>/<arch>"` → asset |
| `url` | absolute, or **relative to where `plugins.json` was fetched** |
| `zip_sha256` | sha256 of the zip; checked before anything is extracted |
| `signature` | ed25519 over the `zip_sha256` hex string, base64 |

The v1 shape (`assets` values were bare URL strings, `name` instead of
`key`) is still accepted when reading.

## Zip naming

`<key>-<version>-<os>-<arch>.zip`, e.g. `text_counter-0.1.0-linux-amd64.zip`.
The host reads key, version and os/arch from the name when there is no index
(a bare zip link), which is why keys cannot contain `-`.

## Two layers of hashing

1. `zip_sha256` in the index → checked on the downloaded zip.
2. `sha256` in `plugin.json` inside the zip → checked on the binary after
   extraction (plus the manifest signature when signing is required).

## Generating it

```bash
wick plugin build --kind tool text_counter --target linux/amd64,linux/arm64
wick plugin index --dir bin                     # bin/plugins.json, relative urls
wick plugin index --dir bin --key text_counter  # only one plugin
wick plugin index --dir bin --base-url https://cdn.example.com/plugins/
wick plugin index --dir bin --sign-key ~/.wick/plugin-signing.key
wick plugin sign --sign-key ~/.wick/plugin-signing.key bin/plugins.json
```

`plugin index` refuses a folder holding two versions of the same key, and a
zip whose name does not match its manifest.
