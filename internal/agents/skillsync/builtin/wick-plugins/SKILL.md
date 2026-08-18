---
name: wick-plugins
description: Use when the user asks about installing, updating, removing, disabling, or building a wick plugin — a connector shipped as a standalone binary that wick runs over gRPC. Covers the plugin CLI, the marketplace catalog, install verification and signing, when to choose a plugin over an in-tree module or a custom connector, and how to disable a connector type entirely.
---

# Using wick plugins

A **plugin** is a connector shipped as a standalone binary. wick downloads it, verifies it, and runs it in its own process, talking over gRPC. Installing or updating one needs no core rebuild and no restart.

From the LLM's side a plugin connector is indistinguishable from a built-in one — same `tool_id` shape, same encrypted fields, same audit trail, same tag-based access control. The difference is purely packaging.

## Choosing the right connector form

| | Connector module | Custom connector | Plugin |
|---|---|---|---|
| Lives in | Go code compiled into wick | A database row (admin UI) | A separate binary |
| Runs | In the wick process | In the wick process | Its own subprocess (gRPC) |
| Add / update | Code change + redeploy | Edit in UI, click Reload | `install` / bump version |
| Versioned | With the core | With the database | Independently |

Reach for a plugin when the connector should **release on its own schedule** or be distributable through the marketplace. For something that ships inside the app, write a connector module. For a no-code definition, use a custom connector.

## Installing

Plugins are managed from the **app** binary, not the dev CLI:

```bash
<app> plugin search             # browse the marketplace catalog
<app> plugin install slack      # download + verify + install by name
<app> plugin list               # installed, version, arch, signature, enabled
<app> plugin disable slack      # turn off without removing
<app> plugin enable slack
<app> plugin remove slack
```

Every install **verifies before wiring in**: the binary's sha256 must match its manifest, the OS/arch must match the host, and when a trusted key is configured the signature must check out. A hot-reload poller picks up any change within a few seconds — no restart.

You can install without the catalog too:

```bash
<app> plugin install ./my-connector/                          # a built {binary, plugin.json} dir
<app> plugin install https://example.com/foo-0.1.0-linux-arm64.zip
```

## Where plugins come from

`search` and `install <name>` read a `plugins.json` catalog fetched directly (not through the GitHub API, so no rate limit and no token). Entries point at per-OS/arch release downloads; the binary is fetched only on install. Point wick elsewhere with `WICK_PLUGIN_CATALOG=<url>`.

## Updating from the UI

The connector detail page (Manager → Connectors → {connector}) carries admin-only lifecycle actions in its header kebab menu:

- **Update to v{X}** — appears when the catalog has a newer version. Downloads and hot-swaps the binary, no restart. The card shows a live progress bar.
- **Uninstall plugin** — removes the binary. Existing rows and their config stay in the database and go inert; reinstalling restores them.

Install and update replace the binary with an atomic rename, so updating while a request is in flight does not fail.

Same operation over the API (admin-only):

```
POST /manager/api/plugins/{key}/update
```

**Anyone logged in can browse** the catalog; the lifecycle actions are admin-only. A non-admin sees "Requires admin" on the Download button and no kebab menu.

## Building a plugin

Building is the producer side and uses the **`wick` dev CLI**, run from a `plugins` checkout:

```bash
wick plugin build slack --all                                # every OS/arch → one zip each
wick plugin build slack --target linux/arm64,darwin/amd64
```

Each build produces `slack-<version>-<os>-<arch>.zip` with the binary plus a `plugin.json` generated **from the binary itself**, so the manifest cannot drift from the code. Sign with `--sign-key` (ed25519 manifest signature) or `--cosign-key` (cosign binary signature).

After publishing releases, regenerate the catalog:

```bash
wick plugin catalog --repo owner/plugins-repo --out plugins/plugins.json
```

The connector code itself — `Meta`, `Configs`, `Operations`, the `wick:"..."` tags — is written exactly like an in-tree connector module. A plugin only adds a small `main.go` wrapper.

## Disabling a connector type

Any connector, built-in or plugin, can be hidden from the LLM entirely. This is separate from the per-row `Disabled` flag: it gates the whole type, so every instance and every operation disappears from `wick_list` / `wick_execute`. Rows stay in the manager UI with a **Disabled** badge and can be re-enabled anytime.

```
POST /manager/api/connectors/{key}/type-disable
POST /manager/api/connectors/{key}/type-enable
```

Use **type disable** to stop an entire connector until it is reconfigured; use **per-row Disable** to hide one credential set without affecting others.

## Security

- **Verified before load** — OS/arch match, supported proto version, sha256 integrity, and signature when `WICK_PLUGIN_PUBKEY` / `WICK_PLUGIN_REQUIRE_SIGNATURE` are set.
- **Credentials stay in the host** — wick decrypts encrypted fields and passes plaintext over the local gRPC channel; the plugin never holds the master key.
- **Process isolation** — a plugin crash cannot take down the core.

Installing a plugin runs third-party native code. Recommend only trusted plugins, and `WICK_PLUGIN_REQUIRE_SIGNATURE=1` in production.
