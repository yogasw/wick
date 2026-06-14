<script lang="ts">
  import { onMount } from "svelte";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { renderMarkdown } from "@wick-fe/common-md";
  import { getSkill, postMutation } from "$lib/api.js";
  import type { SkillDetailResponse } from "$lib/types.js";

  type Props = {
    name: string;
    onBack: () => void;
  };
  let { name, onBack }: Props = $props();

  let data = $state<SkillDetailResponse | null>(null);
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

  async function load() {
    loading = true;
    error = null;
    try {
      data = await getSkill(name);
    } catch (e) {
      error = e instanceof Error ? e.message : "Failed to load";
    } finally {
      loading = false;
    }
  }

  async function deleteAll() {
    try {
      await postMutation(`/skills/${name}/delete`);
      toastOk(`Deleted ${name}`);
      onBack();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Delete failed");
    }
  }

  async function syncAll() {
    try {
      await postMutation(`/skills/${name}/sync`);
      toastOk(`Synced ${name}`);
      await load();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Sync failed");
    }
  }

  onMount(load);
</script>

<div class="space-y-6">
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <div class="flex items-center gap-3">
      <button onclick={onBack} class="text-sm text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100">← Skills</button>
      <h1 class="font-mono text-base font-semibold text-black-900 dark:text-white-100">{name}</h1>
    </div>
    {#if data}
      <div class="flex items-center gap-2">
        <div class="flex flex-wrap gap-1">
          {#each data.in_dirs as dir}
            <span class="rounded-full bg-green-100 dark:bg-green-900/30 border border-green-300 dark:border-green-700 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-400" title={dir}>{dirLabel(dir)}</span>
          {/each}
        </div>
        <button onclick={syncAll} class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">Sync</button>
        <button onclick={deleteAll} class="rounded-lg border border-red-300 dark:border-red-700 px-3 py-2 text-xs text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20">Delete</button>
      </div>
    {/if}
  </div>

  {#if loading}
    <div class="text-sm text-black-600 dark:text-black-500">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if data?.is_dir}
    <div class="flex flex-wrap gap-2 items-center">
      <span class="text-xs text-black-700 dark:text-black-600">Present in:</span>
      {#each data.in_dirs as dir}
        <span class="rounded-full bg-green-100 dark:bg-green-900/30 border border-green-300 dark:border-green-700 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-400" title={dir}>{dirLabel(dir)}</span>
      {/each}
      {#each (data.missing_dirs ?? []) as dir}
        <span class="rounded-full bg-white-300 dark:bg-navy-600 border border-white-400 dark:border-navy-500 px-2 py-0.5 text-xs text-black-600 dark:text-black-500 line-through" title="{dir} (missing)">{dirLabel(dir)}</span>
      {/each}
    </div>
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
              <tr class="cursor-pointer hover:bg-white-200 dark:hover:bg-navy-800 transition-colors">
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
  {:else if data && !data.is_dir}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden flex flex-col" style="min-height: calc(100vh - 180px)">
      {#if data.source_path}
        <div class="px-4 py-2 border-b border-white-300 dark:border-navy-600 text-xs text-black-600 dark:text-black-500 shrink-0">{data.source_path}</div>
      {/if}
      <div class="px-5 py-4 space-y-1 overflow-x-auto flex-1">
        {@html renderMarkdown(data.content ?? "")}
      </div>
    </div>
  {/if}
</div>
