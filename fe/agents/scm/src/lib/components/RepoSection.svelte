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

  /* Where the selected repo sits relative to the visible slice: null when it
     is on screen, "up"/"down" when it has scrolled out. Drives the focus
     button — the list no longer drags itself back to the selection, so this
     is how you get back to it. */
  let activeOff = $state<"up" | "down" | null>(null);

  // scrollTop we last set OURSELVES. A scroll event landing on this value is
  // our own move, not the user's, so it must not count as "hands on the list".
  let autoTop = 0;
  // When the user last scrolled by hand. Auto-scroll steps aside for a while
  // after that: being yanked mid-scroll is exactly the bug.
  let touchedAt = 0;
  const HOLD_MS = 5000;
  function handsOn(): boolean { return Date.now() - touchedAt < HOLD_MS; }

  // Auto-scroll fires only when its REASON changes (opened / new selection /
  // new filter), never merely because the rows were re-rendered.
  let lastAutoKey = "";

  function measure(): void {
    const root = listEl;
    const row = root?.querySelector<HTMLElement>('[data-active="true"]');
    if (!root || !row) {
      activeOff = null;
      return;
    }
    const rb = row.getBoundingClientRect();
    const lb = root.getBoundingClientRect();
    if (rb.bottom <= lb.top + 1) activeOff = "up";
    else if (rb.top >= lb.bottom - 1) activeOff = "down";
    else activeOff = null;
  }

  /* Bring the selected repo into view. The list shows ~5 rows of what can be
     dozens of repos AND starts collapsed, so expanding it lands at scroll 0
     with the active repo almost always off-screen — the selection was there,
     just invisible until you scrolled for it.

     The offset is computed by hand instead of using scrollIntoView: that moves
     the nearest scrollable ANCESTOR too, and we want this list and nothing
     else to move. Returns false when the row isn't rendered (filtered out, or
     the snapshot hasn't arrived yet) so the caller can retry later. */
  function scrollActiveIntoView(): boolean {
    const root = listEl;
    const row = root?.querySelector<HTMLElement>('[data-active="true"]');
    if (!root || !row) return false;
    const rowTop = row.getBoundingClientRect().top - root.getBoundingClientRect().top + root.scrollTop;
    let top = root.scrollTop;
    if (rowTop < root.scrollTop) top = rowTop;
    else if (rowTop + row.offsetHeight > root.scrollTop + root.clientHeight) {
      top = rowTop + row.offsetHeight - root.clientHeight;
    }
    autoTop = Math.max(0, Math.min(top, root.scrollHeight - root.clientHeight));
    root.scrollTop = autoTop;
    measure();
    return true;
  }

  function onListScroll(): void {
    const root = listEl;
    if (!root) return;
    if (Math.abs(root.scrollTop - autoTop) > 1) touchedAt = Date.now();
    measure();
  }

  function jumpToActive(): void {
    touchedAt = 0; // an explicit click is not "hands off, leave me alone"
    scrollActiveIntoView();
  }

  /* The snapshot behind `repos` is REPLACED wholesale on every git_status SSE
     event, so `filtered` is a brand-new array several times a minute while an
     agent works. Depending on it re-ran the scroll each time and threw away
     wherever the user had scrolled to — the list kept snapping back down to
     the checked row. Reading repos.length keeps the effect alive for the first
     snapshot (rows arrive after the panel is opened); the key guard is what
     stops every later one from moving anything. */
  $effect(() => {
    void repos.length;
    if (!open) {
      lastAutoKey = "";
      activeOff = null;
      return;
    }
    const key = `${activeRepo}\n${query}`;
    if (key === lastAutoKey) return;
    const root = listEl;
    if (!root) return;
    requestAnimationFrame(() => {
      if (handsOn()) {
        // Mid-scroll: leave the viewport alone and let the button offer the way back.
        lastAutoKey = key;
        measure();
        return;
      }
      if (scrollActiveIntoView()) lastAutoKey = key;
    });
  });

  // Keep the button's answer current as rows come and go, without moving anything.
  $effect(() => {
    void repos.length;
    void filtered.length;
    void activeRepo;
    void query;
    if (!open || !listEl) return;
    requestAnimationFrame(() => measure());
  });
</script>

<div class="border-b border-white-300 dark:border-navy-600">
  <button type="button" onclick={() => (open = !open)} class="flex w-full items-center gap-1.5 px-2 py-1.5 text-left">
    <svg class={"h-3 w-3 shrink-0 text-black-600 transition-transform " + (open ? "rotate-90" : "")} fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7"/></svg>
    <span class="text-[10px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">Repositories</span>
    <span class="rounded-full bg-white-300 px-1.5 text-[10px] font-semibold text-black-700 dark:bg-navy-600 dark:text-black-600">{repos.length}</span>
    <!-- Only while collapsed: expanded, the checked row already says it.
         Sits right after the count and takes the rest of the row, rather
         than being pushed to the far edge — and carries no branch, which
         the branch bar below already shows with its own picker. Sharing
         the row three ways left the name a stub ("bbg-ads-agent-…"). -->
    {#if !open && active}
      <span
        class="min-w-0 flex-1 truncate text-left text-xs font-medium text-black-900 dark:text-white-100"
        title={active.name}
      >{active.name}</span>
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
    <!-- `relative` so the focus button can float over the rows: it has to sit
         OUTSIDE the scroller, or it would scroll away with them. -->
    <div class="relative">
      <!-- Cap at ~5 rows; longer lists scroll instead of pushing the panel down. -->
      <div bind:this={listEl} onscroll={onListScroll} class="max-h-[150px] overflow-y-auto pb-1">
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
      <!-- Only while the checked row is out of sight. Centred, and inset from
           the list's edges: parked in the bottom-right corner it sat a few px
           above the Changes/History tabs, so a miss on a 24px target switched
           the tab instead. The chevron points the way the selection went, and
           carrying its NAME makes the pill say where it takes you. -->
      {#if activeOff && active}
        <button
          type="button"
          onclick={jumpToActive}
          aria-label={`Jump to current repository — ${active.name}`}
          title={`Jump to current repository — ${active.name}`}
          class={"absolute left-1/2 z-10 flex max-w-[85%] -translate-x-1/2 items-center gap-1 rounded-full border border-white-300 bg-white-100 py-1 pl-2 pr-2.5 text-[10px] font-medium text-green-700 shadow-md transition-colors hover:bg-white-200 dark:border-navy-600 dark:bg-navy-700 dark:text-green-400 dark:hover:bg-navy-800 " +
            (activeOff === "up" ? "top-2" : "bottom-2")}
        >
          <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <circle cx="8" cy="8" r="2.25"/>
            <circle cx="8" cy="8" r="5.5"/>
            <path d="M8 .8v1.9M8 13.3v1.9M.8 8h1.9M13.3 8h1.9" stroke-linecap="round"/>
          </svg>
          <span class="truncate">{active.name}</span>
          <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true">
            {#if activeOff === "up"}
              <path d="M8 12.5V4M4.5 7.5L8 4l3.5 3.5" stroke-linecap="round" stroke-linejoin="round"/>
            {:else}
              <path d="M8 3.5V12M4.5 8.5L8 12l3.5-3.5" stroke-linecap="round" stroke-linejoin="round"/>
            {/if}
          </svg>
        </button>
      {/if}
    </div>
  {/if}
</div>
