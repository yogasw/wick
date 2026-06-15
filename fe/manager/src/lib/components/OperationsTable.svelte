<script lang="ts">
  /* Operations table for the connector detail page. When the caller may
     configure the row, each op gets an enable/disable toggle (optimistic,
     reverts on failure) plus bulk Enable/Disable-all; admins additionally
     get a per-op admin-only toggle. Mirrors the legacy connector_ops.templ
     OpsSection + the operation toggle / bulk / admin-only POST routes. */
  import { Button } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { push } from "$lib/router.js";
  import {
    toggleConnectorOperation,
    bulkToggleOperations,
    toggleOperationAdminOnly,
  } from "$lib/api.js";
  import type { ConnectorOp } from "$lib/types.js";

  type Props = {
    operations: ConnectorOp[];
    connectorKey: string;
    connectorId: string;
    canConfigure: boolean;
    isAdmin: boolean;
    onchanged?: () => void;
  };
  let { operations, connectorKey, connectorId, canConfigure, isAdmin, onchanged }: Props = $props();

  let ops = $state<ConnectorOp[]>([]);
  let busy = $state<Record<string, boolean>>({});
  let bulkBusy = $state(false);

  $effect(() => {
    ops = operations.map((o) => ({ ...o }));
  });

  function testOp(opKey: string): void {
    push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(connectorId)}/test?op=${encodeURIComponent(opKey)}`);
  }

  async function toggleEnabled(op: ConnectorOp): Promise<void> {
    if (busy[op.key]) return;
    const next = !op.enabled;
    busy = { ...busy, [op.key]: true };
    ops = ops.map((o) => (o.key === op.key ? { ...o, enabled: next, system_disabled: next ? false : o.system_disabled } : o));
    try {
      const saved = await toggleConnectorOperation(connectorKey, connectorId, op.key, next);
      ops = ops.map((o) => (o.key === op.key ? { ...o, enabled: saved } : o));
      onchanged?.();
    } catch (e) {
      ops = ops.map((o) => (o.key === op.key ? { ...o, enabled: !next } : o));
      toastError("Toggle failed", e instanceof Error ? e.message : String(e));
    } finally {
      busy = { ...busy, [op.key]: false };
    }
  }

  async function toggleAdminOnly(op: ConnectorOp): Promise<void> {
    if (busy[op.key]) return;
    const next = !op.admin_only;
    busy = { ...busy, [op.key]: true };
    ops = ops.map((o) => (o.key === op.key ? { ...o, admin_only: next } : o));
    try {
      const saved = await toggleOperationAdminOnly(connectorKey, connectorId, op.key, next);
      ops = ops.map((o) => (o.key === op.key ? { ...o, admin_only: saved } : o));
    } catch (e) {
      ops = ops.map((o) => (o.key === op.key ? { ...o, admin_only: !next } : o));
      toastError("Update failed", e instanceof Error ? e.message : String(e));
    } finally {
      busy = { ...busy, [op.key]: false };
    }
  }

  async function bulkSet(enabled: boolean): Promise<void> {
    if (bulkBusy) return;
    bulkBusy = true;
    try {
      await bulkToggleOperations(connectorKey, connectorId, enabled);
      ops = ops.map((o) => ({ ...o, enabled, system_disabled: enabled ? false : o.system_disabled }));
      toastOk(enabled ? "All operations enabled" : "All operations disabled");
      onchanged?.();
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
        <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Operations</h2>
        <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[11px] font-medium text-black-700 dark:text-black-600">{ops.length}</span>
        {#if canConfigure}
          <div class="ml-auto flex items-center gap-2">
            <Button variant="secondary" size="sm" disabled={bulkBusy} onclick={() => bulkSet(true)}>Enable all</Button>
            <Button variant="secondary" size="sm" disabled={bulkBusy} onclick={() => bulkSet(false)}>Disable all</Button>
          </div>
        {/if}
      </div>
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Operation</th>
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Description</th>
              {#if isAdmin}
                <th class="px-4 py-3 text-right font-medium text-black-800 dark:text-black-600">Admin-only</th>
              {/if}
              <th class="px-4 py-3 text-right font-medium text-black-800 dark:text-black-600">Enabled</th>
              <th class="px-4 py-3 text-right font-medium text-black-800 dark:text-black-600">Run</th>
            </tr>
          </thead>
          <tbody>
            {#each ops as op (op.key)}
              <tr class="border-b border-white-300 dark:border-navy-600 last:border-0 align-top">
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
                {#if isAdmin}
                  <td class="px-4 py-3 text-right">
                    <button
                      type="button"
                      role="switch"
                      aria-checked={op.admin_only}
                      aria-label={`Admin-only ${op.name}`}
                      disabled={busy[op.key]}
                      onclick={() => toggleAdminOnly(op)}
                      class="relative inline-flex h-5 w-9 items-center rounded-full transition-colors disabled:opacity-50 {op.admin_only ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
                    >
                      <span class="absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white-100 shadow transition-transform {op.admin_only ? 'translate-x-4' : ''}"></span>
                    </button>
                  </td>
                {/if}
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
                <td class="px-4 py-3 text-right">
                  <button type="button" class="rounded-md border border-white-400 dark:border-navy-600 px-2.5 py-1 text-[11px] font-medium text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600" onclick={() => testOp(op.key)}>Test</button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    </div>
  {/if}
</section>
