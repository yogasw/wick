<script lang="ts">
  /* Read-only operations list for the connector detail page. Shows each op's
     name, key, destructive hint, description, and effective state (enabled +
     any health-check system-disable warning). Toggling is deferred to a later
     phase — this mirrors the legacy table's display, not its mutations. */
  import type { ConnectorOp } from "$lib/types.js";

  type Props = { operations: ConnectorOp[] };
  let { operations }: Props = $props();
</script>

<section class="mt-8">
  {#if operations.length === 0}
    <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Operations</h2>
    <p class="mt-1 text-sm text-black-700 dark:text-black-600">This connector exposes no operations.</p>
  {:else}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
      <div class="flex items-center gap-2 px-4 py-3 border-b border-white-300 dark:border-navy-600">
        <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Operations</h2>
        <span class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[11px] font-medium text-black-700 dark:text-black-600">{operations.length}</span>
      </div>
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Operation</th>
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Description</th>
              <th class="px-4 py-3 text-right font-medium text-black-800 dark:text-black-600">Enabled</th>
            </tr>
          </thead>
          <tbody>
            {#each operations as op (op.key)}
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
                <td class="px-4 py-3 text-right">
                  <div class="inline-flex flex-col items-end gap-1.5">
                    {#if op.system_disabled}
                      <span class="inline-flex items-center gap-1 rounded-md border border-prog-300 bg-prog-100 px-2 py-0.5 text-[10px] font-medium text-prog-400" title={`Health check warning: ${op.system_disabled_reason}`}>⚠ {op.system_disabled_reason}</span>
                    {/if}
                    {#if op.enabled && !op.system_disabled}
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
    </div>
  {/if}
</section>
