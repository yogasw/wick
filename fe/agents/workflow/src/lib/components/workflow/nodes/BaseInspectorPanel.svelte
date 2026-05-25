<script lang="ts">
  // Inspector panel shell — header + tabs + slotted body. Per-type
  // inspector components fill in `body` with their form fields.
  import type { Snippet } from "svelte";
  import type { Node, NodeType } from "$lib/types/workflow";

  type Props = {
    node: Node;
    title?: string;
    body?: Snippet;
    advanced?: Snippet;
    onSave?: () => void;
    onCancel?: () => void;
  };

  let { node, title, body, advanced, onSave, onCancel }: Props = $props();

  let activeTab = $state<"params" | "advanced" | "notes">("params");
</script>

<aside class="flex flex-col h-full w-[360px] border-l border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800">
  <header class="px-4 py-3 border-b border-white-300 dark:border-navy-600">
    <div class="text-[10px] uppercase tracking-wide text-black-500 dark:text-white-700">{node.type}</div>
    <div class="text-sm font-semibold text-black-900 dark:text-white-100">{title ?? node.label ?? node.id}</div>
  </header>

  <nav class="flex border-b border-white-300 dark:border-navy-600 text-xs">
    {#each ["params", "advanced", "notes"] as t}
      <button
        class="flex-1 py-2 capitalize transition-colors"
        class:bg-white-200={activeTab === t}
        class:dark:bg-navy-700={activeTab === t}
        class:text-emerald-600={activeTab === t}
        onclick={() => (activeTab = t as typeof activeTab)}
      >{t}</button>
    {/each}
  </nav>

  <div class="flex-1 overflow-y-auto p-4 space-y-3 text-xs">
    {#if activeTab === "params" && body}
      {@render body()}
    {:else if activeTab === "advanced"}
      {#if advanced}
        {@render advanced()}
      {:else}
        <p class="text-black-500 dark:text-white-700 italic">No advanced settings for {node.type}.</p>
      {/if}
    {:else if activeTab === "notes"}
      <label class="flex flex-col gap-1">
        <span class="font-medium">Description</span>
        <textarea
          class="rounded border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-2 min-h-[120px]"
          value={node.description ?? ""}
          placeholder="Free-form notes — not consumed by the executor."
        ></textarea>
      </label>
    {/if}
  </div>

  <footer class="flex gap-2 p-3 border-t border-white-300 dark:border-navy-600">
    <button class="px-3 py-1.5 rounded bg-emerald-500 text-white-100 text-xs font-medium" onclick={onSave}>Save</button>
    <button class="px-3 py-1.5 rounded text-xs text-black-700 dark:text-white-300" onclick={onCancel}>Cancel</button>
  </footer>
</aside>
