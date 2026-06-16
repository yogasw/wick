<script lang="ts">
  import { Button, ConfirmDialog } from "@wick-fe/common-ui";
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
  } from "$lib/api.js";
  import type { ConnectorList, ConnectorRow } from "$lib/types.js";
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

  function statusChip(row: ConnectorRow): { label: string; cls: string } {
    if (row.disabled) {
      return { label: "Disabled", cls: "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600" };
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
    return clearBreadcrumbNames;
  });
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if error}
  <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
{:else if data}
  <div class="space-y-6">
    {#if data.needs_reload}
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-cau-400 bg-cau-100 px-4 py-3 text-sm text-cau-400">
        <span class="font-medium">Definition updated — reload to apply the latest changes.</span>
        <Button size="sm" disabled={reloadBusy} onclick={reloadDef}>{reloadBusy ? "Reloading…" : "Reload"}</Button>
      </div>
    {/if}
    <div class="flex items-start gap-4">

      <!-- LEFT: icon + name + description -->
      <div class="flex min-w-0 flex-1 items-start gap-3">
        <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg bg-navy-700 dark:bg-navy-700 text-base" aria-hidden="true">{data.icon || "🔌"}</span>
        <div class="min-w-0 flex-1 overflow-hidden">
          <div class="flex flex-wrap items-center gap-2">
            <h1 class="text-lg font-bold text-black-900 dark:text-white-100">{data.name}</h1>
            {#if data.custom}
              <span class="flex-shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium text-green-500 border border-green-600/40 bg-green-900/20">Custom</span>
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
        <div class="mt-4 flex flex-col gap-2">
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
                    <span class="rounded-md border border-dashed border-white-400 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-700 dark:text-black-600">Everyone</span>
                  {:else}
                    {#each row.tags ?? [] as tag (tag)}
                      <span class="inline-flex min-w-0 max-w-[12rem] items-center gap-1 rounded-md border border-white-400 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-2 py-0.5 text-[11px] text-black-800 dark:text-black-600">
                        <svg class="h-3 w-3 flex-shrink-0" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24" aria-hidden="true"><path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/></svg>
                        <span class="truncate">{tag}</span>
                      </span>
                    {/each}
                  {/if}
                  <span class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium {chip.cls}">{chip.label}</span>
                  <Button variant="ghost" size="sm" onclick={() => push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(row.id)}/history`)}>History</Button>
                  <Button variant="ghost" size="sm" onclick={() => toggleDisabled(row)}>{row.disabled ? "Enable" : "Disable"}</Button>
                  {#if !data.fixed}
                    <Button variant="ghost" size="sm" disabled={busy} onclick={() => duplicateRow(row)}>Duplicate</Button>
                    <Button variant="ghost" size="sm" onclick={() => (confirmRow = row)}>Delete</Button>
                  {/if}
                </div>
              </div>
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
{/if}