---
name: wick-plugins
description: Use when the user asks about installing, updating, removing, disabling, or building a wick plugin — a connector, tool, job, or service shipped as a standalone binary that wick runs in its own process. Covers the four kinds, plugin sources (upload, link, GitHub release incl. private + PAT), Add new plugin, the plugin CLI, plugins.json, install verification and signing, releasing from a plugins repo, troubleshooting, and how to disable a connector type entirely.
---

# Using wick plugins

A **plugin** is a connector, tool, job, or service shipped as a standalone binary. wick downloads it, verifies it, and runs it in its own process. Installing or updating one needs no core rebuild and no restart.

From the LLM's side a plugin connector is indistinguishable from a built-in one — same `tool_id` shape, same encrypted fields, same audit trail, same tag-based access control. The difference is purely packaging.

## The four kinds

| Kind | Lifetime | Reached by | Notes |
|---|---|---|---|
| connector | on demand, idle-killed | the LLM via `wick_execute` | gRPC |
| job | spawned per run (cron / Run now), killed when `Run` returns | the scheduler | gRPC `Job`; shows on the Jobs page |
| tool | spawned on first request (held ≤10 s, then 503), idle-killed | users at `/tools/{key}` | HTTP over a unix socket, proxied by wick; "plugin" badge on the home grid; `KeepWarm` for fast webhooks |
| service | always on, restarted with backoff 1s→30s | outside systems at `/x/{key}/*` | per-route auth `public` / `token` / `session`; admin page Manager → Services → {key} (status, last 200 log lines, config, access token) |

Installed under `<data dir>/plugins/{connectors,jobs,tools,services}/<key>/`. Plugin keys use `_`, never `-` — so a built-in tool `text-counter` moved to a plugin becomes `/tools/text_counter`.

## Replacing a built-in (`Replaces`)

When a plugin takes over an existing tool or job under a new key (built-in `notion-ticket-sync` → plugin `notion_ticket_sync`), the plugin declares the old key so it is a **replacement, not a new item**:

```go
job.Meta{Key: "notion_ticket_sync", Replaces: []string{"notion-ticket-sync"}, ...}   // job plugin
tool.Tool{Key: "text_counter", Replaces: []string{"text-counter"}, ...}               // tool plugin
```

It lands in `plugin.json` (`job.meta.Replaces` / `tool.meta.replaces`). On the next boot wick, once per old→new pair:

- copies config fields whose key the plugin also declares — secrets as the stored ciphertext, never decrypted. A field already set on the plugin is kept; only an empty field, or a non-secret still at the plugin default, is filled. A field whose secret flag differs is skipped;
- for jobs, copies schedule, enabled, max runs and timeout while the plugin's job row is untouched (default cron, disabled, never run); run history stays on the old key;
- merges the old item's tags, visibility override and bookmarks into the new path;
- hides the old item (not registered, not listed, its job row disabled on every boot), so the two never both run.

Old rows are not deleted: removing the plugin brings the old item back. Admin re-run: `GET /manager/api/plugins/{key}/replace` is a dry run (secrets shown as `set`/`empty`), `POST …/replace` applies (`?force=1` after a previous run). Each migration is recorded in the plugin audit log (`replace.migrate`).

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

## Add new plugin (sources)

Admin → **Plugins** → *Add new plugin* — admin only:

- **Upload** a zip (binary + `plugin.json`, max 100 MB). No update detection.
- **Link** to a `plugins.json` (polled for new versions) or straight to a `.zip` (one-off).
- **GitHub release** `owner/repo`, public or private. Private needs a fine-grained PAT for that repo with `Contents: Read-only`; it is stored encrypted and sent only to the GitHub API. Releases must be tagged `<key>/v<version>` with the zips + a `plugins.json` as assets.

Sources are re-checked every 30 minutes by default (ETag; *Check now* forces it). A newer version shows *Update available*; the update itself is the kebab **Update to vX** on the plugin's detail page (Connectors / Tools / Jobs / Services). `auto_update` is off unless enabled per source. Pin a publisher public key on a source to reject unsigned releases.

CLI equivalent: `<app> plugin source add <https-url-to-plugins.json|owner/repo>`, `plugin source list`, `plugin source check <id>`, `plugin source remove <id>`.

The **Test** button runs six checks: Reachable, Auth, Index, Asset for host, Dry-run (download + `zip_sha256` + signature + manifest, nothing installed), After install (plugin `Health`).

## The marketplace catalog

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

To write the plugin code itself (pick the kind, template, Module contract, testing, service routes and RemoteSource), use the **wick-plugin-authoring** skill (and **wick-plugin-service** for service plugins). This section covers only the build and release commands.

Building is the producer side and uses the **`wick` dev CLI**, run from the folder holding `connector/ tool/ job/ service/` (each has a `_template` to copy):

```bash
wick plugin build slack --all                                # every OS/arch → one zip each
wick plugin build slack --target linux/arm64,darwin/amd64
wick plugin build --kind tool text_counter --target linux/amd64
wick plugin index --dir bin                                  # plugins.json v2: relative urls + zip_sha256
wick plugin sign --sign-key k.key bin/plugins.json           # sign every zip_sha256
```

Only `main.go` is new when wrapping existing code: `wickplugin.Serve(connector.Module)`, `wickplugin.ServeJob(job.Module)`, `toolplugin.ServeTool(tool.Module)`, `service.ServeService(service.Module)`.

A separate plugins repo releases through the reusable workflow `uses: yogasw/wick/.github/workflows/plugin-release.yml@master`: every `<kind>/<key>/VERSION` without a `<key>/v<VERSION>` tag is built, indexed and released.

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

## Troubleshooting

| Symptom | Cause |
|---|---|
| 404 from a private GitHub source | the PAT cannot read that repo (GitHub answers 404, not 403) |
| 403 | rate limit without a PAT, or an expired token |
| "has no build for linux/amd64" | the release lacks a zip for this host |
| "file name is not `<key>-<version>-<os>-<arch>.zip`" | renamed asset or a `-` in the key |
| "zip_sha256 mismatch" | zip replaced after `plugins.json` was written — re-run `wick plugin index` |
| "signature required but the index entry is unsigned" / "signature verification failed" | the source pins a key or `WICK_PLUGIN_REQUIRE_SIGNATURE=1`, and the release was not signed with the matching key |
| tool returns 503 on first open | the plugin did not become ready within 10 s — check it starts (`<bin> --dump-manifest`) |
| service keeps restarting | read the last 200 log lines on Manager → Services → {key} |
| plugin socket errors | the socket path must stay under 108 bytes; shorten `WICK_PLUGIN_SOCKET_DIR` |

Service plugin config is edited on its admin page; there is no MCP operation for it yet.

## Security

- **Verified before load** — OS/arch match, supported proto version, sha256 integrity, and signature when `WICK_PLUGIN_PUBKEY` / `WICK_PLUGIN_REQUIRE_SIGNATURE` are set.
- **Credentials stay in the host** — wick decrypts encrypted fields and passes plaintext over the local gRPC channel; the plugin never holds the master key.
- **Process isolation** — a plugin crash cannot take down the core.

Installing a plugin runs third-party native code. Recommend only trusted plugins, and `WICK_PLUGIN_REQUIRE_SIGNATURE=1` in production.
