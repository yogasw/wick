<script lang="ts">
  /* The Tickets tab of Team settings: which projects the Team Tickets page
     combines, one switch per project. Nothing else lives here. The list is
     not a draft field (TeamSettingValues.ticket_projects), so a switch
     saves through the drawer at once. The switches show the choice made
     here at once (pending) and each save sends that whole choice, so two
     quick flips never start from a list the server has not answered yet. */
  import { onMount } from "svelte";
  import { Toggle } from "@wick-fe/common-ui";
  import type { TeamSettings, TeamSettingValues, TeamSettingsWrite } from "../api/team.js";
  import type { ProjectOption } from "../types/agents.js";
  import { ticketProjectOptions, withProject } from "../ticketProjects.js";

  type Props = {
    draft: TeamSettingValues;
    saved: TeamSettings;
    base?: string;
    onSave?: (body: TeamSettingsWrite) => void;
  };
  let { draft = $bindable(), saved, base = "", onSave }: Props = $props();

  let projects = $state<ProjectOption[] | null>(null);
  let loadError = $state("");
  onMount(async () => {
    try {
      projects = await ticketProjectOptions(base);
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  });

  let pending = $state<string[] | null>(null);
  const selected = $derived(pending ?? saved.ticket_projects ?? []);
  // The server confirmed the choice: follow it again.
  $effect(() => {
    const s = saved.ticket_projects ?? [];
    if (pending && pending.length === s.length && pending.every((id) => s.includes(id))) pending = null;
  });
  function flip(id: string, on: boolean) {
    pending = withProject(selected, id, on);
    onSave?.({ ticket_projects: [...pending] });
  }
</script>

<p class="text-xs text-black-800 dark:text-black-600">
  Your Team setting: which projects show on Tickets. Nothing is written to the projects.
</p>
{#if loadError}
  <p class="text-sm text-neg-400">{loadError}</p>
{:else if !projects}
  <p class="text-sm text-black-800 dark:text-black-600">Loading…</p>
{:else if projects.length === 0}
  <p class="text-sm text-black-800 dark:text-black-600" data-testid="tickets-tab-empty">
    None of the projects you can open has ticket mode on.
  </p>
{:else}
  <div class="divide-y divide-white-300 rounded-xl border border-white-300 dark:divide-navy-600 dark:border-navy-600" data-testid="tickets-tab-list">
    <div class="flex items-center justify-between px-3 py-2 text-xs font-medium text-black-800 dark:text-black-600">
      <span>Project</span><span>On Tickets</span>
    </div>
    {#each projects as p (p.id)}
      <div class="flex items-center gap-3 px-3 py-2">
        <span class="min-w-0 flex-1 truncate text-sm text-black-900 dark:text-white-100">📁 {p.name}</span>
        <Toggle
          checked={selected.includes(p.id)}
          onChange={(v) => flip(p.id, v)}
          label="{p.name} on Tickets"
        />
      </div>
    {/each}
  </div>
{/if}
