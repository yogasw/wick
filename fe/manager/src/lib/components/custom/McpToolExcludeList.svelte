<script lang="ts">
  /* The opt-out tool list: every discovered tool is exposed as an
     operation unless its name is in `excluded`. Toggling a tool flips
     its membership; the parent owns the excluded set and persists it.
     Mirrors the renderTools loop in custom_mcp_form.js. */
  import type { McpTool } from "$lib/types.js";

  type Props = {
    tools: McpTool[];
    excluded: string[];
    onChange: (excluded: string[]) => void;
  };
  let { tools, excluded, onChange }: Props = $props();

  let excludedSet = $derived(new Set(excluded));

  function toggle(name: string) {
    const next = new Set(excludedSet);
    if (next.has(name)) {
      next.delete(name);
    } else {
      next.add(name);
    }
    onChange(Array.from(next));
  }
</script>

<div class="space-y-1.5" data-cc-tools>
  {#if (tools ?? []).length === 0}
    <p class="text-[11px] text-black-700 dark:text-black-600">Run Test now to discover this server's tools.</p>
  {:else}
    {#each tools as t (t.name)}
      {@const isExcluded = excludedSet.has(t.name)}
      <div
        class="flex items-start gap-3 rounded-lg border px-3 py-2 {isExcluded
          ? 'border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 opacity-60'
          : 'border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700'}"
      >
        <div class="min-w-0 flex-1">
          <p class="font-mono text-xs font-semibold text-black-900 dark:text-white-100 {isExcluded ? 'line-through' : ''}">{t.name}</p>
          {#if t.description}
            <p class="mt-0.5 text-[11px] leading-relaxed text-black-700 dark:text-black-600">{t.description}</p>
          {/if}
        </div>
        <button
          type="button"
          class="flex-shrink-0 rounded-lg border px-2.5 py-1 text-[11px] font-medium {isExcluded
            ? 'border-green-400 text-green-600 hover:bg-green-100 dark:hover:bg-green-800'
            : 'border-white-400 dark:border-navy-600 text-black-700 dark:text-black-600 hover:border-neg-400 hover:text-neg-400'}"
          onclick={() => toggle(t.name)}
        >{isExcluded ? "Include" : "Exclude"}</button>
      </div>
    {/each}
  {/if}
</div>
