<script lang="ts">
  import ValidationTab from "./tabs/ValidationTab.svelte";
  import GuardTab from "./tabs/GuardTab.svelte";
  import TestsTab from "./tabs/TestsTab.svelte";
  import LogsTab from "./tabs/LogsTab.svelte";
  import JsonTab from "./tabs/JsonTab.svelte";
  import HistoryTab from "./tabs/HistoryTab.svelte";
  import { logLines } from "$lib/stores/sse";
  import { draftWorkflow, validationReport } from "$lib/stores/editor";
  import { workflowAPI, type ValidationReport } from "$lib/api/workflow";

  // Bottom-panel data fetched once per workflow load + on demand when
  // the user re-opens a tab. Cheap enough to refetch — validate runs
  // in ~ms, guard rules are pure functions, tests reads N JSON files.
  let validationData = $state<ValidationReport | null>(null);
  let guardData = $state<any[] | null>(null);
  let testsData = $state<{ name: string; assertions: number }[]>([]);
  let panelLoading = $state(false);

  async function refreshPanel(tab: typeof tabs[number]) {
    const wf = $draftWorkflow;
    if (!wf) return;
    panelLoading = true;
    try {
      if (tab === "validation") {
        validationData = await workflowAPI.validate(wf.id);
      } else if (tab === "guard") {
        const res = await workflowAPI.guard(wf.id);
        guardData = res.hits ?? [];
      } else if (tab === "tests") {
        const res = await workflowAPI.tests(wf.id);
        testsData = res.cases ?? [];
      }
    } catch (e) {
      console.error("panel refresh failed:", e);
    } finally {
      panelLoading = false;
    }
  }

  let active = $state<
    "logs" | "json" | "validation" | "guard" | "tests" | "history"
  >("logs");

  // Tab labels — storage keys stay terse, labels read full nouns.
  // JSON preview shows the live draft side-by-side with the last
  // published copy so operators see the diff before publishing.
  // Runs moved to the top-level Executions tab; that view owns the
  // detail pane + replay-to-editor action.
  const labels: Record<string, string> = {
    logs: "Logs",
    json: "JSON preview",
    validation: "Validation",
    guard: "Guard",
    tests: "Tests",
    history: "History",
  };

  // Count badge per tab: surfaces "Logs (0)" / "Tests (3)" inline so
  // operators see at a glance whether anything happened on the active
  // run before opening the panel.
  function tabCount(t: string): number | null {
    switch (t) {
      // Logs come from the live SSE store, not the placeholder prop.
      case "logs": return $logLines.length;
      case "tests": return (testsData.length > 0 ? testsData : tests)?.length ?? 0;
      case "guard": return guardData?.length ?? null;
      case "validation": {
        const r = $validationReport ?? validationData;
        return (r?.errors?.length ?? 0) + (r?.warnings?.length ?? 0);
      }
      case "history": return versions?.length ?? 0;
      default: return null;
    }
  }

  // Collapsed by default — matches the legacy editor where the bottom
  // panel is a "▴ expand" strip until the user opens it. Persisted to
  // localStorage so refreshing the editor keeps the user's preference.
  let collapsed = $state(true);
  if (typeof window !== "undefined") {
    const stored = window.localStorage.getItem("wick:wfv2:bottom-collapsed");
    if (stored !== null) collapsed = stored === "1";
  }
  function toggle() {
    collapsed = !collapsed;
    try {
      window.localStorage.setItem(
        "wick:wfv2:bottom-collapsed",
        collapsed ? "1" : "0",
      );
    } catch {
      /* private mode / storage full — ignore */
    }
  }

  // Tab order mirrors the legacy editor: read-only debugging first
  // (logs / json / runs), static checks next (validation / guard /
  // tests), history last.
  const tabs = [
    "logs",
    "json",
    "validation",
    "guard",
    "tests",
    "history",
  ] as const;

  type Props = {
    validation?: any;
    guardHits?: any;
    tests?: any[];
    logs?: any[];
    versions?: any[];
    onRestoreVersion?: (id: number) => void;
  };
  let {
    validation = null,
    guardHits = null,
    tests = [],
    logs = [],
    versions = [],
    onRestoreVersion,
  }: Props = $props();

  // Clicking a tab while collapsed both opens the panel AND switches to
  // that tab — matches the n8n/Drawflow editor pattern.
  function pickTab(t: (typeof tabs)[number]) {
    active = t;
    if (collapsed) toggle();
    if (t === "validation" || t === "guard" || t === "tests") {
      void refreshPanel(t);
    }
  }
</script>

<section
  class="flex flex-col border-t border-slate-200 dark:border-[#2c3a5a] bg-white dark:bg-[#0f172a] transition-[height] duration-150"
  style:height={collapsed ? "auto" : "260px"}
>
  <nav class="flex items-center border-b border-slate-200 dark:border-[#2c3a5a] text-xs">
    {#each tabs as t}
      <button
        class="px-3 py-1.5 border-b-2 transition-colors flex items-center gap-1.5"
        class:border-emerald-500={!collapsed && active === t}
        class:border-transparent={collapsed || active !== t}
        class:text-emerald-600={!collapsed && active === t}
        class:dark:text-emerald-400={!collapsed && active === t}
        onclick={() => pickTab(t)}
      >
        {labels[t]}
        {#if tabCount(t) !== null}
          <span class="text-[10px] text-slate-400">({tabCount(t)})</span>
        {/if}
      </button>
    {/each}
    <button
      class="ml-auto px-3 py-1.5 text-[11px] text-slate-500 dark:text-slate-400 hover:text-slate-900 dark:hover:text-slate-100"
      onclick={toggle}
      aria-expanded={!collapsed}
      title={collapsed ? "Expand panel" : "Collapse panel"}
    >
      {collapsed ? "▴ expand" : "▾ collapse"}
    </button>
  </nav>
  {#if !collapsed}
    <div class="flex-1 overflow-y-auto p-3">
      {#if active === "validation"}<ValidationTab />
      {:else if active === "guard"}<GuardTab hits={guardData ?? guardHits} />
      {:else if active === "tests"}<TestsTab cases={testsData.length > 0 ? testsData : tests} onRunAll={() => refreshPanel("tests")} running={panelLoading} />
      {:else if active === "logs"}<LogsTab lines={logs} />
      {:else if active === "json"}<JsonTab />
      {:else if active === "history"}<HistoryTab versions={versions} onrestore={onRestoreVersion} />
      {/if}
    </div>
  {/if}
</section>
