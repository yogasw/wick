<script lang="ts">
  /* Per-connector-type instance list: every row (instance) of one connector,
     with status chips, a "+ New row" action, and per-row disable/delete from a
     kebab menu. Clicking a row opens its detail page via the SPA router. Mirrors
     the legacy connector_list.templ surface. */
  import { Button, ConfirmDialog } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { push } from "$lib/router.js";
  import {
    getConnector,
    createConnectorRow,
    toggleConnectorDisabled,
    duplicateConnectorRow,
    deleteConnectorRow,
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

  function statusChip(row: ConnectorRow): { label: string; cls: string; dot: string } {
    if (row.disabled) return { label: "Disabled", cls: "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600", dot: "bg-black-700" };
    if (row.status === "needs_setup") return { label: "Needs setup", cls: "bg-prog-100 text-prog-400", dot: "bg-prog-400" };
    return { label: "Published", cls: "bg-pos-100 text-pos-400", dot: "bg-pos-400" };
  }

  let rows = $derived(data?.rows ?? []);

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
    <div class="grid grid-cols-[1fr_auto] items-start gap-4">
      <div class="flex min-w-0 items-start gap-3">
        <span class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg bg-navy-700 dark:bg-navy-700 text-base" aria-hidden="true">{data.icon || "🔌"}</span>
        <div class="min-w-0 w-full">
          <div class="flex flex-wrap items-center gap-2">
            <h1 class="text-[1.375rem] font-semibold text-black-900 dark:text-white-100">{data.name}</h1>
            {#if data.custom}
              <span class="rounded px-1.5 py-0.5 text-[11px] font-medium text-green-500 border border-green-600/40 bg-green-900/20">Custom</span>
            {/if}
          </div>
          {#if data.description}
            <p class="mt-0.5 text-sm text-black-800 dark:text-black-600">{data.description}</p>
          {/if}
          <p class="mt-1 text-xs text-black-700 dark:text-black-600">{data.op_count} operation(s) · {rows.length} row(s)</p>
        </div>
      </div>
      <div class="flex flex-shrink-0 items-start gap-2">
        {#if data.custom && data.def_id}
          <button
            type="button"
            onclick={() => push(`/custom/${encodeURIComponent(data!.def_id!)}/edit`)}
            class="inline-flex items-center rounded-lg border border-white-300 dark:border-navy-600 bg-transparent px-3 py-2 text-sm font-semibold text-black-800 dark:text-white-100 hover:border-green-400 hover:text-green-600 transition-colors leading-tight text-center"
          >Edit<br/>definition</button>
        {/if}
        {#if !data.fixed}
          <Button size="sm" disabled={busy} onclick={newRow}>+ New row</Button>
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
                  <span class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium {chip.cls}"><span class="h-1.5 w-1.5 rounded-full {chip.dot}"></span>{chip.label}</span>
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
