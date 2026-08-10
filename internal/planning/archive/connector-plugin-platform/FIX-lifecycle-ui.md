# Fix: plugin lifecycle UI (update / uninstall / install-refresh)

User report (2 bugs):
1. After installing a plugin, **no way to update or uninstall** it from the UI.
2. After install the button shows "loading"; the connector only appears in the
   list after navigating **back and reopening** — not immediately.

## TODO

- [ ] **BE-1** Surface plugin status to the FE: extend `/manager/api/plugins`
      `installed[]` entries with `update_available` + `latest_version` (compare
      installed manifest version vs catalog version). Already has `version`.
- [ ] **BE-2** Add `POST /manager/api/plugins/{key}/update` — re-resolve catalog
      URL + `InstallFromURL` (overwrites dir) + `reload`. (reuses install path.)
- [ ] **FE-1** `ConnectorList.svelte` (`/connectors/{key}`, the page the card
      opens, has "+ New instance"): add a **kebab ⋯ menu next to "+ New
      instance"** with Disable / Enable, Update (when available), Uninstall —
      shown ONLY when the key is an installed plugin. Cross-reference via
      `listPlugins()`.
- [ ] **FE-2** `ConnectorsIndex.svelte` list card: show an **"update available"
      indicator** (badge/dot) on installed plugin cards so it's visible without
      opening detail.
- [ ] **FE-3** Fix install refresh: after `installPlugin`, await BOTH lists and
      ensure the just-installed key is present before clearing the spinner
      (poll-with-timeout fallback if reload is async).
- [ ] **api.ts** add `updatePlugin(key)`; wire `setPluginEnabled`/`removePlugin`
      (already defined, currently unused).
- [ ] **types.ts** add `update_available`, `latest_version` to `PluginEntry`.

## Root cause

**Bug 1 — no update/uninstall:** backend has `/enable /disable /remove`
(`plugins_api.go`) and `setPluginEnabled`/`removePlugin` (`api.ts`), but the FE
**never imports them**. Installed plugin → renders as a plain connector card →
links to `ConnectorList`/`ConnectorDetail`, neither of which is plugin-aware.
`connectorDef`/`connectorListJSON` carry no `plugin`/`version` flag, so the FE
can't even tell a card is an uninstallable plugin. **Update never existed**
(no BE endpoint, no FE).

**Bug 2 — install needs back+reopen:** `apiInstall` reconciles synchronously
(`h.reload(ctx)` before responding), and `UpsertModule` writes the module map
under lock — so for admins the card *should* appear on the immediate `load()`.
Race surface: `onInstall` fires `loadAvailable()` un-awaited inside `load()`;
and a non-`Fixed` plugin seeds **zero instance rows** (`seedModuleRows` only
seeds when `Fixed`), so the card has 0 instances. FE-3 makes the refresh
deterministic (await + confirm key present).

## Decisions (from user)

- Plugin actions live on the **connector detail/list page header** as a kebab
  "⋯" next to "+ New instance" — NOT on every card.
- The **list card** gets a separate **update-available indicator** so a new
  version is visible without opening detail.
