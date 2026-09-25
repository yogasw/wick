<script lang="ts">
  import { ToastHost } from "@wick-fe/common-ui";
  import ProjectSettingsForm from "$lib/components/ProjectSettingsForm.svelte";
  import SubAgentsTab from "$lib/components/SubAgentsTab.svelte";
  import AgentMemoryTab from "$lib/components/AgentMemoryTab.svelte";
  import SaveStatus from "$lib/components/SaveStatus.svelte";
  import type { SaveStatus as SaveStatusValue } from "$lib/autosave.js";

  function getProjectID(): string {
    return document.getElementById("app")?.dataset.projectId ?? "";
  }

  function getBase(): string {
    return document.getElementById("app")?.dataset.base ?? "";
  }

  const projectID = getProjectID();
  const base = getBase();

  /* Agent Memory's two facts, inlined by the Go shell the way data-base is:
     whether the feature is on for this reader at all, and whether they may
     MANAGE it. Read here so the tab is right on the first paint — a door to
     a feature that is off, or that this person cannot open, is not shown
     (PLAN §22.1, §23.3). */
  const el = document.getElementById("app");
  const memoryVisible = (el?.dataset.agentMemory ?? "") === "true";
  const memoryManageable = (el?.dataset.canManage ?? "") === "true";

  /* Back goes where the operator came from — the sessions list remembers
     whether it was showing cards or rows, and a hard link to /sessions
     would throw that away. Only same-origin history is trusted; opened
     cold (direct link, new tab) it falls back to the list. */
  function goBack() {
    const ref = document.referrer;
    if (ref && new URL(ref).origin === window.location.origin && window.history.length > 1) {
      window.history.back();
      return;
    }
    window.location.href = `${base}/sessions`;
  }

  type Tab = "general" | "subagents" | "memory";

  /* The project's "⋯" menu links straight to ?tab=memory, so the entry lands
     the reader INSIDE the project rather than on the global panel. An
     unknown or unavailable tab falls back to General instead of showing an
     empty page. */
  function initialTab(): Tab {
    const want = new URLSearchParams(window.location.search).get("tab");
    if (want === "subagents") return "subagents";
    if (want === "memory" && memoryVisible) return "memory";
    return "general";
  }
  let tab = $state<Tab>(initialTab());

  // A project that does not exist yet has nothing to scope roles to, so
  // the tab strip only appears once the project has been created.
  const isNew = projectID === "" || projectID === "new";

  /* Auto-save status lives here, not in the form: it belongs beside the tab
     strip where it stays visible however far the form is scrolled, and the
     header is the one row both tabs share. */
  let saveStatus = $state<SaveStatusValue>({ state: "idle" });
  let retry = $state<() => void>(() => {});

  const tabClass = (active: boolean) =>
    active
      ? "relative px-1 py-3 text-sm font-semibold text-black-900 after:absolute after:inset-x-0 after:-bottom-px after:h-0.5 after:rounded-full after:bg-green-500 dark:text-white-100"
      : "relative px-1 py-3 text-sm font-medium text-black-700 transition-colors hover:text-black-900 dark:text-black-600 dark:hover:text-white-100";
</script>

<ToastHost />

<div class="min-h-screen bg-white-200 dark:bg-navy-800">
  <!-- Sticky chrome: one row — back, tabs, save indicator. The back link
       used to sit on a line of its own above the tabs, which cost the page
       a whole header's worth of height to say "All chats". -->
  <header class="sticky top-0 z-20 border-b border-white-300 bg-white-100/95 backdrop-blur dark:border-navy-600 dark:bg-navy-700/95">
    <div class="page-col flex items-center gap-4 px-6">
      <button
        type="button"
        onclick={goBack}
        aria-label="Back"
        title="Back"
        class="-ml-2 flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-white-200 hover:text-black-900 dark:text-black-600 dark:hover:bg-navy-800 dark:hover:text-white-100"
      >
        <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.75" aria-hidden="true">
          <path d="M10 3L5 8l5 5" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
      </button>

      {#if isNew}
        <h1 class="py-3 text-sm font-semibold text-black-900 dark:text-white-100">New project</h1>
      {:else}
        <nav class="flex items-center gap-6" aria-label="Project settings sections">
          <button type="button" class={tabClass(tab === "general")} onclick={() => (tab = "general")}>
            General
          </button>
          <button type="button" class={tabClass(tab === "subagents")} onclick={() => (tab = "subagents")}>
            Sub-agents
          </button>
          {#if memoryVisible}
            <button
              type="button"
              class={tabClass(tab === "memory")}
              onclick={() => (tab = "memory")}
              data-testid="tab-memory"
            >
              Agent Memory
            </button>
          {/if}
        </nav>
        <div class="ml-auto">
          <SaveStatus status={saveStatus} onRetry={() => retry()} />
        </div>
      {/if}
    </div>
  </header>

  <main class="page-col px-6 py-6">
    {#if isNew}
      <ProjectSettingsForm {projectID} {base} />
    {:else if tab === "memory" && memoryVisible}
      <AgentMemoryTab {projectID} {base} canManage={memoryManageable} />
    {:else if tab === "general"}
      <ProjectSettingsForm
        {projectID}
        {base}
        onStatus={(s, r) => { saveStatus = s; retry = r; }}
      />
    {:else}
      <SubAgentsTab {projectID} {base} />
    {/if}
  </main>
</div>
