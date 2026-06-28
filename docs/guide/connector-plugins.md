---
outline: deep
---

# Connector Plugins

Ship a connector as an **external plugin** — a standalone binary that wick downloads and runs in its own process, talking to the core over gRPC. The connector lives under `plugins/` in the wick repo, builds and versions on its own schedule, and installs into a running app without recompiling or redeploying wick.

From the LLM's point of view a plugin connector is indistinguishable from a built-in one: same `tool_id` shape in `wick_list` / `wick_execute`, same [encrypted fields](/reference/encrypted-fields), same run audit trail, same [tag-based access control](/guide/connector-module#sharing-connectors-with-tags). The difference is purely how it's packaged and shipped.

Use a plugin when you want a connector to **release independently of the core** — its own version, its own build, distributable through the marketplace — or to keep the core binary lean. For a connector that ships inside your app, write a [connector module](/guide/connector-module); for a no-code definition built in the UI, use a [custom connector](/guide/custom-connectors).

## How it compares

| | [Connector module](/guide/connector-module) | [Custom connector](/guide/custom-connectors) | Connector plugin |
|---|---|---|---|
| Lives in | Go code compiled into wick | A database row (admin UI) | A separate binary in `plugins` |
| Runs | In the wick process | In the wick process | Its own subprocess (gRPC) |
| Add / update | Code change + redeploy | Edit in UI, click Reload | `install` / bump version — no core rebuild |
| Versioned | With the core | With the database | Independently, per plugin |
| Distribution | — | — | Marketplace (downloadable) |
| MCP surface, secrets, audit, tags | Same | Same | Same |

The connector code itself — `Meta`, `Configs`, `Operations`, the `wick:"..."` tags — is written exactly as a [connector module](/guide/connector-module). A plugin only adds a tiny `main.go` wrapper and is built as a separate binary.

## Installing a plugin

Plugins are managed from the **app** binary (`<your-app> plugin ...`). The full command reference is in [App CLI → Plugin](/reference/app-cli#plugin); the essentials:

```bash
<app> plugin search                # browse the marketplace catalog
<app> plugin install slack         # download + verify + install by name
<app> plugin list                  # what's installed, version, arch, signature, enabled
<app> plugin disable slack         # turn off without removing
<app> plugin enable slack
<app> plugin remove slack
```

Every install **verifies** the plugin before it's wired in — the binary's sha256 must match its manifest, the OS/arch must match the host, and (when a trusted key is configured) the signature must check out. A hot-reload poller inside the app picks up an install / enable / disable / remove within a few seconds — **no restart needed**.

In the manager UI, plugins available to install appear in the **same connector list** under "Available to install", alongside built-in and already-installed connectors. Installing one is a single click.

### Where plugins come from

`search` and `install <name>` read a **catalog** — a `plugins.json` file published in the `plugins` repo and fetched directly (not via the GitHub API, so there's no rate limit and no token needed). Each entry points at the per-OS/arch download URL of a GitHub release; the binary is only downloaded when you install. Point wick at a different catalog with `WICK_PLUGIN_CATALOG=<url>`.

You can also install without the catalog — from a local directory, a `.zip`/`.tar.gz` file, or a direct URL:

```bash
<app> plugin install ./my-connector/                      # a built {binary, plugin.json} dir
<app> plugin install https://example.com/foo-0.1.0-linux-arm64.zip
```

## Building a plugin

Building is the producer side and uses the **`wick` dev CLI** (`wick plugin build`), run from a `plugins` checkout. See [CLI → wick plugin build](/reference/cli#wick-plugin-build) for the full flag list. In short:

```bash
# from a plugins repo
wick plugin build slack --all          # cross-build every OS/arch → one zip each
```

Each build produces `slack-<version>-<os>-<arch>.zip` containing the binary plus a `plugin.json` generated **from the binary itself**, so the manifest can never drift from the code. Optionally sign each build (`--sign-key` for an ed25519 manifest signature, `--cosign-key` for a cosign binary signature).

Authoring + release flow (folder layout, the `key` == folder rule, the PR → release CI that publishes a release and updates the catalog) lives in the `plugins` repo's `README.md` and `RELEASE.md`.

## Security

- **Verified before load.** A plugin is never registered until `VerifyManifest` passes: OS/arch match, supported proto version, sha256 integrity, and signature (when a trusted key is set via `WICK_PLUGIN_PUBKEY` / `WICK_PLUGIN_REQUIRE_SIGNATURE`).
- **Credentials stay in the host.** wick decrypts a connector's [encrypted fields](/reference/encrypted-fields) and passes the plaintext to the plugin over the local gRPC channel; the plugin never holds the master key.
- **Process isolation.** A plugin crash can't take down the core — it runs in its own subprocess, spawned lazily and reaped when idle.
- Installing a plugin runs third-party native code. Install only plugins you trust, and prefer signed plugins with `WICK_PLUGIN_REQUIRE_SIGNATURE=1` in production.
