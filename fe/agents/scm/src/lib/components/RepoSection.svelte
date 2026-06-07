<script lang="ts">
  import type { RepoSummary } from "$lib/api/scm";

  type Props = {
    repos: RepoSummary[];
    activeRepo: string;
    onSelect: (rel: string) => void;
  };
  let { repos, activeRepo, onSelect }: Props = $props();

  let open = $state(false); // default collapsed — expand manually
</script>

<div class="border-b border-white-300 dark:border-navy-600">
  <button type="button" onclick={() => (open = !open)} class="flex w-full items-center gap-1.5 px-2 py-1.5 text-left">
    <svg class={"h-3 w-3 shrink-0 text-black-600 transition-transform " + (open ? "rotate-90" : "")} fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7"/></svg>
    <span class="text-[10px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">Repositories</span>
    <span class="rounded-full bg-white-300 px-1.5 text-[10px] font-semibold text-black-700 dark:bg-navy-600 dark:text-black-600">{repos.length}</span>
  </button>
  {#if open}
    <div class="pb-1">
      {#each repos as r (r.rel)}
        <button
          type="button"
          onclick={() => onSelect(r.rel)}
          class={"flex w-full items-center gap-2 px-3 py-1.5 text-left transition-colors " +
            (activeRepo === r.rel ? "bg-green-100 dark:bg-green-900/30" : "hover:bg-white-200 dark:hover:bg-navy-800")}
        >
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-black-600" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M2 6h12" stroke-linecap="round"/></svg>
          <span class={"min-w-0 flex-1 truncate text-xs font-medium " + (activeRepo === r.rel ? "text-black-900 dark:text-white-100" : "text-black-800 dark:text-black-600")}>{r.name}</span>
          {#if r.branch}
            <span class="flex shrink-0 items-center gap-0.5 text-[10px] text-black-600 dark:text-black-700">
              <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
              {r.branch}{r.ahead > 0 ? `↑${r.ahead}` : ""}{r.behind > 0 ? `↓${r.behind}` : ""}
            </span>
          {/if}
          {#if r.changed > 0}
            <span class="shrink-0 rounded-full bg-green-500 px-1.5 text-[10px] font-semibold text-white-100">{r.changed}</span>
          {/if}
        </button>
      {/each}
    </div>
  {/if}
</div>
