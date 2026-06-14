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
  import { toastError, toastOk } from "@wick-fe/common-stores";

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
  let kindFilter = $state<"all" | "manual" | "automation" | "test">("all");
  let showAdvanced = $state(false);
  let searchID = $state("");
  let fromDate = $state("");
  let toDate = $state("");

  const advancedActive = $derived(!!searchID || !!fromDate || !!toDate);
  const filterActive = $derived(filter !== "all" || kindFilter !== "all" || advancedActive);

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
        kind: kindFilter === "all" ? undefined : kindFilter,
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
    searchID; fromDate; toDate; filter; kindFilter;
    if (debounceID) clearTimeout(debounceID);
    debounceID = setTimeout(() => {
      untrack(() => void refresh());
    }, 250);
    return () => {
      if (debounceID) clearTimeout(debounceID);
    };
  });

  async function handleDelete(runID: string) {
    try {
      await workflowAPI.deleteRun(workflowID, runID);
      if (selectedRunID === runID) {
        selectedRunID = null;
        runDetail = null;
      }
      toastOk("Run deleted");
      await refresh();
    } catch (e) {
      toastError("Delete failed", e instanceof Error ? e.message : String(e));
    }
  }

  // Re-run a past run with its original input, then jump to the fresh run
  // (newest after refresh) so the user watches it live.
  async function handleRerun(runID: string) {
    try {
      await workflowAPI.rerunRun(workflowID, runID);
      toastOk("Re-running…");
      await refresh();
      if (runs.length > 0) await loadRun(runKey(runs[0]));
    } catch (e) {
      toastError("Re-run failed", e instanceof Error ? e.message : String(e));
    }
  }

  async function loadRun(runID: string) {
    selectedRunID = runID;
    runDetail = null;
    try {
      // Endpoint returns { state: {...completed, failed, event, ...},
      // events: [...] }. Flatten state into the top level so children
      // can read `runDetail.completed` without reaching through `state.`.
      const res = await workflowAPI.runState(workflowID, runID);
      runDetail = {
        ...(res?.state ?? {}),
        events: res?.events ?? [],
        events_total: res?.events_total ?? (res?.events?.length ?? 0),
        events_truncated: res?.events_truncated ?? false,
      };
    } catch (e) {
      console.error("run state fetch failed:", e);
    }
  }

  async function loadAllEvents() {
    if (!selectedRunID) return;
    try {
      const res = await workflowAPI.runState(workflowID, selectedRunID, 0);
      runDetail = {
        ...(res?.state ?? {}),
        events: res?.events ?? [],
        events_total: res?.events_total ?? (res?.events?.length ?? 0),
        events_truncated: false,
      };
    } catch (e) {
      toastError("Load all events failed", e instanceof Error ? e.message : String(e));
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
  <aside class={`shrink-0 border-r border-slate-200 dark:border-navy-600 flex-col w-full md:w-96 ${selectedRunID ? "hidden md:flex" : "flex"}`}>
    <header class="px-4 py-2 border-b border-slate-200 dark:border-navy-600 flex items-center gap-3 text-xs">
      <span class="font-semibold text-black-500 dark:text-white-100">
        {runs.length} {filterActive && total >= 0 ? `of ${total}` : ""} runs
      </span>
      {#if loading}<span class="text-black-700 dark:text-black-500 text-[10px]">…</span>{/if}
      <button class="ml-auto text-black-700 dark:text-black-600 hover:text-slate-900 dark:hover:text-black-800 dark:text-white-100" onclick={() => void refresh()} disabled={loading} title="Refresh">↻</button>
      <label class="flex items-center gap-1 text-black-700 dark:text-black-600 cursor-pointer">
        <input type="checkbox" bind:checked={auto} />
        auto
      </label>
    </header>

    <!-- Status filter tabs + advanced toggle. Counts here reflect the
         CURRENT result set the backend returned (not global) — the
         status pill itself is sent to the API on change. -->
    <div class="flex items-center gap-1 px-2 py-2 border-b border-slate-200 dark:border-navy-600 text-[11px]">
      {#each [
        { key: "all", label: "All" },
        { key: "success", label: "✓" },
        { key: "failed", label: "✗" },
        { key: "running", label: "●" },
      ] as t}
        <button
          class={`px-2 py-0.5 rounded transition-colors text-[11px] ${filter === t.key ? "bg-slate-200 dark:bg-navy-600 text-slate-900 dark:text-white-100 font-medium" : "text-black-700 dark:text-black-500 hover:bg-slate-100 dark:hover:bg-navy-700"}`}
          onclick={() => (filter = t.key as typeof filter)}
        >
          {t.label}
        </button>
      {/each}
      <button
        class="ml-auto text-black-700 dark:text-black-600 hover:text-slate-900 dark:hover:text-black-800 dark:text-white-100"
        onclick={() => (showAdvanced = !showAdvanced)}
        title="Advanced filters"
      >
        {showAdvanced ? "▾" : "▸"} advanced
        {#if advancedActive}<span class="ml-1 h-1.5 w-1.5 rounded-full bg-emerald-500 inline-block align-middle"></span>{/if}
      </button>
    </div>

    <!-- Kind filter — manual / automation / test bucket. Sent to the
         backend as `?kind=`; rules match runKind() in
         spa_workflows.go so the FE pill in each row mirrors the
         backend's bucketing. -->
    <div class="flex items-center gap-1 px-2 py-1.5 border-b border-slate-200 dark:border-navy-600 text-[11px]">
      <span class="px-2 text-black-700 dark:text-black-500 uppercase tracking-wider text-[10px]">kind</span>
      {#each [
        { key: "all", label: "All" },
        { key: "manual", label: "Manual" },
        { key: "automation", label: "Auto" },
        { key: "test", label: "Test" },
      ] as t}
        <button
          class={`px-2 py-0.5 rounded transition-colors text-[11px] ${kindFilter === t.key ? "bg-slate-200 dark:bg-navy-600 text-slate-900 dark:text-white-100 font-medium" : "text-black-700 dark:text-black-500 hover:bg-slate-100 dark:hover:bg-navy-700"}`}
          onclick={() => (kindFilter = t.key as typeof kindFilter)}
        >
          {t.label}
        </button>
      {/each}
    </div>

    {#if showAdvanced}
      <div class="px-3 py-2 border-b border-slate-200 dark:border-navy-600 space-y-2 text-[11px]">
        <input
          type="search"
          placeholder="Search run id…"
          class="w-full rounded border border-slate-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 font-mono"
          bind:value={searchID}
        />
        <div class="grid grid-cols-2 gap-2">
          <label class="flex flex-col gap-0.5 text-black-700 dark:text-black-600">
            <span class="uppercase tracking-wider text-[10px]">From</span>
            <input
              type="date"
              class="rounded border border-slate-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1"
              bind:value={fromDate}
            />
          </label>
          <label class="flex flex-col gap-0.5 text-black-700 dark:text-black-600">
            <span class="uppercase tracking-wider text-[10px]">To</span>
            <input
              type="date"
              class="rounded border border-slate-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1"
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
        <p class="p-4 text-xs text-black-700 dark:text-black-600">
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

  <!-- Right: run detail. Mobile shows list OR detail (toggled by selection). -->
  <section class={`flex-1 overflow-y-auto p-4 ${selectedRunID ? "block" : "hidden md:block"}`}>
    {#if !selectedRunID}
      <div class="h-full flex flex-col items-center justify-center text-black-700 dark:text-black-600 text-sm gap-3">
        <span class="text-2xl">⊜</span>
        <div class="font-medium">Select a run</div>
        <div class="text-xs">Click any execution on the left to inspect its output.</div>
      </div>
    {:else}
      <button
        type="button"
        class="md:hidden mb-3 inline-flex items-center gap-1 text-xs text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100"
        onclick={() => (selectedRunID = null)}
      >← Back to runs</button>
      <RunDetail runID={selectedRunID} runDetail={runDetail} {onReplay} onDelete={handleDelete} onRerun={handleRerun} onLoadAllEvents={loadAllEvents} />
    {/if}
  </section>
</div>
