<script lang="ts">
  import { ToastHost } from "@wick-fe/common-ui";
  import ProjectSettingsForm from "$lib/components/ProjectSettingsForm.svelte";
  import SubAgentsTab from "$lib/components/SubAgentsTab.svelte";

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

  const tabClass = (active: boolean) =>
    active
      ? "border-b-2 border-green-500 px-3 py-2 text-sm font-medium text-black-900 dark:text-white-100"
      : "border-b-2 border-transparent px-3 py-2 text-sm font-medium text-black-800 transition-colors hover:text-black-900 dark:text-black-600 dark:hover:text-white-100";
</script>

<div class="min-h-screen p-6">
  <ToastHost />

  {#if isNew}
    <ProjectSettingsForm {projectID} {base} />
  {:else}
    <nav class="mb-6 flex items-center gap-2 border-b border-white-300 dark:border-navy-600">
      <button type="button" class={tabClass(tab === "general")} onclick={() => (tab = "general")}>
        General
      </button>
      <button
        type="button"
        class={tabClass(tab === "subagents")}
        onclick={() => (tab = "subagents")}
      >
        Sub-agents
      </button>
    </nav>

    {#if tab === "general"}
      <ProjectSettingsForm {projectID} {base} />
    {:else}
      <SubAgentsTab {projectID} {base} />
    {/if}
  {/if}
</div>
