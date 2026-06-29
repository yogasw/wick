<script lang="ts">
  import { Button, ConfirmDialog, KebabMenu } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { push } from "$lib/router.js";
  import {
    getConnector,
    createConnectorRow,
    toggleConnectorDisabled,
    duplicateConnectorRow,
    deleteConnectorRow,
    reloadConnector,
    resyncMcpTools,
    disconnectConnectorAccount,
    setConnectorTypeDisabled,
    listPlugins,
    updatePlugin,
    removePlugin as uninstallPlugin,
  } from "$lib/api.js";
  import { startConnectorOAuth, type OAuthConnect } from "./connectorOAuth.js";
  import type { ConnectorList, ConnectorRow, ConnectorAccount, PluginEntry } from "$lib/types.js";
  import { setBreadcrumbNames, clearBreadcrumbNames } from "$lib/stores/breadcrumb.js";

  type Props = { connectorKey: string };
  let { connectorKey }: Props = $props();

  let data = $state<ConnectorList | null>(null);
  let loading = $state(true);
  let error = $state("");
  let busy = $state(false);
  let confirmRow = $state<ConnectorRow | null>(null);
  let reloadBusy = $state(false);
  let resyncBusy = $state(false);

  /* Plugin overlay for THIS connector key: non-null when the connector is a
     downloaded plugin (not a built-in). Drives the header kebab's Update /
     Uninstall items + the "update available" hint. Loaded best-effort
     (admin-only endpoint); a failure just hides plugin actions. */
  let plugin = $state<PluginEntry | null>(null);
  /* Whether the viewer may run the header kebab's admin actions (type
     enable/disable, plugin update/uninstall). The list endpoint is readable
     by everyone now, so a successful fetch no longer implies admin — gate the
     action items on this flag instead. */
  let isAdmin = $state(false);
  let typeBusy = $state(false);
  let pluginBusy = $state(false);
  let confirmUninstall = $state(false);

  async function loadPlugin() {
    try {
      const r = await listPlugins();
      isAdmin = r.is_admin;
      plugin = (r.installed ?? []).find((p) => p.key === connectorKey) ?? null;
    } catch {
      // Marketplace unavailable — hide plugin actions but leave isAdmin as-is.
      plugin = null;
    }
  }

  /* Per-row kebab (⋮) menu items. History/Disable/Duplicate/Delete —
     Duplicate + Delete only when the connector isn't fixed. */
  function rowMenuItems(row: ConnectorRow) {
    const items = [
      { label: "History", onclick: () => push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(row.id)}/history`) },
      { label: row.disabled ? "Enable" : "Disable", onclick: () => toggleDisabled(row) },
    ];
    if (!data?.fixed) {
      items.push({ label: "Duplicate", onclick: () => duplicateRow(row), disabled: busy });
      items.push({ label: "Delete", onclick: () => (confirmRow = row), danger: true });
    }
    return items;
  }

  /* Per-row OAuth connect: the row id currently mid-popup, the live popup
     handle, and the account pending disconnect confirmation. */
  let connectingId = $state("");
  let oauthHandle: OAuthConnect | null = null;
  let confirmAcc = $state<{ row: ConnectorRow; acc: ConnectorAccount } | null>(null);

  /* Connect button is offered only when the backend resolved a start_url
     (SSO on + caller may connect + client_id set). Label mirrors
     AccountsSection: first connect vs reconnect vs add-another. */
  function canConnect(row: ConnectorRow): boolean {
    return !!row.oauth && !!row.oauth.start_url;
  }
  function connectLabel(row: ConnectorRow): string {
    const n = row.accounts?.length ?? 0;
    if (n === 0) return "Connect";
    return row.multi_account ? "+ Connect another" : "Reconnect";
  }

  function connect(row: ConnectorRow): void {
    const url = row.oauth?.start_url;
    if (!url || connectingId) return;
    connectingId = row.id;
    oauthHandle = startConnectorOAuth(url);
    oauthHandle.promise
      .then(() => {
        toastOk("Account connected");
        return load(true);
      })
      .catch((e) => toastError("Connect failed", e instanceof Error ? e.message : String(e)))
      .finally(() => {
        connectingId = "";
        oauthHandle = null;
      });
  }

  async function confirmDisconnectAccount() {
    const pending = confirmAcc;
    confirmAcc = null;
    if (!pending) return;
    try {
      await disconnectConnectorAccount(connectorKey, pending.row.id, pending.acc.id);
      toastOk("Account disconnected");
      await load(true);
    } catch (e) {
      toastError("Disconnect failed", e instanceof Error ? e.message : String(e));
    }
  }

  $effect(() => () => oauthHandle?.cancel());

  async function load(silent = false) {
    if (!silent) loading = true;
    try {
      data = await getConnector(connectorKey);
      error = "";
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (silent) {
        toastError("Refresh failed", msg);
      } else {
        error = msg;
      }
    } finally {
      if (!silent) loading = false;
    }
  }

  async function newRow() {
    if (busy) return;
    busy = true;
    try {
      const id = await createConnectorRow(connectorKey);
      push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(id)}`);
    } catch (e) {
      toastError("Could not create row", e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  async function toggleDisabled(row: ConnectorRow) {
    try {
      const disabled = await toggleConnectorDisabled(connectorKey, row.id);
      toastOk(disabled ? "Row disabled" : "Row enabled");
      await load(true);
    } catch (e) {
      toastError("Action failed", e instanceof Error ? e.message : String(e));
    }
  }

  async function duplicateRow(row: ConnectorRow) {
    if (busy) return;
    busy = true;
    try {
      const id = await duplicateConnectorRow(connectorKey, row.id);
      push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(id)}`);
    } catch (e) {
      toastError("Duplicate failed", e instanceof Error ? e.message : String(e));
    } finally {
      busy = false;
    }
  }

  async function confirmDelete() {
    const row = confirmRow;
    confirmRow = null;
    if (!row) return;
    try {
      await deleteConnectorRow(connectorKey, row.id);
      toastOk("Row deleted");
      await load(true);
    } catch (e) {
      toastError("Delete failed", e instanceof Error ? e.message : String(e));
    }
  }

  async function reloadDef() {
    if (reloadBusy) return;
    reloadBusy = true;
    try {
      await reloadConnector(connectorKey);
      toastOk("Definition reloaded");
      await load(true);
    } catch (e) {
      toastError("Reload failed", e instanceof Error ? e.message : String(e));
    } finally {
      reloadBusy = false;
    }
  }

  async function resyncTools() {
    if (resyncBusy) return;
    resyncBusy = true;
    try {
      const res = await resyncMcpTools(connectorKey);
      toastOk(`Tools re-synced — ${res.operations} operation(s)`);
      await load(true);
    } catch (e) {
      toastError("Re-sync failed", e instanceof Error ? e.message : String(e));
    } finally {
      resyncBusy = false;
    }
  }

  /* Connector-TYPE off-switch (header kebab). Disabling hides the whole
     connector from the LLM; the page stays open so it can be re-enabled. */
  async function toggleType() {
    if (!data || typeBusy) return;
    typeBusy = true;
    const next = !data.disabled_type;
    try {
      await setConnectorTypeDisabled(connectorKey, next);
      data = { ...data, disabled_type: next };
      toastOk(next ? "Connector disabled" : "Connector enabled");
    } catch (e) {
      toastError("Action failed", e instanceof Error ? e.message : String(e));
    } finally {
      typeBusy = false;
    }
  }

  async function doUpdate() {
    if (pluginBusy) return;
    pluginBusy = true;
    try {
      const r = await updatePlugin(connectorKey);
      toastOk(r.version ? `Updated to v${r.version}` : "Plugin updated");
      await Promise.all([load(true), loadPlugin()]);
    } catch (e) {
      toastError("Update failed", e instanceof Error ? e.message : String(e));
    } finally {
      pluginBusy = false;
    }
  }

  async function confirmDoUninstall() {
    confirmUninstall = false;
    if (pluginBusy) return;
    pluginBusy = true;
    try {
      await uninstallPlugin(connectorKey);
      toastOk("Plugin uninstalled");
      push("/"); // connector is gone — back to the index
    } catch (e) {
      toastError("Uninstall failed", e instanceof Error ? e.message : String(e));
      pluginBusy = false;
    }
  }

  /* Header kebab items: the type on/off switch for every connector, plus
     Update (when a newer version exists) + Uninstall for plugins. Every item
     is an admin-only action server-side, so non-admins get an empty menu (the
     kebab itself is hidden when empty). */
  let headerMenu = $derived.by(() => {
    if (!isAdmin) return [];
    const items: { label: string; onclick: () => void; danger?: boolean; disabled?: boolean }[] = [
      {
        label: data?.disabled_type ? "Enable connector" : "Disable connector",
        onclick: toggleType,
        disabled: typeBusy,
      },
    ];
    if (plugin) {
      if (plugin.update_available) {
        items.push({
          label: pluginBusy
            ? "Updating…"
            : `Update to v${plugin.latest_version ?? ""}`.trimEnd(),
          onclick: doUpdate,
          disabled: pluginBusy,
        });
      }
      items.push({
        label: "Uninstall plugin",
        onclick: () => (confirmUninstall = true),
        danger: true,
        disabled: pluginBusy,
      });
    }
    return items;
  });

  const inactiveCls = "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600";

  function statusChip(row: ConnectorRow): { label: string; cls: string } {
    // Connector type off → every instance reads "Inactive", not "Published",
    // so the green chip can't imply a row is live while the type is disabled.
    if (data?.disabled_type) {
      return { label: "Inactive", cls: inactiveCls };
    }
    if (row.disabled) {
      return { label: "Disabled", cls: inactiveCls };
    }
    if (row.status === "needs_setup") {
      return { label: "Needs setup", cls: "bg-prog-100 text-prog-400" };
    }
    return { label: "Published", cls: "bg-pos-100 text-pos-400" };
  }

  let rows = $derived(data?.rows ?? []);

  let mcpChip = $derived.by(() => {
    if (!data?.mcp || !data.mcp_status) return null;
    switch (data.mcp_status) {
      case "connected":
        return { label: "Connected", cls: "bg-pos-100 text-pos-400", dot: "bg-pos-400" };
      case "disconnected":
        return { label: "Disconnected", cls: "bg-neg-100 text-neg-400", dot: "bg-neg-400" };
      default:
        return { label: "Never tested", cls: "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600", dot: "bg-black-700" };
    }
  });

  $effect(() => {
    if (data) setBreadcrumbNames({ connector: data.name });
  });

  $effect(() => {
    load();
    loadPlugin();
    return clearBreadcrumbNames;
  });
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if error}
  <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
{:else if data}
  <div class="space-y-6">
    {#if data.disabled_type}
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-neg-400 bg-neg-100 px-4 py-3 text-sm text-neg-400">
        <span class="font-medium">This connector is disabled — hidden from the LLM. Every instance below is inactive until you re-enable it.</span>
        <Button size="sm" variant="secondary" disabled={typeBusy} onclick={toggleType}>{typeBusy ? "Enabling…" : "Enable connector"}</Button>
      </div>
    {/if}
    {#if data.needs_reload}
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-cau-400 bg-cau-100 px-4 py-3 text-sm text-cau-400">
        <span class="font-medium">Definition updated — reload to apply the latest changes.</span>
        <Button size="sm" disabled={reloadBusy} onclick={reloadDef}>{reloadBusy ? "Reloading…" : "Reload"}</Button>
      </div>
    {/if}
    <div class="flex items-start gap-4">

      <!-- LEFT: icon + name + description -->
      <div class="flex min-w-0 flex-1 items-start gap-3">
        <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-base text-green-700 dark:text-green-300" aria-hidden="true">{data.icon || "🔌"}</span>
        <div class="min-w-0 flex-1 overflow-hidden">
          <div class="flex flex-wrap items-center gap-2">
            <h1 class="text-lg font-bold text-black-900 dark:text-white-100">{data.name}</h1>
            {#if data.custom}
              <span class="flex-shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium text-green-500 border border-green-600/40 bg-green-900/20">Custom</span>
            {/if}
            {#if plugin}
              <span class="flex-shrink-0 rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[11px] font-medium text-black-800 dark:text-black-600">Plugin · v{plugin.version}</span>
            {/if}
            {#if data.disabled_type}
              <span class="flex-shrink-0 rounded-full bg-neg-100 px-2 py-0.5 text-[11px] font-medium text-neg-400">Disabled</span>
            {/if}
            {#if plugin?.update_available}
              <span class="flex-shrink-0 rounded-full bg-prog-100 px-2 py-0.5 text-[11px] font-medium text-prog-400">Update available · v{plugin.latest_version}</span>
            {/if}
            {#if mcpChip}
              <span class="inline-flex flex-shrink-0 items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium {mcpChip.cls}"><span class="h-1.5 w-1.5 rounded-full {mcpChip.dot}"></span>{mcpChip.label}</span>
            {/if}
          </div>
          {#if data.description}
            <p class="mt-0.5 break-words text-sm text-black-800 dark:text-black-600 line-clamp-3">{data.description}</p>
          {/if}
          <p class="mt-1 text-xs text-black-700 dark:text-black-600">{data.op_count} operation(s) · {rows.length} row(s)</p>
        </div>
      </div>
      <div class="flex flex-shrink-0 items-center gap-2 pt-1">
        {#if data.mcp}
          <button
            type="button"
            disabled={resyncBusy}
            onclick={resyncTools}
            class="whitespace-nowrap rounded-lg border border-white-400 dark:border-navy-600 px-4 py-2 text-sm font-medium text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600 disabled:opacity-50 disabled:cursor-not-allowed"
          >{resyncBusy ? "Syncing…" : "Re-sync tools"}</button>
        {/if}
        {#if data.custom && data.def_id}
          <button
            type="button"
            onclick={() => push(`/custom/${encodeURIComponent(data!.def_id!)}/edit`)}
            class="whitespace-nowrap rounded-lg border border-white-400 dark:border-navy-600 px-4 py-2 text-sm font-medium text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600"
          >Edit definition</button>
        {/if}
        {#if !data.fixed}
          <button
            type="button"
            disabled={busy}
            onclick={newRow}
            class="whitespace-nowrap rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50 disabled:cursor-not-allowed"
          >+ New row</button>
        {/if}
        <!-- Connector-type actions: Disable/Enable (all connectors) +
             Update/Uninstall (plugins only). Admin-only — hidden when empty. -->
        {#if headerMenu.length > 0}
          <KebabMenu items={headerMenu} ariaLabel="Connector actions" width={200} />
        {/if}
      </div>
    </div>

    <section>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Instances</h2>
      <p class="mt-1 text-sm text-black-800 dark:text-black-600">Each row carries its own credentials and label. MCP exposes one tool per (row × enabled operation).</p>
      {#if rows.length === 0}
        <div class="mt-4 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-8 text-center">
          <p class="text-sm text-black-700 dark:text-black-600">No rows yet. Click <strong>+ New row</strong> to create one.</p>
        </div>
      {:else}
        <!-- Dim the instances while the connector type is disabled — they're
             inert (hidden from the LLM) but still editable to fix config. -->
        <div class="mt-4 flex flex-col gap-2 pb-6 {data.disabled_type ? 'opacity-60' : ''}">
          {#each rows as row (row.id)}
            {@const chip = statusChip(row)}
            <div class="group relative rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 hover:border-green-400">
              <div class="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:gap-4">
                <button type="button" class="absolute inset-0 z-0 rounded-xl" aria-label={`Open ${row.label}`} onclick={() => push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(row.id)}`)}></button>
                <div class="pointer-events-none relative min-w-0 flex-1">
                  <span class="block truncate font-medium text-black-900 dark:text-white-100 group-hover:text-green-600">{row.label}</span>
                  <span class="mt-0.5 block truncate font-mono text-[10px] text-black-700 dark:text-black-600">{row.id}</span>
                </div>
                <div class="pointer-events-auto relative z-10 flex flex-wrap items-center gap-2 sm:flex-shrink-0">
                  {#if (row.tags ?? []).length === 0}
                    {#if row.private}
                      <span class="inline-flex items-center gap-1 rounded-md border border-white-400 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-2 py-0.5 text-[11px] text-black-800 dark:text-black-600" title="Visible to its owner and admins only. An admin can add a tag to share it.">
                        <svg class="h-3 w-3 flex-shrink-0" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24" aria-hidden="true"><rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/></svg>
                        Private
                      </span>
                    {:else}
                      <span class="rounded-md border border-dashed border-white-400 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-700 dark:text-black-600">Everyone</span>
                    {/if}
                  {:else}
                    {#each row.tags ?? [] as tag (tag)}
                      <span class="inline-flex min-w-0 max-w-[12rem] items-center gap-1 rounded-md border border-white-400 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-2 py-0.5 text-[11px] text-black-800 dark:text-black-600">
                        <svg class="h-3 w-3 flex-shrink-0" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24" aria-hidden="true"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/></svg>
                        <span class="truncate">{tag}</span>
                      </span>
                    {/each}
                  {/if}
                  <span class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium {chip.cls}">{chip.label}</span>
                  {#if canConnect(row)}
                    <Button variant="primary" size="sm" disabled={connectingId === row.id} onclick={() => connect(row)}>
                      {connectingId === row.id ? "Connecting…" : connectLabel(row)}
                    </Button>
                  {/if}
                  <KebabMenu ariaLabel={`Actions for ${row.label}`} items={rowMenuItems(row)} />
                </div>
              </div>
              {#if (row.accounts ?? []).length}
                <div class="pointer-events-auto relative z-10 border-t border-white-300 dark:border-navy-600">
                  {#each row.accounts ?? [] as acc (acc.id)}
                    <div class="flex items-center justify-between gap-3 px-4 py-2.5">
                      <span class="flex min-w-0 items-center gap-2 text-sm text-black-800 dark:text-black-600">
                        <svg class="h-3.5 w-3.5 flex-shrink-0" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24" aria-hidden="true"><path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/></svg>
                        <span class="truncate font-medium">@{acc.display_name}</span>
                      </span>
                      {#if acc.can_manage}
                        <button
                          type="button"
                          class="flex-shrink-0 text-xs font-medium text-neg-400 hover:underline"
                          onclick={() => (confirmAcc = { row, acc })}
                        >Disconnect</button>
                      {/if}
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </section>
  </div>

  <ConfirmDialog
    open={confirmRow !== null}
    title="Delete this connector row?"
    body="Run history is kept for audit. This cannot be undone."
    confirmLabel="Delete"
    destructive
    onConfirm={confirmDelete}
    onCancel={() => (confirmRow = null)}
  />

  <ConfirmDialog
    open={confirmAcc !== null}
    title="Disconnect this account?"
    body="The stored OAuth token is removed. The user can reconnect at any time."
    confirmLabel="Disconnect"
    destructive
    onConfirm={confirmDisconnectAccount}
    onCancel={() => (confirmAcc = null)}
  />

  <ConfirmDialog
    open={confirmUninstall}
    title="Uninstall this plugin?"
    body="The plugin binary is removed from disk. Instances and their config stay and become inert; reinstall to restore them."
    confirmLabel="Uninstall"
    destructive
    onConfirm={confirmDoUninstall}
    onCancel={() => (confirmUninstall = false)}
  />
{/if}