<script lang="ts">
  /* "Percakapan lain": every conversation of one agent, main chat pinned
     first. Picking one opens it in place inside the app — the agent's other
     sessions are ordinary sessions, the app just filters to this agent.
     Any other chat can be pinned as the main chat (a manual context reset):
     what goes to "Main chat" lands there from then on. */
  import { onMount } from "svelte";
  import DrawerHeader from "./DrawerHeader.svelte";
  import { timeAgo } from "../timeFormat.js";
  import { listAgentSessions, setAgentMainChat, runApi, isWorking, type AgentItem, type AgentSessionItem } from "../api/team.js";
  import { orderChats, pinChatVia } from "../agentChats.js";

  type Props = {
    base: string;
    agent: AgentItem;
    current: string;
    onClose: () => void;
    onPick: (id: string, main: boolean) => void;
    onNew: () => void;
    /** A chat became the main chat. */
    onPinned?: (id: string) => void;
  };
  let { base, agent, current, onClose, onPick, onNew, onPinned }: Props = $props();

  let items = $state<AgentSessionItem[]>([]);
  let loading = $state(true);
  let error = $state("");
  let pinning = $state("");
  let pinned = $state("");

  onMount(async () => {
    try {
      const r = (await runApi(listAgentSessions(base, agent.id))) ?? [];
      items = orderChats(r);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  });

  async function pin(id: string) {
    if (pinning) return;
    pinning = id;
    error = "";
    try {
      items = await pinChatVia(items, id, (sid) => runApi(setAgentMainChat(base, agent.id, sid)));
      pinned = id;
      onPinned?.(id);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      pinning = "";
    }
  }
</script>

<DrawerHeader title="Chats" subtitle={`${agent.name} · @${agent.handle}`} avatar={agent.avatar} {onClose} />
<div class="flex-1 overflow-y-auto px-4 py-4">
  <button
    type="button"
    class="mb-4 w-full rounded-lg border border-dashed border-white-400 px-4 py-2 text-sm font-medium text-black-900 hover:bg-white-200 dark:border-navy-500 dark:text-white-100 dark:hover:bg-navy-600"
    onclick={onNew}
  >+ New chat</button>
  {#if loading}
    <p class="text-sm text-black-800 dark:text-black-600">Loading…</p>
  {:else if error}
    <p class="text-sm text-neg-400">{error}</p>
  {:else if items.length === 0}
    <p class="text-sm text-black-800 dark:text-black-600">No chats yet.</p>
  {/if}
  {#each items as s (s.id)}
    <div
      class="group mb-1 flex w-full items-center rounded-lg {s.id === current
        ? 'bg-white-200 dark:bg-navy-600'
        : 'hover:bg-white-200 dark:hover:bg-navy-600'}"
    >
      <button
        type="button"
        class="flex min-w-0 flex-1 items-center gap-3 px-3 py-2 text-left"
        onclick={() => onPick(s.id, s.agent_main)}
      >
        <span class="min-w-0 flex-1">
          <span class="block truncate text-sm text-black-900 dark:text-white-100">
            {s.agent_main ? "📌 Main chat" : s.label || s.id}
          </span>
          <span class="block truncate text-xs text-black-800 dark:text-black-600">
            {s.last_active ? timeAgo(s.last_active) : ""}{isWorking(s.status) ? " · working…" : ""}
          </span>
          {#if s.agent_main && s.id === pinned}
            <span class="mt-0.5 block text-xs text-green-600 dark:text-green-400">Now the main chat — Slack mentions and schedules to Main chat land here.</span>
          {/if}
        </span>
      </button>
      {#if !s.agent_main}
        <button
          type="button"
          class="mr-2 rounded-md p-1.5 text-black-800 opacity-0 hover:bg-white-300 hover:text-black-900 focus:opacity-100 group-hover:opacity-100 disabled:opacity-50 dark:text-black-600 dark:hover:bg-navy-500 dark:hover:text-white-100"
          title="Set as main chat"
          aria-label="Set as main chat"
          disabled={!!pinning}
          onclick={() => pin(s.id)}
        >
          <svg class="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 17v5"></path><path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z"></path></svg>
        </button>
      {/if}
    </div>
  {/each}
</div>
