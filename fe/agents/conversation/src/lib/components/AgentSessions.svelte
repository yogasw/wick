<script lang="ts">
  /* "Percakapan lain": every conversation of one agent, main chat pinned
     first. Picking one opens it in place inside the app — the agent's other
     sessions are ordinary sessions, the app just filters to this agent. */
  import { onMount } from "svelte";
  import DrawerHeader from "./DrawerHeader.svelte";
  import { timeAgo } from "../timeFormat.js";
  import { listAgentSessions, runApi, isWorking, type AgentItem, type AgentSessionItem } from "../api/team.js";

  type Props = {
    base: string;
    agent: AgentItem;
    current: string;
    onClose: () => void;
    onPick: (id: string, main: boolean) => void;
    onNew: () => void;
  };
  let { base, agent, current, onClose, onPick, onNew }: Props = $props();

  let items = $state<AgentSessionItem[]>([]);
  let loading = $state(true);
  let error = $state("");

  onMount(async () => {
    try {
      const r = (await runApi(listAgentSessions(base, agent.id))) ?? [];
      // Main chat first, the rest keep the server's newest-first order.
      items = [...r.filter((s) => s.agent_main), ...r.filter((s) => !s.agent_main)];
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  });
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
    <button
      type="button"
      class="mb-1 flex w-full items-center gap-3 rounded-lg px-3 py-2 text-left {s.id === current
        ? 'bg-white-200 dark:bg-navy-600'
        : 'hover:bg-white-200 dark:hover:bg-navy-600'}"
      onclick={() => onPick(s.id, s.agent_main)}
    >
      <span class="min-w-0 flex-1">
        <span class="block truncate text-sm text-black-900 dark:text-white-100">
          {s.agent_main ? "📌 Main chat" : s.label || s.id}
        </span>
        <span class="block truncate text-xs text-black-800 dark:text-black-600">
          {s.last_active ? timeAgo(s.last_active) : ""}{isWorking(s.status) ? " · working…" : ""}
        </span>
      </span>
    </button>
  {/each}
</div>
