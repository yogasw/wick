# Plugin security

The v1 model assumes plugins are **your own**: written by your team, released
from repositories you control.

## What is enforced

- **Admin only.** Adding or removing sources, uploading, installing, updating,
  enabling and uninstalling all go through the admin middleware. The CLI
  (`<app> plugin …`) needs shell access to the host, which is already
  admin-equivalent.
- **Transport and size.** Remote sources must be HTTPS (plain http only for
  localhost). Zips are capped at 100 MB, downloaded to a temp dir, verified,
  then moved into place.
- **Two hash layers.** `zip_sha256` from the index is checked before
  extraction; the binary `sha256` in `plugin.json` is checked after.
- **Signatures.** ed25519 over `zip_sha256` (index) and over the binary hash
  (manifest). Required when the source pins a publisher key or the host sets
  `WICK_PLUGIN_REQUIRE_SIGNATURE=1`; extra trusted keys come from
  `WICK_PLUGIN_PUBKEY` (comma-separated). Pin the key for private GitHub
  sources, so a compromised GitHub account cannot slip in a release.
- **PAT handling.** Stored encrypted, sent only to the GitHub API host, never
  to URLs that appear inside an index.
- **Process limits.** Each plugin is its own process with a scrubbed
  environment and optional rlimits: `WICK_PLUGIN_RLIMIT_AS_MB`,
  `WICK_PLUGIN_RLIMIT_CPU_SEC`, `WICK_PLUGIN_RLIMIT_NOFILE`.
- **Sockets.** Tool/service sockets are created `0600` in a `0700` run dir,
  so the `X-Wick-User-*` headers the host injects cannot be forged from
  another local user; browser-supplied `X-Wick-*` headers are stripped.
- **Service tokens.** Route tokens are stored hashed and can be rotated or
  revoked; the callback token `WICK_PLUGIN_TOKEN` is scoped to the plugin's
  `CallbackScopes` and revocable.
- **Audit.** Every install, update, upload and source change is logged with
  actor, source, key, old → new version and `zip_sha256`.
- **No silent updates.** `auto_update` is off unless enabled per source.

## What is NOT guarded yet

A plugin runs as the same OS user as wick. It can read whatever that user can
read, open any network connection, and start processes. Before accepting
plugins from third parties you would need, at least:

- a separate OS user (or systemd `DynamicUser` + `ProtectHome`),
- permissions declared in the manifest (allowed hosts, data dir) and enforced,
- a network allowlist,
- tool UIs served from a separate origin (iframe) instead of the wick origin,
- a visible "third-party" label in the UI.

Until then, install only plugins whose source you trust as much as wick
itself.
