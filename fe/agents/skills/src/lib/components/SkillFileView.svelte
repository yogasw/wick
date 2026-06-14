<script lang="ts">
  import { onMount } from "svelte";
  import { renderMarkdown } from "@wick-fe/common-md";
  import { getSkillFile } from "$lib/api.js";
  import type { SkillFileDetailResponse } from "$lib/types.js";

  type Props = {
    folder: string;
    file: string;
    onBack: () => void;
  };
  let { folder, file, onBack }: Props = $props();

  let data = $state<SkillFileDetailResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);

  function dirLabel(dir: string): string {
    const clean = dir.replace(/\\/g, "/").replace(/\/$/, "");
    const parts = clean.split("/");
    for (let i = parts.length - 1; i >= 0; i--) {
      const seg = parts[i].replace(/^\./, "");
      if (seg && seg !== "skills") return seg;
    }
    return dir;
  }

  onMount(async () => {
    loading = true;
    error = null;
    try {
      data = await getSkillFile(folder, file);
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load";
    } finally {
      loading = false;
    }
  });
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <div class="flex items-center gap-2 text-sm flex-wrap">
      <button onclick={onBack} class="text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100">← {folder}</button>
      <span class="text-black-500">/</span>
      <span class="font-mono font-semibold text-black-900 dark:text-white-100">{file}</span>
    </div>
    {#if data}
      <div class="flex flex-wrap gap-1">
        {#each data.in_dirs as dir}
          <span class="rounded-full bg-green-100 dark:bg-green-900/30 border border-green-300 dark:border-green-700 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-400" title={dir}>{dirLabel(dir)}</span>
        {/each}
      </div>
    {/if}
  </div>

  {#if loading}
    <div class="text-sm text-black-600 dark:text-black-500">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if data}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden flex flex-col" style="min-height: calc(100vh - 160px)">
      <div class="px-4 py-2 border-b border-white-300 dark:border-navy-600 text-xs text-black-600 dark:text-black-500 shrink-0">{data.source_path}</div>
      <div class="px-5 py-4 space-y-1 overflow-x-auto flex-1">
        {@html renderMarkdown(data.content)}
      </div>
    </div>
  {/if}
</div>
