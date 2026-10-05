<script lang="ts">
  /* One group chat: header (stacked avatars, name, @handles, ⋯), the
     shared thread with members' replies side by side — drawn by the same
     ThreadMessage as an agent's chat — and the same Composer, its @ menu
     limited to the members and its caption naming the routing and cap, and the group Settings drawer. Members answer in
     their own sessions server-side (team_group.go); this view only reads
     the group thread and listens for its group_turn / group_typing /
     system_event pushes on the shared session stream (the SharedWorker's
     one connection, never an EventSource of its own). A slow poll covers a
     pending reply only while that stream is down, and a reconnect reloads
     the thread. Group Settings opens in the app's drawer (GroupSettings). */
  import { onMount } from "svelte";
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import GroupAvatars from "./GroupAvatars.svelte";
  import { Composer } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { killProcess } from "../api/processes.js";
  import ThreadMessage from "./ThreadMessage.svelte";
  import { teamMentionAgents } from "../teamMention.js";
  import type { ConversationTurn } from "../types/agents.js";
  import {
    groupConversation, sendToGroup, markGroupRead, runApi,
    type AgentItem, type GroupItem, type GroupTurn,
  } from "../api/team.js";
  import { backingLink, composerHint, handlesLine, mergeTurn } from "../teamGroups.js";
  import { formatAgentsRoute, navigate } from "../agentsRouter.js";
  import { connectSession } from "../stores/sse.js";
  import type { AgentEvent } from "../types/agents.js";

  /* Fallback poll while a reply is pending AND the stream is down. */
  const DROPPED_POLL_MS = 60_000;

  type Props = {
    base: string;
    group: GroupItem;
    agents: AgentItem[];
    onMenu?: () => void;
    onSettings: () => void;
  };
  let { base, group, agents, onMenu, onSettings }: Props = $props();

  let turns = $state<GroupTurn[]>([]);
  let sending = $state(false);
  let error = $state("");
  let typing = $state<Record<string, boolean>>({});
  // Each member answers in its own backing session; the typing push names
  // it so Stop knows which turns to kill.
  let typingSession = $state<Record<string, string>>({});
  let pendingUntil = $state(0);
  let threadEl = $state<HTMLDivElement | null>(null);

  const hint = $derived(composerHint(group));
  const byId = $derived(Object.fromEntries(group.members.map((m) => [m.id, m])));
  /* The @ menu offers the group's enabled members only. */
  const mentionAgents = $derived(
    teamMentionAgents(
      group.members.map((m) => ({ id: m.id, handle: m.handle, name: m.name, description: "", disabled: m.disabled, avatar: m.avatar })),
      "",
    ),
  );
  const teamAgents = $derived(
    Object.fromEntries(group.members.map((m) => [m.handle, { name: m.name, kind: m.avatar?.kind, shape: m.avatar?.shape, color: m.avatar?.color, expression: m.avatar?.expression }])),
  );
  const typingHandles = $derived(group.members.filter((m) => typing[m.id]).map((m) => m.handle));

  async function load() {
    try {
      const r = await runApi(groupConversation(base, group.id));
      turns = r.turns ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  onMount(() => {
    load();
    runApi(markGroupRead(base, group.id)).catch(() => {});
    /* Pushes ride the stream as `agent` frames whose type names the push
       and whose data is its JSON body. */
    const stream = connectSession(base, group.id);
    stream.onEvent((ev: AgentEvent) => {
      if (!ev.data) return;
      try {
        if (ev.type === "group_turn" || ev.type === "system_event") {
          turns = mergeTurn(turns, JSON.parse(ev.data) as GroupTurn);
        } else if (ev.type === "group_typing") {
          const d = JSON.parse(ev.data) as { agent_id: string; state: string; session_id?: string };
          typing = { ...typing, [d.agent_id]: d.state === "start" };
          if (d.session_id) typingSession = { ...typingSession, [d.agent_id]: d.session_id };
        }
      } catch {
        /* a malformed push is ignored; a reconnect reloads */
      }
    });
    /* A drop may have swallowed pushes: reload once it is back. */
    let dropped = false;
    const unsubStatus = stream.status.subscribe((st) => {
      if (st === "error") dropped = true;
      else if (st === "connected" && dropped) {
        dropped = false;
        load();
      }
    });
    const poll = setInterval(() => {
      if (dropped && Date.now() < pendingUntil && document.visibilityState === "visible") load();
    }, DROPPED_POLL_MS);
    return () => {
      unsubStatus();
      stream.close();
      clearInterval(poll);
      runApi(markGroupRead(base, group.id)).catch(() => {});
    };
  });

  $effect(() => {
    void turns.length;
    if (threadEl) queueMicrotask(() => threadEl && (threadEl.scrollTop = threadEl.scrollHeight));
  });

  /* The composer's Stop: kills the turn of every member typing right now,
     no confirm. A member whose push carried no session (an older server)
     falls back to the session its last reply came from. */
  async function stopTyping() {
    const ids = Object.keys(typing).filter((id) => typing[id]);
    const sids = ids
      .map((id) => typingSession[id] || [...turns].reverse().find((t) => t.speaker?.agent_id === id)?.speaker?.session_id || "")
      .filter(Boolean);
    if (!sids.length) return;
    const res = await Promise.allSettled(sids.map((sid) => runApi(killProcess(base, sid))));
    const failed = res.find((r) => r.status === "rejected");
    if (failed) {
      const e = (failed as PromiseRejectedResult).reason;
      toastError(`Stop: ${e instanceof Error ? e.message : String(e)}`);
      return;
    }
    typing = {};
    toastOk("Stopped");
    await load();
  }

  async function send(msg: { text: string; files: File[] }) {
    const t = msg.text.trim();
    if (!t || sending) return;
    if (msg.files.length) error = "Files can't be sent to a group yet — only the text went.";
    sending = true;
    try {
      await runApi(sendToGroup(base, group.id, t));
      if (!msg.files.length) error = "";
      pendingUntil = Date.now() + 3 * 60_000;
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      sending = false;
    }
  }

</script>

{#snippet mentionAvatar(a: { kind?: string; shape?: string; color?: string; expression?: string })}
  <AgentAvatar kind={a.kind} shape={a.shape} expression={a.expression} color={a.color} size={18} />
{/snippet}

<div class="flex h-full min-w-0 flex-col" data-testid="group-view">
  <header class="flex h-16 shrink-0 items-center gap-3 border-b border-white-300 px-4 dark:border-navy-600" data-testid="group-header">
    {#if onMenu}
      <button type="button" class="lg:hidden flex h-8 w-8 items-center justify-center rounded-lg text-black-800 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600" aria-label="Agent list" onclick={onMenu}>
        <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M2 4h12M2 8h12M2 12h12"></path></svg>
      </button>
    {/if}
    <GroupAvatars members={group.members} size={30} />
    <div class="min-w-0 flex-1">
      <div class="truncate text-base font-semibold text-black-900 dark:text-white-100">{group.name}</div>
      <div class="truncate text-xs text-black-800 dark:text-black-600">
        {#if typingHandles.length}
          <span class="font-medium text-green-600 dark:text-green-400">@{typingHandles.join(", @")} typing<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span></span> ·
        {/if}
        {handlesLine(group.members)}
      </div>
    </div>
    <button type="button" class="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-black-800 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600" title="Group settings" aria-label="Group menu" onclick={onSettings}>⋯</button>
  </header>

  <div class="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-4" bind:this={threadEl} data-testid="group-thread">
    {#if turns.length === 0}
      <p class="py-10 text-center text-sm text-black-800 dark:text-black-600">Say hi to {group.name}. A message with no @ goes to @{group.responder}.</p>
    {/if}
    {#each turns as t, i (t.turn_id ?? i)}
      <ThreadMessage turn={t as unknown as ConversationTurn} {teamAgents} />
      {#if t.role === "assistant"}
        {@const link = backingLink(t)}
        {#if link}
          <a
            style="margin-left:28px" class="-mt-2 inline-block text-[11px] text-black-700 hover:text-green-600 hover:underline dark:hover:text-green-400"
            href={formatAgentsRoute({ handle: link.handle, session: link.session, panel: null }, base)}
            data-testid="backing-link"
            onclick={(e) => { e.preventDefault(); navigate({ handle: link.handle, session: link.session, panel: null }); }}
          >open in @{link.handle}'s chat ↗</a>
        {/if}
      {/if}
    {/each}
    {#if typingHandles.length}
      <p class="text-xs font-medium text-green-600 dark:text-green-400" data-testid="group-typing">@{typingHandles.join(", @")} typing<span class="dots" aria-hidden="true"><i></i><i></i><i></i></span></p>
    {/if}
  </div>

  <div class="shrink-0 px-2 pb-2" data-testid="group-composer">
    <Composer
      onSend={send}
      disabled={sending}
      placeholder={`Message ${group.name} — @ asks one member`}
      {mentionAgents}
      {mentionAvatar}
      caption={hint.caption}
      running={typingHandles.length > 0}
      onStop={stopTyping}
    />
    {#if error}<p class="mt-1 px-2 text-xs text-neg-400">{error}</p>{/if}
  </div>
</div>

<style>
  /* The typing dots, as in the roster (AgentsApp's rule is scoped there). */
  .dots i {
    display: inline-block;
    width: 4px;
    height: 4px;
    margin-left: 2px;
    border-radius: 9999px;
    background: currentColor;
    animation: group-dot 1s infinite;
  }
  .dots i:nth-child(2) { animation-delay: 0.15s; }
  .dots i:nth-child(3) { animation-delay: 0.3s; }
  @keyframes group-dot {
    0%, 60%, 100% { opacity: 0.3; transform: translateY(0); }
    30% { opacity: 1; transform: translateY(-2px); }
  }
  @media (prefers-reduced-motion: reduce) {
    .dots i { animation: none; opacity: 0.7; }
  }
</style>
