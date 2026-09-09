<script lang="ts">
  import type { RepoSummary } from "$lib/api/scm";

  type Props = {
    repos: RepoSummary[];
    activeRepo: string;
    onSelect: (rel: string) => void;
  };
  let { repos, activeRepo, onSelect }: Props = $props();

  let open = $state(false); // default collapsed — expand manually
  let query = $state("");
  const filtered = $derived.by(() => {
    const q = query.trim().toLowerCase();
    if (!q) return repos;
    return repos.filter((r) => r.name.toLowerCase().includes(q) || r.rel.toLowerCase().includes(q));
  });

  // The active repo, for the collapsed header. Closed is the default
  // state, and closed it said nothing about WHICH of the 58 repos the
  // panel below it belongs to — the only way to find out was to expand
  // the list and hunt for the checked row.
  const active = $derived(repos.find((r) => r.rel === activeRepo));

  let listEl = $state<HTMLDivElement | null>(null);

  /* Bring the selected repo into view. The list shows ~5 rows of what can be
     dozens of repos AND starts collapsed, so expanding it lands at scroll 0
     with the active repo almost always off-screen — the selection was there,
     just invisible until you scrolled for it.

     Re-runs on open, on a new selection, and after the search filter changes
     the rows. block:"nearest" scrolls the list only as far as it must and
     never moves the surrounding panel. The row is looked up by data-active
     rather than bound per-row, which keeps this out of the each block. */
  $effect(() => {
    if (!open) return;
    void activeRepo;
    void query;
    void filtered.length;
    const root = listEl;
    if (!root) return;
    requestAnimationFrame(() => {
      // Absent when the search box filtered the selection out — nothing to do.
      root.querySelector<HTMLElement>('[data-active="true"]')?.scrollIntoView({ block: "nearest" });
    });
  });
</script>

<div class="border-b border-white-300 dark:border-navy-600">
  <button type="button" onclick={() => (open = !open)} class="flex w-full items-center gap-1.5 px-2 py-1.5 text-left">
    <svg class={"h-3 w-3 shrink-0 text-black-600 transition-transform " + (open ? "rotate-90" : "")} fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7"/></svg>
    <span class="text-[10px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">Repositories</span>
    <span class="rounded-full bg-white-300 px-1.5 text-[10px] font-semibold text-black-700 dark:bg-navy-600 dark:text-black-600">{repos.length}</span>
    <!-- Only while collapsed: expanded, the checked row already says it,
         and repeating it here would just crowd the row. -->
    {#if !open && active}
      <span class="ml-auto flex min-w-0 items-center gap-1 pl-2" title={`${active.name}${active.branch ? ` — ${active.branch}` : ""}`}>
        <span class="truncate text-xs font-medium text-black-900 dark:text-white-100">{active.name}</span>
        {#if active.branch}
          <span class="flex min-w-0 max-w-[45%] items-center gap-0.5 text-[10px] text-black-600 dark:text-black-700">
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
            <span class="truncate">{active.branch}</span>
          </span>
        {/if}
      </span>
    {/if}
  </button>
  {#if open}
    {#if repos.length > 5}
      <div class="px-2 pb-1">
        <input
          type="text"
          bind:value={query}
          placeholder="Search repositories"
          class="w-full rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2.5 py-1.5 text-xs text-black-900 dark:text-white-100 placeholder-black-600 dark:placeholder-black-700 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 focus:outline-none"
        />
      </div>
    {/if}
    <!-- Cap at ~5 rows; longer lists scroll instead of pushing the panel down. -->
    <div bind:this={listEl} class="max-h-[150px] overflow-y-auto pb-1">
      {#if filtered.length === 0}
        <p class="px-3 py-2 text-xs text-black-700 dark:text-black-600">No repositories match.</p>
      {/if}
      {#each filtered as r (r.rel)}
        <button
          type="button"
          onclick={() => onSelect(r.rel)}
          data-active={activeRepo === r.rel}
          aria-current={activeRepo === r.rel ? "true" : undefined}
          title={activeRepo === r.rel ? `${r.name} — current source` : r.name}
          class={"flex w-full items-center gap-2 px-3 py-1.5 text-left transition-colors " +
            (activeRepo === r.rel
              ? "bg-green-100 dark:bg-green-900/30 border-l-2 border-green-500"
              : "border-l-2 border-transparent hover:bg-white-200 dark:hover:bg-navy-800")}
        >
          <!-- The selected row swaps the repo glyph for a check in the accent
               colour: the background tint alone is easy to miss while scanning,
               and it disappears entirely against a hover on a neighbouring row. -->
          {#if activeRepo === r.rel}
            <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-green-600 dark:text-green-400" fill="currentColor" aria-hidden="true"><path d="M8 1.5a6.5 6.5 0 100 13 6.5 6.5 0 000-13zm3.1 4.9l-3.6 4.2a.75.75 0 01-1.1.04L4.9 9.1a.75.75 0 011.06-1.06l1 1 3.1-3.6a.75.75 0 011.14.98z"/></svg>
          {:else}
            <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-black-600" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M2 6h12" stroke-linecap="round"/></svg>
          {/if}
          <span class={"min-w-0 flex-1 truncate text-xs font-medium " + (activeRepo === r.rel ? "text-black-900 dark:text-white-100" : "text-black-800 dark:text-black-600")}>{r.name}</span>
          <!-- Branch is capped and truncates like the name. Left unbounded it
               refused to shrink, so a long branch ("ai/feat/…-and-scm-badge")
               squeezed the repo name to nothing and pushed the row into a
               horizontal scroll. -->
          {#if r.branch}
            <span
              class="flex min-w-0 max-w-[45%] items-center gap-0.5 text-[10px] text-black-600 dark:text-black-700"
              title={`${r.branch}${r.ahead > 0 ? ` ↑${r.ahead}` : ""}${r.behind > 0 ? ` ↓${r.behind}` : ""}`}
            >
              <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
              <span class="truncate">{r.branch}{r.ahead > 0 ? `↑${r.ahead}` : ""}{r.behind > 0 ? `↓${r.behind}` : ""}</span>
            </span>
          {/if}
          <!-- Fixed-width slot so the count sits in the same column on every
               row, and rows without one keep the same right edge. -->
          <span class="flex w-5 shrink-0 justify-end">
            {#if r.changed > 0}
              <span class="rounded-full bg-green-500 px-1.5 text-[10px] font-semibold leading-4 text-white-100">{r.changed}</span>
            {/if}
          </span>
        </button>
      {/each}
    </div>
  {/if}
</div>
