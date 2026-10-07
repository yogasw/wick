<script lang="ts">
  import { untrack } from "svelte";
  import Self from "./JsonTree.svelte";

  type Props = {
    value: unknown;
    name?: string;
    depth?: number;
  };
  let { value, name, depth = 0 }: Props = $props();

  const isArray = $derived(Array.isArray(value));
  const isObject = $derived(value !== null && typeof value === "object" && !Array.isArray(value));
  const isContainer = $derived(isArray || isObject);

  const entries = $derived.by<[string, unknown][]>(() => {
    if (Array.isArray(value)) return (value as unknown[]).map((v, i) => [String(i), v]);
    if (value !== null && typeof value === "object") return Object.entries(value as Record<string, unknown>);
    return [];
  });

  const preview = $derived(isArray ? `[ ${entries.length} ]` : isObject ? `{ ${entries.length} }` : "");

  let expanded = $state(untrack(() => depth < 2));

  function valueClass(v: unknown): string {
    if (v === null) return "text-black-600 dark:text-black-500";
    if (typeof v === "string") return "text-emerald-700 dark:text-emerald-400";
    if (typeof v === "number") return "text-amber-700 dark:text-amber-400";
    if (typeof v === "boolean") return "text-violet-700 dark:text-violet-400";
    return "text-black-900 dark:text-white-100";
  }

  function valueText(v: unknown): string {
    if (v === null) return "null";
    if (typeof v === "string") return JSON.stringify(v);
    return String(v);
  }
</script>

<div class="font-mono text-xs leading-relaxed">
  {#if isContainer}
    <button
      type="button"
      onclick={() => { expanded = !expanded; }}
      class="inline-flex w-full items-start gap-1 rounded px-0.5 text-left hover:bg-white-300/60 dark:hover:bg-navy-800/60 transition-colors"
    >
      <span class="inline-block w-3 shrink-0 select-none text-black-600 dark:text-black-500">{expanded ? "▾" : "▸"}</span>
      <span class="min-w-0 break-all">
        {#if name !== undefined}<span class="text-sky-700 dark:text-sky-400">{name}</span><span class="text-black-600 dark:text-black-500">: </span>{/if}<span class="text-black-600 dark:text-black-500">{preview}</span>
      </span>
    </button>
    {#if expanded}
      <div class="ml-2 border-l border-white-300 dark:border-navy-600 pl-2">
        {#each entries as [k, v] (k)}
          <Self value={v} name={k} depth={depth + 1} />
        {/each}
      </div>
    {/if}
  {:else}
    <div class="break-all pl-4">
      {#if name !== undefined}<span class="text-sky-700 dark:text-sky-400">{name}</span><span class="text-black-600 dark:text-black-500">: </span>{/if}<span class={valueClass(value)}>{valueText(value)}</span>
    </div>
  {/if}
</div>
