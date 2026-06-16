<script lang="ts">
  import type { WsInstance, WsBase } from "../types/agents.js";
  import WsInstanceCard from "./WsInstanceCard.svelte";

  type TestResult = { ok: boolean; error?: string; no_health_check?: boolean } | null;

  type Props = {
    instances: WsInstance[];
    bases: WsBase[];
    openCards?: Record<string, boolean>;
    onAdd: (baseKey: string) => void;
    onSave: (cid: string, values: Record<string, string>) => void;
    onTest: (cid: string, config: Record<string, string>) => Promise<TestResult>;
    onRename: (cid: string, label: string) => void;
    onDuplicate: (cid: string) => void;
    onDelete: (cid: string) => void;
  };

  let {
    instances,
    bases,
    openCards = {},
    onAdd,
    onSave,
    onTest,
    onRename,
    onDuplicate,
    onDelete,
  }: Props = $props();

  const INPUT_CLASS =
    "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none";

  function handlePickerChange(e: Event) {
    const sel = e.currentTarget as HTMLSelectElement;
    const key = sel.value;
    sel.value = "";
    if (key) onAdd(key);
  }
</script>

<div class="flex-1 overflow-y-auto p-3 space-y-2">
  {#if instances.length === 0}
    <p class="text-xs text-black-700 dark:text-black-600">No session connectors yet.</p>
    {#if bases.length === 0}
      <p class="text-[11px] text-black-700 dark:text-black-600">
        No connector here is enabled for session instances. An admin turns this on per connector.
      </p>
    {/if}
  {:else}
    {#each instances as inst (inst.id)}
      <WsInstanceCard
        instance={inst}
        open={openCards[inst.id] ?? false}
        {onSave}
        {onTest}
        {onRename}
        {onDuplicate}
        {onDelete}
      />
    {/each}
  {/if}

  {#if bases.length > 0}
    <div class="pt-2 border-t border-white-300 dark:border-navy-600">
      <select class={INPUT_CLASS} onchange={handlePickerChange} data-testid="base-picker">
        <option value="">+ Add a session connector…</option>
        {#each bases as b (b.base_key)}
          <option value={b.base_key}>{b.label ?? b.base_key}</option>
        {/each}
      </select>
    </div>
  {/if}
</div>
