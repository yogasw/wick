<script lang="ts">
  // Which references the graph walks. Mirrors the editor's picker: All,
  // Auto (whatever is checked out), or an explicit set of branches.
  //
  // Selection is a list of ref names; the two modes are encoded as the
  // sentinels the API already understands, so there is no second concept
  // to keep in sync — [] is auto, ["all"] is all.
  import type { HistoryRef } from "$lib/api/scm";

  type Props = {
    refs: HistoryRef[];
    selected: string[];
    trunk: string;
    onchange: (next: string[]) => void;
  };
  let { refs, selected, trunk, onchange }: Props = $props();

  let open = $state(false);
  let filter = $state("");

  const isAll = $derived(selected.length === 1 && selected[0] === "all");
  const isAuto = $derived(selected.length === 0);
  const label = $derived(
    isAll ? "All" : isAuto ? "Auto" : selected.length === 1 ? selected[0] : `${selected.length} refs`,
  );

  const locals = $derived(
    refs.filter((r) => !r.remote && r.name.toLowerCase().includes(filter.toLowerCase())),
  );
  const remotes = $derived(
    refs.filter((r) => r.remote && r.name.toLowerCase().includes(filter.toLowerCase())),
  );

  function toggleRef(name: string) {
    // Coming from a mode, the click starts a fresh explicit selection —
    // otherwise the first tick would silently mean "auto plus this".
    const base = isAll || isAuto ? [] : selected;
    const next = base.includes(name) ? base.filter((n) => n !== name) : [...base, name];
    onchange(next);
  }
</script>

<div class="relative">
  <button
    type="button"
    onclick={() => (open = !open)}
    title="Which branches the graph shows"
    class="flex items-center gap-1 rounded border border-white-300 dark:border-navy-600 px-1.5 py-0.5 text-[10px] text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
  >
    <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
    <span class="max-w-[10rem] truncate">{label}</span>
    <span class="text-[8px]">▾</span>
  </button>

  {#if open}
    <!-- Click-away: a bare overlay rather than a document listener, which
         would also have to be torn down on unmount. -->
    <button type="button" class="fixed inset-0 z-10 cursor-default" aria-label="Close" onclick={() => (open = false)}></button>
    <div class="absolute right-0 top-full z-20 mt-1 w-72 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-1.5 shadow-lg">
      <input
        bind:value={filter}
        placeholder="Select references to view, type to filter"
        class="mb-1 w-full rounded border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-1.5 py-1 text-xs text-black-900 dark:text-white-100 focus:border-green-500 focus:outline-none"
      />

      <button
        type="button"
        onclick={() => { onchange(["all"]); open = false; }}
        class={"flex w-full items-center gap-2 rounded px-2 py-1 text-left text-xs hover:bg-white-200 dark:hover:bg-navy-800 " + (isAll ? "font-semibold text-green-600 dark:text-green-400" : "text-black-800 dark:text-black-600")}
      >
        <span class="w-3 text-[10px]">{isAll ? "✓" : ""}</span>
        <span class="font-medium">All</span>
        <span class="text-[10px] text-black-600 dark:text-black-700">Every branch, local and remote</span>
      </button>
      <button
        type="button"
        onclick={() => { onchange([]); open = false; }}
        class={"flex w-full items-center gap-2 rounded px-2 py-1 text-left text-xs hover:bg-white-200 dark:hover:bg-navy-800 " + (isAuto ? "font-semibold text-green-600 dark:text-green-400" : "text-black-800 dark:text-black-600")}
      >
        <span class="w-3 text-[10px]">{isAuto ? "✓" : ""}</span>
        <span class="font-medium">Auto</span>
        <span class="text-[10px] text-black-600 dark:text-black-700">Checked-out branch + its remote</span>
      </button>

      <div class="mt-1 max-h-64 overflow-y-auto border-t border-white-300 dark:border-navy-600 pt-1">
        {#each locals as r (r.name)}
          <button
            type="button"
            onclick={() => toggleRef(r.name)}
            class="flex w-full items-center gap-2 rounded px-2 py-1 text-left text-xs text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
          >
            <span class="w-3 text-[10px]">{selected.includes(r.name) ? "✓" : ""}</span>
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="4" cy="4" r="1.5"/><circle cx="4" cy="12" r="1.5"/><circle cx="12" cy="5" r="1.5"/><path d="M4 5.5v5M5.5 4H9a2 2 0 012 2v0" stroke-linecap="round"/></svg>
            <span class={"min-w-0 flex-1 truncate " + (r.current ? "font-semibold text-black-900 dark:text-white-100" : "")}>{r.name}</span>
            {#if r.trunk || r.name === trunk}<span class="shrink-0 text-[9px] text-green-600 dark:text-green-400">trunk</span>{/if}
            <span class="shrink-0 font-mono text-[9px] text-black-600 dark:text-black-700">{r.sha}</span>
          </button>
        {/each}
        {#each remotes as r (r.name)}
          <button
            type="button"
            onclick={() => toggleRef(r.name)}
            class="flex w-full items-center gap-2 rounded px-2 py-1 text-left text-xs text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
          >
            <span class="w-3 text-[10px]">{selected.includes(r.name) ? "✓" : ""}</span>
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M4.5 12a3 3 0 01-.3-6A4 4 0 0112 6.5a2.75 2.75 0 01-.25 5.5z" stroke-linejoin="round"/></svg>
            <span class="min-w-0 flex-1 truncate">{r.name}</span>
            <span class="shrink-0 font-mono text-[9px] text-black-600 dark:text-black-700">{r.sha}</span>
          </button>
        {/each}
      </div>
    </div>
  {/if}
</div>
