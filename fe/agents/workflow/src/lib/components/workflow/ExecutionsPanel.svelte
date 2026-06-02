<script lang="ts">
  // Executions view orchestrator. Owns the runs list + selection +
  // auto-refresh polling; delegates row rendering to RunListItem and
  // the right pane to RunDetail. Filters are server-side — the backend
  // route accepts ?status&from&to&q so we can search across the full
  // history, not just the recently-loaded page.
  import { onMount, untrack } from "svelte";
  import { workflowAPI, type RunSummary } from "$lib/api/workflow";
  import RunListItem from "./executions/RunListItem.svelte";
  import RunDetail from "./executions/RunDetail.svelte";
  import { runKey } from "./executions/runHelpers";

  type Props = {
    workflowID: string;
    onReplay?: (triggerID: string | null) => void;
  };
  let { workflowID, onReplay }: Props = $props();

  let runs = $state<RunSummary[]>([]);
  let total = $state<number>(-1); // -1 when backend skipped the count (no filter)
  let selectedRunID = $state<string | null>(null);
  let runDetail = $state<any | null>(null);
  let auto = $state(true);
  let loading = $state(false);

  // Filters. `all` is the default for status; the rest are
  // collapsible advanced inputs. Every input goes to the backend so
  // the search reaches runs older than the loaded page.
  let filter = $state<"all" | "success" | "failed" | "running">("all");
  let showAdvanced = $state(false);
  let searchID = $state("");
  let fromDate = $state("");
  let toDate = $state("");

  const advancedActive = $derived(!!searchID || !!fromDate || !!toDate);
  const filterActive = $derived(filter !== "all" || advancedActive);

  function clearAdvanced() {
    searchID = "";
    fromDate = "";
    toDate = "";
  }

  async function refresh() {
    if (loading) return;
    loading = true;
    try {
      const res = await workflowAPI.runs(workflowID, {
        status: filter === "all" ? undefined : filter,
        from: fromDate || undefined,
        to: toDate || undefined,
        q: searchID.trim() || undefined,
      });
      runs = res.runs ?? [];
      total = res.total ?? -1;
    } catch (e) {
      console.error("runs fetch failed:", e);
    } finally {
      loading = false;
    }
  }

  // Debounce filter inputs so each keystroke doesn't fire a request.
  // The status tab buttons skip the debounce by calling refresh()
  // directly — those are discrete clicks that deserve instant feedback.
  let debounceID: ReturnType<typeof setTimeout> | null = null;
  $effect(() => {
    // Track every filter input so the effect re-fires on any change.
    searchID; fromDate; toDate; filter;
    if (debounceID) clearTimeout(debounceID);
    debounceID = setTimeout(() => {
      untrack(() => void refresh());
    }, 250);
    return () => {
      if (debounceID) clearTimeout(debounceID);
    };
  });

  async function loadRun(runID: string) {
    selectedRunID = runID;
    runDetail = null;
    try {
      // Endpoint returns { state: {...completed, failed, event, ...},
      // events: [...] }. Flatten state into the top level so children
      // can read `runDetail.completed` without reaching through `state.`.
      const res = await workflowAPI.runState(workflowID, runID);
      runDetail = { ...(res?.state ?? {}), events: res?.events ?? [] };
    } catch (e) {
      console.error("run state fetch failed:", e);
    }
  }

  onMount(() => {
    // The $effect above already kicks the initial fetch — set up the
    // auto-refresh interval here so polling continues independently.
    const id = setInterval(() => {
      if (auto) void refresh();
    }, 3000);
    return () => clearInterval(id);
  });
</script>

<div class="flex flex-1 min-h-0">
  <!-- Left: runs list. -->
  <aside class="shrink-0 border-r border-slate-200 dark:border-slate-700 flex flex-col" style="width:380px;">
    <header class="px-4 py-2 border-b border-slate-200 dark:border-slate-700 flex items-center gap-3 text-xs">
      <span class="font-semibold text-slate-700 dark:text-slate-200">
        {runs.length} {filterActive && total >= 0 ? `of ${total}` : ""} runs
      </span>
      {#if loading}<span class="text-slate-400 text-[10px]">…</span>{/if}
      <button class="ml-auto text-slate-500 hover:text-slate-900 dark:hover:text-slate-100" onclick={() => void refresh()} disabled={loading} title="Refresh">↻</button>
      <label class="flex items-center gap-1 text-slate-500 cursor-pointer">
        <input type="checkbox" bind:checked={auto} />
        auto
      </label>
    </header>

    <!-- Status filter tabs + advanced toggle. Counts here reflect the
         CURRENT result set the backend returned (not global) — the
         status pill itself is sent to the API on change. -->
    <div class="flex items-center gap-1 px-2 py-2 border-b border-slate-200 dark:border-slate-700 text-[11px]">
      {#each [
        { key: "all", label: "All" },
        { key: "success", label: "✓" },
        { key: "failed", label: "✗" },
        { key: "running", label: "●" },
      ] as t}
        <button
          class="px-2 py-0.5 rounded transition-colors"
          class:bg-slate-200={filter === t.key}
          class:dark:bg-slate-700={filter === t.key}
          class:text-slate-900={filter === t.key}
          class:dark:text-slate-100={filter === t.key}
          class:text-slate-500={filter !== t.key}
          onclick={() => (filter = t.key as typeof filter)}
        >
          {t.label}
        </button>
      {/each}
      <button
        class="ml-auto text-slate-500 hover:text-slate-900 dark:hover:text-slate-100"
        onclick={() => (showAdvanced = !showAdvanced)}
        title="Advanced filters"
      >
        {showAdvanced ? "▾" : "▸"} advanced
        {#if advancedActive}<span class="ml-1 h-1.5 w-1.5 rounded-full bg-emerald-500 inline-block align-middle"></span>{/if}
      </button>
    </div>

    {#if showAdvanced}
      <div class="px-3 py-2 border-b border-slate-200 dark:border-slate-700 space-y-2 text-[11px]">
        <input
          type="search"
          placeholder="Search run id…"
          class="w-full rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1 font-mono"
          bind:value={searchID}
        />
        <div class="grid grid-cols-2 gap-2">
          <label class="flex flex-col gap-0.5 text-slate-500">
            <span class="uppercase tracking-wider text-[10px]">From</span>
            <input
              type="date"
              class="rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1"
              bind:value={fromDate}
            />
          </label>
          <label class="flex flex-col gap-0.5 text-slate-500">
            <span class="uppercase tracking-wider text-[10px]">To</span>
            <input
              type="date"
              class="rounded border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-800 px-2 py-1"
              bind:value={toDate}
            />
          </label>
        </div>
        {#if advancedActive}
          <button
            class="text-emerald-600 dark:text-emerald-400 hover:underline"
            onclick={clearAdvanced}
          >clear filters</button>
        {/if}
      </div>
    {/if}

    <div class="flex-1 overflow-y-auto">
      {#if runs.length === 0}
        <p class="p-4 text-xs text-slate-500">
          {filterActive ? "No runs match the current filters." : "No runs yet."}
        </p>
      {:else}
        {#each runs as r (runKey(r))}
          <RunListItem
            run={r}
            active={selectedRunID === runKey(r)}
            onpick={loadRun}
          />
        {/each}
      {/if}
    </div>
  </aside>

  <!-- Right: run detail. -->
  <section class="flex-1 overflow-y-auto p-4">
    {#if !selectedRunID}
      <div class="h-full flex flex-col items-center justify-center text-slate-500 text-sm gap-3">
        <span class="text-2xl">⊜</span>
        <div class="font-medium">Select a run</div>
        <div class="text-xs">Click any execution on the left to inspect its output.</div>
      </div>
    {:else}
      <RunDetail runID={selectedRunID} runDetail={runDetail} {onReplay} />
    {/if}
  </section>
</div>
