<script lang="ts">
  /* One group chat: header (stacked avatars, name, @handles, ⋯), the
     shared thread with members' replies side by side, the composer with
     the routing hint, and the group Settings drawer. Members answer in
     their own sessions server-side (team_group.go); this view only reads
     the group thread and listens for its group_turn / group_typing /
     system_event pushes, with a short poll as the fallback while a reply
     is pending. Group Settings opens in the app's drawer (GroupSettings). */
  import { onMount } from "svelte";
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import GroupAvatars from "./GroupAvatars.svelte";
  import { renderMarkdown } from "../markdown.js";
  import {
    groupConversation, sendToGroup, markGroupRead, runApi,
    type AgentItem, type GroupItem, type GroupTurn,
  } from "../api/team.js";
  import { backingLink, composerHint, handlesLine, mergeTurn } from "../teamGroups.js";
  import { formatAgentsRoute, navigate } from "../agentsRouter.js";
  import { getSystemEvent } from "../systemEvents.js";

  type Props = {
    base: string;
    group: GroupItem;
    agents: AgentItem[];
    onMenu?: () => void;
    onSettings: () => void;
  };
  let { base, group, agents, onMenu, onSettings }: Props = $props();

  let turns = $state<GroupTurn[]>([]);
  let text = $state("");
  let sending = $state(false);
  let error = $state("");
  let typing = $state<Record<string, boolean>>({});
  let pendingUntil = $state(0);
  let threadEl = $state<HTMLDivElement | null>(null);

  const hint = $derived(composerHint(group));
  const byId = $derived(Object.fromEntries(group.members.map((m) => [m.id, m])));
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
    const es = new EventSource(`${base}/stream?session=${encodeURIComponent(group.id)}`, { withCredentials: true });
    const onTurn = (ev: MessageEvent) => {
      try {
        turns = mergeTurn(turns, JSON.parse(ev.data) as GroupTurn);
      } catch {
        /* a malformed push is ignored; the poll catches up */
      }
    };
    es.addEventListener("group_turn", onTurn);
    es.addEventListener("system_event", onTurn);
    es.addEventListener("group_typing", (ev: MessageEvent) => {
      try {
        const d = JSON.parse(ev.data) as { agent_id: string; state: string };
        typing = { ...typing, [d.agent_id]: d.state === "start" };
      } catch {
        /* ignore */
      }
    });
    const poll = setInterval(() => {
      if (Date.now() < pendingUntil && document.visibilityState === "visible") load();
    }, 4000);
    return () => {
      es.close();
      clearInterval(poll);
      runApi(markGroupRead(base, group.id)).catch(() => {});
    };
  });

  $effect(() => {
    void turns.length;
    if (threadEl) queueMicrotask(() => threadEl && (threadEl.scrollTop = threadEl.scrollHeight));
  });

  async function send() {
    const t = text.trim();
    if (!t || sending) return;
    sending = true;
    error = "";
    try {
      await runApi(sendToGroup(base, group.id, t));
      text = "";
      pendingUntil = Date.now() + 3 * 60_000;
      await load();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      sending = false;
    }
  }
  function onKey(e: KeyboardEvent) {
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      send();
    }
  }

  function toneCls(kind: string | undefined, isError?: boolean) {
    if (isError) return "border-neg-300 text-neg-400";
    return getSystemEvent(kind).tone === "warn" ? "border-amber-300 text-amber-700 dark:text-amber-300" : "border-white-300 text-black-800 dark:border-navy-600 dark:text-black-600";
  }
</script>

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
      {#if t.role === "system"}
        <div class="flex justify-center">
          <span class="rounded-full border px-3 py-1 text-xs {toneCls(t.kind, t.is_error)}">{t.text}</span>
        </div>
      {:else if t.role === "user"}
        <div class="flex justify-end">
          <div class="max-w-[80%] whitespace-pre-wrap rounded-2xl rounded-br-md bg-green-500 px-3.5 py-2 text-sm text-white-100">{t.text}</div>
        </div>
      {:else}
        {@const m = t.speaker ? byId[t.speaker.agent_id] : undefined}
        <div class="flex items-start gap-2.5" data-testid="group-reply">
          <AgentAvatar kind={m?.avatar?.kind} shape={m?.avatar?.shape ?? "circle"} expression={m?.avatar?.expression} color={m?.avatar?.color ?? "#888"} size={30} />
          <div class="min-w-0 max-w-[80%]">
            <div class="mb-0.5 text-xs font-semibold text-black-900 dark:text-white-100">{m?.name ?? t.speaker?.handle ?? "agent"} <span class="font-normal text-black-700">@{t.speaker?.handle ?? ""}</span></div>
            <div class="prose-sm rounded-2xl rounded-tl-md bg-white-200 px-3.5 py-2 text-sm text-black-900 dark:bg-navy-700 dark:text-white-100">{@html renderMarkdown(t.text)}</div>
            {@const link = backingLink(t)}
            {#if link}
              <a
                class="mt-0.5 inline-block text-[11px] text-black-700 hover:text-green-600 hover:underline dark:hover:text-green-400"
                href={formatAgentsRoute({ handle: link.handle, session: link.session, panel: null }, base)}
                data-testid="backing-link"
                onclick={(e) => { e.preventDefault(); navigate({ handle: link.handle, session: link.session, panel: null }); }}
              >open in @{link.handle}'s chat ↗</a>
            {/if}
          </div>
        </div>
      {/if}
    {/each}
  </div>

  <div class="shrink-0 border-t border-white-300 px-4 py-3 dark:border-navy-600">
    <div class="flex items-end gap-2">
      <textarea
        bind:value={text}
        onkeydown={onKey}
        rows="2"
        placeholder="Message {group.name} — start a line with @handle to ask one member"
        aria-label="Message the group"
        class="min-h-[44px] flex-1 resize-none rounded-xl border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
      ></textarea>
      <button type="button" class="rounded-xl bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50" disabled={sending || !text.trim()} onclick={send}>Send</button>
    </div>
    <p class="mt-1.5 text-xs text-black-800 dark:text-black-600" data-testid="group-hint">{hint.route} · {hint.cap}</p>
    {#if error}<p class="mt-1 text-xs text-neg-400">{error}</p>{/if}
  </div>
</div>
