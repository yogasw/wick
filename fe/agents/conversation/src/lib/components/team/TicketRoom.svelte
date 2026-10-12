<script lang="ts">
  /* A ticket's room on its Team › Tickets page (PLAN-team-tickets §4/§5):
     a group chat that belongs to the ticket, where each person's own Team
     agents answer that person only. Sits between the ticket's description
     and its one-agent Sessions, which stay as they are.

     - No room yet: "Open room", or just write — the first message opens it.
     - Members: your agents (removable, the home member never), other
       people's (greyed: only their owner can ask them). "+ Add agents"
       lists your own Team only; "🧭 Ask Captain" brings yours in.
     - The thread reads like a group chat (ThreadMessage), system lines
       ("only takes instructions from @hana", the hop cap) as chips.
     - Context: your "Share the full thread" toggle.
     Live updates ride the shared session stream as in GroupView (one
     connection, no EventSource of its own); a reconnect reloads. */
  import { onMount } from "svelte";
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import { Composer } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import ThreadMessage from "../ThreadMessage.svelte";
  import Dots from "./Dots.svelte";
  import type { AgentEvent, ConversationTurn } from "../../types/agents.js";
  import {
    addRoomMembers,
    getTicketRoom,
    openTicketRoom,
    removeRoomMember,
    setRoomShareFull,
    type TicketRoom,
    type TicketRoomMember,
  } from "../../api/tickets.js";
  import { groupConversation, listAgentRoster, runApi, sendToGroup, type GroupTurn } from "../../api/team.js";
  import { isSharedAgent } from "../../agentSharing.js";
  import { mergeTurn } from "../../teamGroups.js";
  import { connectSession } from "../../stores/sse.js";
  import { addable, memberLabel, responderHint, roomMentionAgents, roomSpeakers, roomTurnView, type OwnAgent } from "../../ticketRoom.js";

  type Props = {
    base: string;
    ticketId: string;
    /** The ticket's project, for the empty state ("opens it in …"). */
    /** The ticket's project: rooms are per (project, ticket). */
    projectId: string;
    projectName?: string;
    /** Told the room's session id once there is a room. */
    onSession?: (sessionId: string) => void;
  };
  let { base, ticketId, projectId, projectName = "", onSession }: Props = $props();

  let room = $state<TicketRoom | null>(null);
  let loaded = $state(false);
  let own = $state<OwnAgent[]>([]);
  let turns = $state<GroupTurn[]>([]);
  let typing = $state<Record<string, string>>({}); // agent id → handle
  let busy = $state(false);
  let error = $state("");
  let picking = $state(false);
  let picked = $state<string[]>([]);

  async function loadRoom() {
    try {
      room = (await runApi(getTicketRoom(base, ticketId, projectId))).room;
      error = "";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loaded = true;
    }
  }
  async function loadTurns() {
    if (!room) return;
    try {
      turns = (await runApi(groupConversation(base, room.session_id))).turns ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  onMount(() => {
    void loadRoom();
    /* Your own Team only: a shared agent stays chat-only, out of rooms. */
    runApi(listAgentRoster(base))
      .then((r) => {
        own = (r.agents ?? [])
          .filter((a) => !isSharedAgent(a))
          .map((a) => ({ id: a.id, handle: a.handle, name: a.name, is_captain: a.is_captain, disabled: a.disabled, avatar: a.avatar }));
      })
      .catch(() => { /* no Team: only the home member answers */ });
  });

  /* The room's thread and its pushes, once there is a room. */
  const roomSession = $derived(room?.session_id ?? "");
  $effect(() => {
    const sid = roomSession;
    if (!sid) return;
    onSession?.(sid);
    void loadTurns();
    const stream = connectSession(base, sid);
    stream.onEvent((ev: AgentEvent) => {
      if (!ev.data) return;
      try {
        if (ev.type === "group_turn" || ev.type === "system_event") {
          const t = JSON.parse(ev.data) as GroupTurn & { kind?: string };
          turns = mergeTurn(turns, t);
          // Someone joined or left: the member strip follows.
          if (t.kind === "group_member_added" || t.kind === "group_member_removed") void loadRoom();
        } else if (ev.type === "group_typing") {
          const d = JSON.parse(ev.data) as { agent_id: string; handle: string; state: string };
          const next = { ...typing };
          if (d.state === "start") next[d.agent_id] = d.handle;
          else delete next[d.agent_id];
          typing = next;
        }
      } catch {
        /* a malformed push is ignored; a reconnect reloads */
      }
    });
    let dropped = false;
    const unsub = stream.status.subscribe((st) => {
      if (st === "error") dropped = true;
      else if (st === "connected" && dropped) {
        dropped = false;
        void loadTurns();
        void loadRoom();
      }
    });
    return () => {
      unsub();
      stream.close();
    };
  });

  const home = $derived(room?.members.find((m) => m.home));
  const homeName = $derived(home ? memberLabel(home) : projectName || "the project");
  const mentionAgents = $derived(roomMentionAgents(room, own));
  const caption = $derived(responderHint(room, homeName));
  const captain = $derived(own.find((a) => a.is_captain && !a.disabled));
  const captainIn = $derived(!!captain && !!room?.members.some((m) => m.mine && m.agent_id === captain.id));
  const typingHandles = $derived(Object.values(typing));
  /* Speakers by (agent, owner): another person's Captain is drawn as
     "Captain · @hana", never as yours. */
  const teamAgents = $derived(roomSpeakers(room));
  const shownTurns = $derived(turns.map((t) => roomTurnView(t, room)));

  async function ensureRoom(): Promise<TicketRoom | null> {
    if (room) return room;
    room = await runApi(openTicketRoom(base, ticketId, projectId));
    return room;
  }

  async function act(f: () => Promise<unknown>) {
    if (busy) return;
    busy = true;
    try {
      await f();
      error = "";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      toastError(error);
    } finally {
      busy = false;
    }
  }

  const openRoom = () => act(ensureRoom);

  async function send(msg: { text: string; files: File[] }) {
    const t = msg.text.trim();
    if (!t) return;
    // A room takes text only; say so rather than drop a file silently.
    const dropped = msg.files.length > 0;
    await act(async () => {
      const r = await ensureRoom();
      if (!r) return;
      await runApi(sendToGroup(base, r.session_id, t));
      // A mention may have brought one of your agents in.
      await loadRoom();
      await loadTurns();
    });
    if (dropped) error = "Files can't be sent to a room yet — only the text went.";
  }

  const addAgents = (ids: string[]) =>
    act(async () => {
      await ensureRoom();
      room = await runApi(addRoomMembers(base, ticketId, projectId, ids));
      picking = false;
      picked = [];
    });
  const askCaptain = () => captain && addAgents([captain.id]);
  const removeMember = (m: TicketRoomMember) =>
    act(async () => {
      if (m.agent_id) room = await runApi(removeRoomMember(base, ticketId, projectId, m.agent_id));
    });
  const toggleShare = (on: boolean) =>
    act(async () => {
      room = await runApi(setRoomShareFull(base, ticketId, projectId, on));
    });

  const muted = "text-black-800 dark:text-black-600";
</script>

{#snippet mentionAvatar(a: { kind?: string; shape?: string; color?: string; expression?: string })}
  <AgentAvatar kind={a.kind} shape={a.shape} expression={a.expression} color={a.color} size={18} />
{/snippet}

<section class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700" data-testid="ticket-room">
  <div class="mb-3 flex items-center gap-2">
    <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">
      👥 Room{#if room} · {room.members.length} member{room.members.length === 1 ? "" : "s"}{/if}
    </h2>
    <span class="flex-1"></span>
    {#if captain && !captainIn}
      <button type="button" class="rounded-lg border border-white-400 px-2.5 py-1 text-xs font-medium text-black-800 hover:border-green-500 hover:text-green-600 dark:border-navy-600 dark:text-black-600" data-testid="room-ask-captain" disabled={busy} onclick={askCaptain}>🧭 Ask Captain</button>
    {/if}
  </div>

  {#if !loaded}
    <p class="text-xs {muted}">Loading…</p>
  {:else if !room}
    <div class="flex flex-wrap items-center gap-3" data-testid="room-empty">
      <p class="min-w-0 flex-1 text-xs {muted}">
        No room yet. Your first message below opens it in {projectName || "this project"}; {homeName} answers unless you @mention one of your agents.
      </p>
      <button type="button" class="rounded-lg bg-green-500 px-3 py-1.5 text-xs font-semibold text-white-100 hover:bg-green-600 disabled:opacity-50" disabled={busy} onclick={openRoom}>Open room</button>
    </div>
  {:else}
    <div class="mb-3 flex flex-wrap items-center gap-1.5" data-testid="room-members">
      {#each room.members as m (m.kind + (m.agent_id ?? "") + (m.owner_id ?? ""))}
        <span
          class="flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-xs {m.mine
            ? 'border-white-400 text-black-900 dark:border-navy-600 dark:text-white-100'
            : 'border-dashed border-white-400 text-black-700 opacity-60 dark:border-navy-600'}"
          title={m.mine ? "" : `only @${m.owner_handle} can ask it`}
          data-testid={"room-member-" + (m.agent_id ?? m.kind)}
        >
          <AgentAvatar kind={m.avatar?.kind} shape={m.avatar?.shape} color={m.avatar?.color} expression={m.avatar?.expression} size={16} />
          {memberLabel(m)}{#if m.home}<span class="text-[10px] {muted}">· home</span>{/if}
          {#if m.mine && !m.home && m.agent_id}
            <button type="button" class="ml-0.5 {muted} hover:text-neg-400" aria-label={"Remove @" + m.handle} disabled={busy} onclick={() => removeMember(m)}>×</button>
          {/if}
        </span>
      {/each}
      <button type="button" class="rounded-full border border-dashed border-white-400 px-2 py-0.5 text-xs text-black-800 hover:border-green-500 hover:text-green-600 dark:border-navy-600 dark:text-black-600" data-testid="room-add" onclick={() => { picking = true; picked = []; }}>＋ Add agents</button>
    </div>

    <div class="max-h-[420px] space-y-3 overflow-y-auto py-1" data-testid="room-thread">
      {#if turns.length === 0}
        <p class="py-4 text-center text-xs {muted}">No messages yet.</p>
      {/if}
      {#each shownTurns as t, i (t.turn_id ?? i)}
        <ThreadMessage turn={t as unknown as ConversationTurn} {teamAgents} />
      {/each}
      {#if typingHandles.length}
        <p class="text-xs font-medium text-green-600 dark:text-green-400" data-testid="room-typing">@{typingHandles.join(", @")} typing<Dots /></p>
      {/if}
    </div>
  {/if}

  {#if loaded}
    <div class="mt-3" data-testid="room-composer">
      <Composer
        onSend={send}
        disabled={busy}
        placeholder="Message the room — @ asks one of your agents"
        {mentionAgents}
        {mentionAvatar}
        {caption}
      />
    </div>
  {/if}

  {#if room}
    <!-- Context: what this person's agents read of the thread. -->
    <div class="mt-4 rounded-lg border border-white-300 p-3 dark:border-navy-600" data-testid="room-context">
      <div class="text-[11px] font-semibold uppercase tracking-wide {muted}">Context sent to your agents</div>
      <label class="mt-2 flex cursor-pointer items-center gap-2 text-xs font-medium text-black-900 dark:text-white-100">
        <input type="checkbox" class="accent-green-500" checked={room.share_full} disabled={busy} data-testid="room-share" onchange={(e) => toggleShare(e.currentTarget.checked)} />
        Share the full thread with my agents
      </label>
      <p class="mt-1 text-[11px] {muted}">
        {room.share_full
          ? "Your agents get everyone's messages, other people's included. Others are written as “Captain (@hana)”."
          : "Off: your agents get the ticket, your messages and their own replies. Other people's turns stay on screen only."}
      </p>
    </div>
  {/if}

  {#if error}<p class="mt-2 text-xs text-neg-400">{error}</p>{/if}
</section>

{#if picking}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-navy-900/40 p-4" data-testid="room-add-modal">
    <div class="w-full max-w-sm rounded-xl border border-white-300 bg-white-100 p-5 shadow-xl dark:border-navy-600 dark:bg-navy-700">
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Add agents to {ticketId}</h2>
      <div class="mt-3 text-[11px] font-semibold uppercase tracking-wide {muted}">Your Team</div>
      {#if captain && !captainIn}
        <button type="button" class="mt-1 w-full rounded-lg px-2 py-1.5 text-left text-xs hover:bg-white-200 dark:hover:bg-navy-600" data-testid="room-modal-captain" disabled={busy} onclick={askCaptain}>🧭 Ask Captain</button>
      {/if}
      <div class="mt-1 flex max-h-64 flex-col gap-0.5 overflow-y-auto">
        {#each addable(room, own) as row (row.agent.id)}
          {@const on = picked.includes(row.agent.id)}
          <label class="flex items-center gap-2 rounded-lg px-2 py-1.5 text-xs {row.inRoom ? 'opacity-45' : 'cursor-pointer hover:bg-white-200 dark:hover:bg-navy-600'}">
            <input
              type="checkbox"
              class="accent-green-500"
              checked={on || row.inRoom}
              disabled={row.inRoom}
              aria-label={row.agent.name || row.agent.handle}
              onchange={(e) => (picked = e.currentTarget.checked ? [...picked, row.agent.id] : picked.filter((x) => x !== row.agent.id))}
            />
            <AgentAvatar kind={row.agent.avatar?.kind} shape={row.agent.avatar?.shape} color={row.agent.avatar?.color} expression={row.agent.avatar?.expression} size={18} />
            <span class="min-w-0 flex-1 truncate">{row.agent.name || row.agent.handle}{#if row.inRoom}<span class={muted}> · in the room</span>{/if}</span>
          </label>
        {:else}
          <p class="text-xs {muted}">You have no Team agents yet.</p>
        {/each}
      </div>
      <p class="mt-2 text-[11px] {muted}">Only your own Team agents. They work for you, with your access — your Captain with everything it can already reach, the others with this ticket.</p>
      <div class="mt-4 flex justify-end gap-2">
        <button type="button" class="rounded-lg border border-white-400 px-3 py-1.5 text-xs dark:border-navy-600" onclick={() => (picking = false)}>Cancel</button>
        <button type="button" class="rounded-lg bg-green-500 px-3 py-1.5 text-xs font-semibold text-white-100 hover:bg-green-600 disabled:opacity-50" disabled={!picked.length || busy} onclick={() => addAgents(picked)}>Add {picked.length || ""}</button>
      </div>
    </div>
  </div>
{/if}
