<script lang="ts">
  /* "Percakapan lain": every conversation of one agent, main chat pinned
     first. Picking one opens it in place inside the app — the agent's other
     sessions are ordinary sessions, the app just filters to this agent.
     Any other chat can be pinned as the main chat (a manual context reset):
     what goes to "Main chat" lands there from then on. A shared agent gets
     You | All: All lists every person's chats with it (one main each), and
     only the caller's own chats can be pinned. Rows stay live off the
     sessions stream the app already listens to (activity), no polling. */
  import { onMount, untrack } from "svelte";
  import DrawerHeader from "./DrawerHeader.svelte";
  import { timeAgo } from "../timeFormat.js";
  import { lifecycleCls } from "../lifecycleCls.js";
  import { listAgentSessionsAll, setAgentMainChat, runApi, type AgentItem, type AgentSessionItem } from "../api/team.js";
  import type { SessionActivity } from "../stores/sessionsStream.js";
  import { orderChats, pinChatVia, chatsForTab, chatLabel, filterChats, chatLifecycle, type ChatsTab } from "../agentChats.js";

  type Props = {
    base: string;
    agent: AgentItem;
    current: string;
    onClose: () => void;
    onPick: (id: string, main: boolean) => void;
    onNew: () => void;
    /** A chat became the main chat. */
    onPinned?: (id: string) => void;
    /** The newest turn step from the app's sessions stream. */
    activity?: SessionActivity | null;
  };
  let { base, agent, current, onClose, onPick, onNew, onPinned, activity = null }: Props = $props();

  let items = $state<AgentSessionItem[]>([]);
  let shared = $state(false);
  let tab = $state<ChatsTab>("you");
  let query = $state("");
  let loading = $state(true);
  let error = $state("");
  let pinning = $state("");
  let pinned = $state("");

  const shown = $derived(filterChats(chatsForTab(items, shared ? tab : "you"), query, shared ? tab : "you"));
  const viewTab = $derived<ChatsTab>(shared ? tab : "you");
  const tabHasChats = $derived(chatsForTab(items, viewTab).length > 0);

  async function load() {
    try {
      const r = await runApi(listAgentSessionsAll(base, agent.id));
      items = orderChats(r?.sessions ?? []);
      shared = !!r?.shared;
      if (!shared) tab = "you";
      error = "";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  onMount(() => {
    void load();
  });

  /* A turn step of one of these chats: working while it runs; when it
     ends, one read brings the real lifecycle and last active. */
  $effect(() => {
    const ev = activity;
    if (!ev?.session_id) return;
    // Only the event drives this; the rows are read, not tracked.
    untrack(() => {
      const row = items.find((s) => s.id === ev.session_id);
      if (!row) return;
      if (ev.work !== "") {
        if (row.lifecycle !== "working") items = items.map((s) => (s.id === ev.session_id ? { ...s, lifecycle: "working" } : s));
      } else if (chatLifecycle(row) === "working" || chatLifecycle(row) === "queued") {
        void load();
      }
    });
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

  function searchKey(e: KeyboardEvent) {
    if (e.key === "Escape" && query) {
      e.stopPropagation();
      query = "";
    }
  }
</script>

<DrawerHeader title="Chats" subtitle={`${agent.name} · @${agent.handle}`} avatar={agent.avatar} {onClose} />
{#if shared}
  <div class="flex shrink-0 gap-1 border-b border-white-300 px-4 dark:border-navy-600" role="tablist" aria-label="Chats">
    {#each [{ key: "all", label: "All" }, { key: "you", label: "Yours" }] as t (t.key)}
      <button
        type="button"
        role="tab"
        aria-selected={tab === t.key}
        class="-mb-px border-b-2 px-3 py-2.5 text-sm {tab === t.key ? 'border-green-500 font-medium text-black-900 dark:text-white-100' : 'border-transparent text-black-800 hover:text-black-900 dark:text-black-600 dark:hover:text-white-100'}"
        onclick={() => (tab = t.key as ChatsTab)}
      >{t.label}</button>
    {/each}
  </div>
{/if}
<div class="flex-1 overflow-y-auto px-4 py-4">
  <button
    type="button"
    class="mb-4 w-full rounded-lg border border-dashed border-white-400 px-4 py-2 text-sm font-medium text-black-900 hover:bg-white-200 dark:border-navy-500 dark:text-white-100 dark:hover:bg-navy-600"
    onclick={onNew}
  >+ New chat</button>
  {#if !loading && !error && items.length > 0}
    <div class="relative mb-4">
      <svg viewBox="0 0 16 16" class="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-black-600 dark:text-black-700" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
        <circle cx="6.5" cy="6.5" r="4.5"></circle>
        <path d="M10.5 10.5l3 3" stroke-linecap="round"></path>
      </svg>
      <input
        type="text"
        placeholder="Search chats..."
        aria-label="Search chats"
        bind:value={query}
        onkeydown={searchKey}
        class="w-full rounded-lg border border-white-400 bg-white-100 py-2 pl-9 pr-4 text-sm text-black-900 placeholder-black-600 focus:border-green-500 focus:outline-none focus:ring-2 focus:ring-green-200 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100 dark:placeholder-black-700 dark:focus:ring-green-800"
      />
    </div>
  {/if}
  {#if loading}
    <p class="text-sm text-black-800 dark:text-black-600">Loading…</p>
  {:else if error}
    <p class="text-sm text-neg-400">{error}</p>
  {:else if !tabHasChats}
    <p class="text-sm text-black-800 dark:text-black-600">No chats yet.</p>
  {:else if shown.length === 0}
    <p class="text-sm text-black-800 dark:text-black-600">No chats match “{query.trim()}”.</p>
  {/if}
  {#each shown as s (s.id)}
    {@const lc = chatLifecycle(s)}
    {@const mine = s.mine !== false}
    <div
      class="group mb-1 flex w-full items-center rounded-lg {s.id === current
        ? 'bg-white-200 dark:bg-navy-600'
        : 'hover:bg-white-200 dark:hover:bg-navy-600'}"
    >
      <button
        type="button"
        class="flex min-w-0 flex-1 items-center gap-3 px-3 py-2 text-left"
        onclick={() => onPick(s.id, s.agent_main && mine)}
      >
        <span class="min-w-0 flex-1">
          <span class="flex items-center gap-1.5 truncate text-sm text-black-900 dark:text-white-100">
            {#if (s.participants ?? 0) > 1}
              <span
                class="shrink-0 text-black-600 dark:text-black-600"
                title={`Shared conversation — ${s.participants} people`}
                aria-label={`Shared conversation, ${s.participants} people`}
              >
                <svg class="h-3.5 w-3.5" viewBox="0 0 20 20" fill="currentColor" aria-hidden="true">
                  <path d="M7 8a3 3 0 1 0 0-6 3 3 0 0 0 0 6ZM14.5 9a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5ZM1.6 16.2C1.6 13.3 4 11 7 11s5.4 2.3 5.4 5.2c0 .4-.3.8-.8.8H2.4a.8.8 0 0 1-.8-.8ZM13.7 17a2 2 0 0 0 .2-.8c0-1.8-.7-3.4-1.9-4.6.8-.4 1.6-.6 2.5-.6 2.4 0 4.3 1.8 4.3 4.2 0 .4-.3.8-.8.8h-4.3Z"></path>
                </svg>
              </span>
            {/if}
            <span class="truncate">{chatLabel(s, viewTab)}</span>
          </span>
          <span class="mt-0.5 flex items-center gap-2">
            <span class="truncate text-xs text-black-800 dark:text-black-600">{s.last_active ? timeAgo(s.last_active) : ""}</span>
            {#if lc}
              <span class={"rounded px-1.5 py-0.5 text-[10px] font-medium " + lifecycleCls(lc)}>{lc}</span>
            {/if}
            {#if viewTab === "all" && !s.agent_main}
              <span class="truncate text-xs text-black-800 dark:text-black-600">{mine ? "You" : s.owner_name || s.owner_user_id}</span>
            {/if}
          </span>
          {#if s.agent_main && s.id === pinned}
            <span class="mt-0.5 block text-xs text-green-600 dark:text-green-400">Now the main chat — Slack mentions and schedules to Main chat land here.</span>
          {/if}
        </span>
      </button>
      {#if mine && !s.agent_main}
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
