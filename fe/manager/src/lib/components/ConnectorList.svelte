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
    <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
      <div class="flex items-center gap-3">
        <span class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-lg" aria-hidden="true">{data.icon || "🔌"}</span>
        <div>
          <div class="flex flex-wrap items-center gap-2">
            <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">{data.name}</h1>
            {#if data.custom}
              <span class="rounded bg-blue-100 dark:bg-blue-900/40 px-1.5 py-0.5 text-[10px] font-medium text-blue-700 dark:text-blue-300">Custom</span>
            {/if}
          </div>
          {#if data.description}
            <p class="mt-0.5 text-sm text-black-800 dark:text-black-600">{data.description}</p>
          {/if}
          <p class="mt-1 text-xs text-black-700 dark:text-black-600">{data.op_count} operation(s) · {rows.length} row(s)</p>
        </div>
      </div>
      {#if !data.fixed}
        <Button size="lg" disabled={busy} onclick={newRow}>+ New row</Button>
      {/if}
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
            <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 hover:border-green-400">
              <div class="flex flex-col gap-2 px-4 py-3 sm:flex-row sm:items-center sm:gap-4">
                <button
                  type="button"
                  class="min-w-0 flex-1 text-left"
                  onclick={() => push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(row.id)}`)}
                >
                  <span class="block truncate font-medium text-black-900 dark:text-white-100 hover:text-green-600">{row.label}</span>
                  <span class="mt-0.5 block truncate font-mono text-[10px] text-black-700 dark:text-black-600">{row.id}</span>
                </button>
                <div class="flex flex-wrap items-center gap-2 sm:flex-shrink-0">
                  {#each row.tags ?? [] as tag (tag)}
                    <span class="rounded-md border border-white-400 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-2 py-0.5 text-[11px] text-black-800 dark:text-black-600">{tag}</span>
                  {/each}
                  <span class="inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-medium {chip.cls}">{chip.label}</span>
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
