<script lang="ts">
  import type { WsInstance, WsBase, WsTombstone } from "../types/agents.js";
  import WsInstanceCard from "./WsInstanceCard.svelte";

  type TestResult = { ok: boolean; error?: string; no_health_check?: boolean } | null;

  type Props = {
    instances: WsInstance[];
    bases: WsBase[];
    deleted?: WsTombstone[];
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
    deleted = [],
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

  /* A tombstone's connector can be re-created only if its base is still one the
     user may add here (module capability + admin toggle). */
  const addableBases = $derived(new Set(bases.map((b) => b.base_key)));

  function deletedNote(t: WsTombstone): string {
    const when = t.deleted_at ? new Date(t.deleted_at).toLocaleString() : "";
    const why = t.reason ? ` (${t.reason})` : "";
    return `Deleted${why}${when ? ` · ${when}` : ""} — its config is gone`;
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

  {#if deleted.length > 0}
    <div class="space-y-2" data-testid="deleted-list">
      {#each deleted as t, i (t.label + t.deleted_at + i)}
        <div
          class="rounded-xl border border-dashed border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-900 px-3 py-2 opacity-80"
          data-testid="tombstone"
        >
          <div class="flex items-center gap-2">
            <span class="min-w-0 flex-1 truncate text-sm font-medium text-black-700 dark:text-black-600 line-through">
              {t.label || t.base_key}
            </span>
            {#if addableBases.has(t.base_key)}
              <button
                type="button"
                class="shrink-0 rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs font-medium text-black-800 dark:text-white-200 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
                onclick={() => onAdd(t.base_key)}
                data-testid="recreate-btn"
              >Re-create</button>
            {/if}
          </div>
          <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">{deletedNote(t)}</p>
        </div>
      {/each}
    </div>
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
