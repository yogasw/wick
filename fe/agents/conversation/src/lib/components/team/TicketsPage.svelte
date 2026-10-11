<script lang="ts">
  /* Team › Tickets (/team/t…): the tickets of the projects picked in Team
     settings › Tickets, combined. Three states, by route and selection:
       - nothing picked yet: the picker (every project the caller can open
         with ticket mode on) and Done, which saves ticket_projects;
       - /team/t: the combined board of the picked projects;
       - /team/t/<project>/<ticket>: that ticket's page.
     The board and the ticket page are placeholders for now; they fill the
     two blocks marked below. The header (title + ⚙) is AgentsApp's. */
  import { onMount } from "svelte";
  import { runApi, saveTeamSettings, type TeamSettings } from "../../api/team.js";
  import type { TicketsRoute } from "../../agentsRouter.js";
  import type { ProjectOption } from "../../types/agents.js";
  import { ticketProjectOptions, withProject } from "../../ticketProjects.js";

  type Props = {
    base: string;
    /** The saved selection; null while Team settings are still loading. */
    selected: string[] | null;
    /** Team settings could not be read: no picker (Done would replace a
        selection we could not see), the reason and Retry instead. */
    loadError?: string;
    onRetry?: () => void;
    /** The pickable projects once read (the header names them). */
    onProjects?: (p: ProjectOption[]) => void;
    route: TicketsRoute;
    /** Done saved the selection; the confirmed settings. */
    onPicked: (s: TeamSettings) => void;
    /** Move within Tickets ({} = the board). */
    onRoute: (r: TicketsRoute) => void;
  };
  let { base, selected, loadError: settingsError = "", onRetry, onProjects, route, onPicked, onRoute }: Props = $props();

  let projects = $state<ProjectOption[] | null>(null);
  let loadError = $state("");
  onMount(async () => {
    try {
      projects = await ticketProjectOptions(base);
      onProjects?.(projects);
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  });
  const nameOf = (id: string) => projects?.find((p) => p.id === id)?.name ?? id;

  /* Picker */
  let draft = $state<string[]>([]);
  let saving = $state(false);
  let saveError = $state("");
  async function done() {
    if (!draft.length || saving) return;
    saving = true;
    saveError = "";
    try {
      onPicked(await runApi(saveTeamSettings(base, { ticket_projects: $state.snapshot(draft) })));
    } catch (e) {
      saveError = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }

  const muted = "text-black-800 dark:text-black-600";
</script>

<div class="h-full overflow-y-auto" data-testid="tickets-page">
  {#if route.project && route.ticket}
    <!-- Ticket page: TicketDetail (and later the room) goes here. A link
         to a ticket opens it even before any project is picked. -->
    <div class="p-4" data-testid="tickets-ticket" data-project={route.project} data-ticket={route.ticket}>
      <div class="flex items-center gap-3">
        <button type="button" class="rounded-lg px-2 py-1 text-sm text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600" onclick={() => onRoute({})}>← Tickets</button>
        <span class="font-mono text-xs {muted}">{route.ticket}</span>
        <span class="text-xs {muted}">📁 {nameOf(route.project)}</span>
      </div>
    </div>
  {:else if settingsError}
    <div class="p-4 text-sm" data-testid="tickets-load-error">
      <span class="text-neg-400">Couldn't load your Tickets settings: {settingsError}</span>
      <button type="button" class="ml-2 font-medium text-green-600 hover:underline" onclick={() => onRetry?.()}>Retry</button>
    </div>
  {:else if selected === null}
    <p class="p-4 text-sm {muted}">Loading…</p>
  {:else if selected.length === 0}
    <div class="mx-auto my-10 max-w-[480px] px-6 text-center" data-testid="tickets-picker">
      <div class="text-2xl" aria-hidden="true">🎫</div>
      <h2 class="mt-1 text-[15px] font-semibold text-black-900 dark:text-white-100">Select projects first</h2>
      <p class="mt-1 text-sm {muted}">
        Their tickets are combined on one board. Board buttons like Sync run on the selected projects only.
        Change it later in Team settings › Tickets.
      </p>
      {#if loadError}
        <p class="mt-4 text-sm text-neg-400">{loadError}</p>
      {:else if !projects}
        <p class="mt-4 text-sm {muted}">Loading projects…</p>
      {:else if projects.length === 0}
        <p class="mt-4 text-sm {muted}" data-testid="tickets-picker-empty">None of the projects you can open has ticket mode on.</p>
      {:else}
        <div class="my-3 flex flex-wrap justify-center gap-1.5" role="group" aria-label="Projects">
          {#each projects as p (p.id)}
            {@const on = draft.includes(p.id)}
            <label
              class="flex cursor-pointer items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-medium {on
                ? 'border-green-500 bg-green-50 text-green-700 dark:bg-navy-600 dark:text-green-400'
                : 'border-white-300 text-black-900 hover:bg-white-200 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-700'}"
            >
              <input type="checkbox" class="accent-green-500" checked={on} onchange={(e) => (draft = withProject(draft, p.id, e.currentTarget.checked))} />
              📁 {p.name}
            </label>
          {/each}
        </div>
        <button
          type="button"
          class="rounded-lg bg-green-500 px-4 py-2 text-sm font-semibold text-white-100 hover:bg-green-600 disabled:cursor-not-allowed disabled:opacity-50"
          disabled={!draft.length || saving}
          onclick={done}
        >{saving ? "Saving…" : draft.length ? `Done · ${draft.length} selected` : "Done"}</button>
        {#if saveError}<p class="mt-2 text-sm text-neg-400">{saveError}</p>{/if}
      {/if}
      <p class="mt-3 text-xs {muted}">Only projects you can already open. Your setting; nothing changes in the projects.</p>
    </div>
  {:else}
    <!-- Combined board: KanbanBoard in multi-project mode goes here. -->
    <div class="p-4" data-testid="tickets-board">
      <p class="text-sm {muted}">
        Tickets of {#each selected as id, i (id)}{i ? " · " : ""}<span class="font-medium text-black-900 dark:text-white-100">📁 {nameOf(id)}</span>{/each}
      </p>
    </div>
  {/if}
</div>
