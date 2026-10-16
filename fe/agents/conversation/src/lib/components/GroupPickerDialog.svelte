<script lang="ts">
  /* "Discuss in a group" (PLAN-team-tickets T12b): pick one of the
     caller's Team groups, or make a new one, and the ticket is attached to
     that group's chat — it then shows on the ticket as a 👥 session.
     Membership stays where it is managed today, in the group itself. */
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import { listAgentRoster, listGroups, runApi, type AgentItem, type GroupItem } from "../api/team.js";
  import { attachSession } from "../api/tickets.js";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import NewGroupDialog from "./NewGroupDialog.svelte";

  type Props = { base: string; ticketId: string; projectId?: string; onClose: () => void; onAttached: (groupId: string) => void };
  let { base, ticketId, projectId, onClose, onAttached }: Props = $props();

  let groups = $state<GroupItem[] | null>(null);
  let agents = $state<AgentItem[]>([]);
  let error = $state("");
  let busy = $state("");
  let creating = $state(false);

  $effect(() => {
    runApi(listGroups(base))
      .then((r) => (groups = r.groups ?? []))
      .catch((e) => { groups = []; error = e instanceof Error ? e.message : String(e); });
    runApi(listAgentRoster(base))
      .then((r) => (agents = (r.agents ?? []).filter((a) => !(a as { shared_by?: string }).shared_by)))
      .catch(() => {});
  });

  async function pick(g: GroupItem) {
    if (busy) return;
    busy = g.id;
    error = "";
    try {
      await Effect.runPromise(attachSession(base, ticketId, g.id, projectId).pipe(Effect.provide(WickClientLayer)));
      onAttached(g.id);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = "";
    }
  }
</script>

{#if creating}
  <NewGroupDialog {base} {agents} onClose={() => (creating = false)} onCreated={(g) => { creating = false; void pick(g); }} />
{:else}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black-900/40 p-4" role="dialog" aria-modal="true" aria-label="Pick a Team group" data-testid="group-picker">
    <div class="flex max-h-full w-full max-w-md flex-col rounded-2xl bg-white-100 shadow-xl dark:bg-navy-800">
      <div class="flex items-center border-b border-white-300 px-5 py-4 dark:border-navy-600">
        <h2 class="flex-1 text-sm font-semibold text-black-900 dark:text-white-100">Pick a Team group</h2>
        <button type="button" aria-label="Close" onclick={onClose} class="rounded-lg px-2 py-1 text-black-700 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-700">✕</button>
      </div>
      <div class="flex min-h-0 flex-col gap-2 overflow-y-auto px-5 py-4">
        <p class="text-xs text-black-700 dark:text-black-600">The ticket is attached to the group's chat. Members are managed in the group, as today.</p>
        {#if groups === null}
          <p class="text-xs text-black-700 dark:text-black-600">Loading groups…</p>
        {:else}
          {#each groups as g (g.id)}
            <button
              type="button"
              data-testid="group-picker-item"
              disabled={!!busy}
              onclick={() => pick(g)}
              class="flex items-center gap-3 rounded-lg border border-white-300 px-3 py-2 text-left hover:border-green-500 disabled:opacity-60 dark:border-navy-600"
            >
              <span aria-hidden="true">👥</span>
              <span class="min-w-0 flex-1 truncate text-sm text-black-900 dark:text-white-100">{g.name}</span>
              <span class="flex -space-x-1.5">
                {#each g.members.slice(0, 4) as m (m.id)}
                  <span title={m.name || m.handle}><AgentAvatar kind={m.avatar?.kind} shape={m.avatar?.shape} color={m.avatar?.color} expression={m.avatar?.expression} image={m.avatar?.image} size={20} /></span>
                {/each}
              </span>
            </button>
          {/each}
          <button
            type="button"
            data-testid="group-picker-new"
            onclick={() => (creating = true)}
            class="rounded-lg border border-dashed border-white-400 px-3 py-2 text-left text-sm text-green-600 hover:border-green-500 dark:border-navy-500 dark:text-green-400"
          >＋ New group</button>
        {/if}
        {#if error}<p class="text-xs text-neg-400" data-testid="group-picker-error">{error}</p>{/if}
      </div>
    </div>
  </div>
{/if}
