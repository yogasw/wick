<script lang="ts">
  /* Operations table for the connector detail page. When the caller may
     configure the row, each op gets an enable/disable toggle (optimistic,
     reverts on failure), a per-row select checkbox, and a selection-aware
     bulk bar (Enable/Disable selected) that falls back to Enable/Disable all
     when nothing is selected. A header search box filters by name/key/
     description and client-side pagination (page size 10) slices the list.
     Every op row carries a per-op Test + History deep-link. Mirrors the
     legacy connector_ops.templ OpsSection (showActions=true) + the
     connector_detail.templ opSectionScript progressive-enhancement JS. */
  import { Button } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { push } from "$lib/router.js";
  import { toggleConnectorOperation, bulkToggleOperations } from "$lib/api.js";
  import type { ConnectorOp } from "$lib/types.js";

  const PAGE_SIZE = 10;

  type Props = {
    operations: ConnectorOp[];
    connectorKey: string;
    connectorId: string;
    canConfigure: boolean;
  };
  let { operations, connectorKey, connectorId, canConfigure }: Props = $props();

  let ops = $state<ConnectorOp[]>([]);
  let busy = $state<Record<string, boolean>>({});
  let bulkBusy = $state(false);
  let query = $state("");
  let page = $state(1);
  let selected = $state<Record<string, boolean>>({});

  $effect(() => {
    ops = operations.map((o) => ({ ...o }));
  });

  const filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    if (!q) return ops;
    return ops.filter((o) =>
      o.name.toLowerCase().includes(q) ||
      o.key.toLowerCase().includes(q) ||
      o.description.toLowerCase().includes(q),
    );
  });

  const totalPages = $derived(Math.max(1, Math.ceil(filtered.length / PAGE_SIZE)));
  const clampedPage = $derived(Math.min(page, totalPages));
  const start = $derived((clampedPage - 1) * PAGE_SIZE);
  const end = $derived(Math.min(start + PAGE_SIZE, filtered.length));
  const pageOps = $derived(filtered.slice(start, end));
  const showingFrom = $derived(filtered.length ? start + 1 : 0);
  const selectedKeys = $derived(ops.filter((o) => selected[o.key]).map((o) => o.key));
  const pageAllSelected = $derived(pageOps.length > 0 && pageOps.every((o) => selected[o.key]));

  $effect(() => {
    query;
    page = 1;
  });

  function prevPage(): void {
    if (clampedPage > 1) page = clampedPage - 1;
  }

  function nextPage(): void {
    if (clampedPage < totalPages) page = clampedPage + 1;
  }

  function toggleRow(opKey: string): void {
    selected = { ...selected, [opKey]: !selected[opKey] };
  }

  function togglePageAll(checked: boolean): void {
    const next = { ...selected };
    for (const o of pageOps) {
      next[o.key] = checked;
    }
    selected = next;
  }

  function testOp(opKey: string): void {
    push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(connectorId)}/test?op=${encodeURIComponent(opKey)}`);
  }

  function historyOp(opKey: string): void {
    push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(connectorId)}/history?op=${encodeURIComponent(opKey)}`);
  }

  async function toggleEnabled(op: ConnectorOp): Promise<void> {
    if (busy[op.key]) return;
    const next = !op.enabled;
    busy = { ...busy, [op.key]: true };
    ops = ops.map((o) => (o.key === op.key ? { ...o, enabled: next, system_disabled: next ? false : o.system_disabled } : o));
    try {
      const saved = await toggleConnectorOperation(connectorKey, connectorId, op.key, next);
      ops = ops.map((o) => (o.key === op.key ? { ...o, enabled: saved } : o));
    } catch (e) {
      ops = ops.map((o) => (o.key === op.key ? { ...o, enabled: !next } : o));
      toastError("Toggle failed", e instanceof Error ? e.message : String(e));
    } finally {
      busy = { ...busy, [op.key]: false };
    }
  }

  async function bulkSet(enabled: boolean): Promise<void> {
    if (bulkBusy) return;
    const keys = selectedKeys;
    bulkBusy = true;
    try {
      await bulkToggleOperations(connectorKey, connectorId, enabled, keys);
      const target = new Set(keys);
      ops = ops.map((o) =>
        keys.length === 0 || target.has(o.key)
          ? { ...o, enabled, system_disabled: enabled ? false : o.system_disabled }
          : o,
      );
      const noun = keys.length === 0 ? "operations" : `${keys.length} operation${keys.length === 1 ? "" : "s"}`;
      toastOk(enabled ? `Enabled ${noun}` : `Disabled ${noun}`);
      selected = {};
    } catch (e) {
      toastError("Bulk update failed", e instanceof Error ? e.message : String(e));
    } finally {
      bulkBusy = false;
    }
  }
</script>

<section class="mt-8">
  {#if ops.length === 0}
    <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Operations</h2>
    <p class="mt-1 text-sm text-black-700 dark:text-black-600">This connector exposes no operations.</p>
  {:else}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
      <div class="flex flex-wrap items-center gap-2 px-4 py-3 border-b border-white-300 dark:border-navy-600">
        <h2 class="text-base font-semibold text-black-900 dark:text-white-100 mr-1">Operations</h2>
        <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[11px] font-medium text-black-700 dark:text-black-600">{ops.length}</span>
        <input
          type="search"
          aria-label="Search operations"
          placeholder="Search…"
          bind:value={query}
          class="ml-2 w-40 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-1.5 text-xs text-black-900 dark:text-white-100 outline-none focus:border-green-500"
        />
        {#if canConfigure}
          {#if selectedKeys.length > 0}
            <div class="ml-auto flex items-center gap-2">
              <span class="text-xs text-black-700 dark:text-black-600">{selectedKeys.length} selected</span>
              <button type="button" disabled={bulkBusy} class="rounded-lg border border-pos-300 px-3 py-1.5 text-xs font-medium text-pos-400 hover:bg-pos-100 disabled:opacity-50" onclick={() => bulkSet(true)}>Enable selected</button>
              <button type="button" disabled={bulkBusy} class="rounded-lg border border-neg-300 px-3 py-1.5 text-xs font-medium text-neg-400 hover:bg-neg-100 disabled:opacity-50" onclick={() => bulkSet(false)}>Disable selected</button>
            </div>
          {:else}
            <div class="ml-auto flex items-center gap-2">
              <Button variant="secondary" size="sm" disabled={bulkBusy} onclick={() => bulkSet(true)}>Enable all</Button>
              <Button variant="secondary" size="sm" disabled={bulkBusy} onclick={() => bulkSet(false)}>Disable all</Button>
            </div>
          {/if}
        {/if}
      </div>
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
              {#if canConfigure}
                <th class="w-8 px-4 py-3">
                  <input
                    type="checkbox"
                    aria-label="Select all on this page"
                    class="h-3.5 w-3.5 rounded accent-green-500"
                    checked={pageAllSelected}
                    onchange={(e) => togglePageAll((e.target as HTMLInputElement).checked)}
                  />
                </th>
              {/if}
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Operation</th>
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Description</th>
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Actions</th>
              <th class="px-4 py-3 text-right font-medium text-black-800 dark:text-black-600">Enabled</th>
            </tr>
          </thead>
          <tbody>
            {#each pageOps as op (op.key)}
              <tr class="border-b border-white-300 dark:border-navy-600 last:border-0 align-top">
                {#if canConfigure}
                  <td class="w-8 px-4 py-3">
                    <input
                      type="checkbox"
                      aria-label={`Select ${op.name}`}
                      class="h-3.5 w-3.5 rounded accent-green-500"
                      checked={!!selected[op.key]}
                      onchange={() => toggleRow(op.key)}
                    />
                  </td>
                {/if}
                <td class="px-4 py-3">
                  <div class="flex items-center gap-2">
                    <span class="font-medium text-black-900 dark:text-white-100">{op.name}</span>
                    {#if op.destructive}
                      <span class="rounded-full bg-neg-100 px-2 py-0.5 text-[10px] font-medium text-neg-400">destructive</span>
                    {/if}
                  </div>
                  <p class="mt-0.5 font-mono text-[10px] text-black-700 dark:text-black-600">{op.key}</p>
                </td>
                <td class="px-4 py-3 text-sm text-black-800 dark:text-black-600">{op.description}</td>
                <td class="px-4 py-3">
                  <div class="flex items-center gap-2">
                    <button
                      type="button"
                      class="rounded-lg border border-green-400 px-3 py-1.5 text-xs font-medium text-green-600 hover:bg-green-100 dark:hover:bg-green-800"
                      onclick={() => testOp(op.key)}
                    >Test</button>
                    <button
                      type="button"
                      class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600"
                      onclick={() => historyOp(op.key)}
                    >History</button>
                  </div>
                </td>
                <td class="px-4 py-3 text-right">
                  <div class="inline-flex flex-col items-end gap-1.5">
                    {#if op.system_disabled}
                      <span class="inline-flex items-center gap-1 rounded-md border border-prog-300 bg-prog-100 px-2 py-0.5 text-[10px] font-medium text-prog-400" title={`Health check warning: ${op.system_disabled_reason}. Toggle on to override.`}>⚠ {op.system_disabled_reason}</span>
                    {/if}
                    {#if canConfigure}
                      <button
                        type="button"
                        role="switch"
                        aria-checked={op.enabled}
                        aria-label={`Enable ${op.name}`}
                        disabled={busy[op.key]}
                        onclick={() => toggleEnabled(op)}
                        class="relative inline-flex h-5 w-9 items-center rounded-full transition-colors disabled:opacity-50 {op.enabled ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
                      >
                        <span class="absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white-100 shadow transition-transform {op.enabled ? 'translate-x-4' : ''}"></span>
                      </button>
                    {:else if op.enabled && !op.system_disabled}
                      <span class="rounded-full bg-pos-100 px-2 py-0.5 text-[10px] font-medium text-pos-400">enabled</span>
                    {:else if op.enabled}
                      <span class="rounded-full bg-prog-100 px-2 py-0.5 text-[10px] font-medium text-prog-400">enabled (warning)</span>
                    {:else}
                      <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-700 dark:text-black-600">disabled</span>
                    {/if}
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <div class="flex items-center justify-between gap-2 border-t border-white-300 dark:border-navy-600 px-4 py-2 text-xs text-black-700 dark:text-black-600">
        <span>Showing {showingFrom}–{end} of {filtered.length}</span>
        <div class="flex gap-1">
          <button type="button" disabled={clampedPage <= 1} class="rounded px-2 py-1 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-40" onclick={prevPage}>← Prev</button>
          <button type="button" disabled={clampedPage >= totalPages} class="rounded px-2 py-1 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-40" onclick={nextPage}>Next →</button>
        </div>
      </div>
    </div>
  {/if}
</section>
