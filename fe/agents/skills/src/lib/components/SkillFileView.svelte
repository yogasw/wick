<script lang="ts">
  import { renderMarkdown } from "@wick-fe/common-md";
  import { getSkillFile } from "$lib/api.js";
  import type { SkillFileDetailResponse } from "$lib/types.js";

  type Props = {
    folder: string;
    file: string;
    onBack: () => void;
    onOpenChild: (childPath: string) => void;
  };
  let { folder, file, onBack, onOpenChild }: Props = $props();

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

  $effect(() => {
    const f = folder;
    const fl = file;
    loading = true;
    error = null;
    data = null;
    getSkillFile(f, fl)
      .then((d) => { data = d; })
      .catch((e) => { error = e instanceof Error ? e.message : "Failed to load"; })
      .finally(() => { loading = false; });
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
  {:else if data?.is_dir}
    {#if (data.entries ?? []).length === 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-12 text-center text-sm text-black-700 dark:text-black-600">Folder is empty.</div>
    {:else}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-white-300 dark:border-navy-600 text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">
              <th class="px-5 py-3 text-left">Name</th>
              <th class="px-5 py-3 text-left">Present In</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-white-300 dark:divide-navy-600">
            {#each (data.entries ?? []) as entry}
              {@const childPath = `${file}/${entry.name}`}
              <tr
                class="cursor-pointer hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
                role="button"
                tabindex="0"
                onclick={() => onOpenChild(childPath)}
                onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onOpenChild(childPath); } }}
              >
                <td class="px-5 py-3">
                  <div class="flex items-center gap-2">
                    {#if entry.is_dir}
                      <svg class="w-4 h-4 text-amber-500 shrink-0" fill="currentColor" viewBox="0 0 20 20"><path d="M2 6a2 2 0 012-2h5l2 2h5a2 2 0 012 2v6a2 2 0 01-2 2H4a2 2 0 01-2-2V6z"/></svg>
                      <span class="font-mono text-xs text-black-900 dark:text-white-100">{entry.name}/</span>
                    {:else}
                      <svg class="w-4 h-4 text-black-500 dark:text-black-600 shrink-0" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"/></svg>
                      <span class="font-mono text-xs text-black-900 dark:text-white-100">{entry.name}</span>
                    {/if}
                  </div>
                </td>
                <td class="px-5 py-3">
                  <div class="flex flex-wrap gap-1">
                    {#each entry.in_dirs as dir}
                      <span class="rounded-full bg-green-100 dark:bg-green-900/30 border border-green-300 dark:border-green-700 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-400" title={dir}>{dirLabel(dir)}</span>
                    {/each}
                    {#each entry.missing_dirs as dir}
                      <span class="rounded-full bg-white-300 dark:bg-navy-600 border border-white-400 dark:border-navy-500 px-2 py-0.5 text-xs text-black-600 dark:text-black-500 line-through">{dirLabel(dir)}</span>
                    {/each}
                  </div>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {:else if data}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden flex flex-col" style="min-height: calc(100vh - 160px)">
      <div class="px-4 py-2 border-b border-white-300 dark:border-navy-600 text-xs text-black-600 dark:text-black-500 shrink-0">{data.source_path}</div>
      <div class="px-5 py-4 space-y-1 overflow-x-auto flex-1">
        {@html renderMarkdown(data.content ?? "")}
      </div>
    </div>
  {/if}
</div>
