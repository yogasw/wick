<script lang="ts">
  import { ToastHost } from "@wick-fe/common-ui";
  import ProjectSettingsForm from "$lib/components/ProjectSettingsForm.svelte";
  import SubAgentsTab from "$lib/components/SubAgentsTab.svelte";
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

  type Tab = "general" | "subagents";
  let tab = $state<Tab>("general");

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
  <!-- Sticky chrome: back link, tabs, and the save indicator. Both tabs get
       the same max width from here, so switching tabs no longer changes how
       wide the content is. -->
  <header class="sticky top-0 z-20 border-b border-white-300 bg-white-100/95 backdrop-blur dark:border-navy-600 dark:bg-navy-700/95">
    <div class="mx-auto w-full max-w-3xl px-6">
      <a
        href={`${base}/sessions`}
        class="inline-flex items-center gap-1.5 pt-4 text-xs text-black-700 transition-colors hover:text-green-600 dark:text-black-600 dark:hover:text-green-400"
      >
        <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
        All chats
      </a>

      {#if isNew}
        <div class="flex items-center justify-between gap-4 pb-3 pt-2">
          <h1 class="text-base font-semibold text-black-900 dark:text-white-100">New project</h1>
        </div>
      {:else}
        <div class="flex items-end justify-between gap-4">
          <nav class="flex items-center gap-6" aria-label="Project settings sections">
            <button type="button" class={tabClass(tab === "general")} onclick={() => (tab = "general")}>
              General
            </button>
            <button type="button" class={tabClass(tab === "subagents")} onclick={() => (tab = "subagents")}>
              Sub-agents
            </button>
          </nav>
          <div class="pb-3">
            <SaveStatus status={saveStatus} onRetry={() => retry()} />
          </div>
        </div>
      {/if}
    </div>
  </header>

  <main class="mx-auto w-full max-w-3xl px-6 py-6">
    {#if isNew}
      <ProjectSettingsForm {projectID} {base} />
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
