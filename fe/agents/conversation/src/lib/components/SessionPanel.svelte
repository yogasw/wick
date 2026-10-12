<script lang="ts">
  /* The rail's Session tab, on every session: where the chat lives (Info),
     which agents answer in it (Agents) and which people wrote (People).
     It replaces the strip that used to sit over the chat: nothing about
     the session is pinned on top of the conversation any more.

     The home agent answers messages without @; every other agent came in
     through a mention (or its owner writing). Agents and people both come
     from the server, counted over the whole history (agents topped up by
     replies loaded since), and only for a viewer who may open the session. */
  import type { NotesResponse } from "../types/agents.js";
  import type { SessionAgent } from "../ticketSession.js";
  import { timeAgo } from "../timeFormat.js";

  type Props = {
    info: NotesResponse | null;
    /** The home agent's name ("Project agent" for a plain project chat). */
    homeName: string;
    /** "@owner" when the viewer does not own the session; "" otherwise. */
    homeOwner?: string;
    agents: SessionAgent[];
    /** Opens the ticket's page; the Ticket row is plain text without it. */
    onOpenTicket?: (ticketId: string) => void;
    /** Ticket sessions only: "Discuss in a group". */
    onDiscuss?: () => void;
  };
  let { info, homeName, homeOwner = "", agents, onOpenTicket, onDiscuss }: Props = $props();

  const s = $derived(info?.session_info);
  const ticket = $derived(info?.ticket ?? null);
  const people = $derived(info?.session_people ?? []);
  const startedBy = $derived(
    s?.started_by_name || (s?.started_by ? (info?.users?.[s.started_by] ?? "") : "") || "—",
  );
  const initial = (n: string) => (n.replace(/^@/, "").trim()[0] ?? "?").toUpperCase();
  /* A writer with no name and no handle is named by where they wrote from,
     not by an internal "slack:U0123" key. */
  const personLabel = (p: { id: string; name?: string; handle?: string; channel?: string }) =>
    p.name || (p.handle ? "@" + p.handle : p.channel ? `Unknown ${p.channel} user` : p.id);
  const replies = (n: number) => (n === 1 ? "1 reply" : `${n} replies`);
  const messages = (n: number) => (n === 1 ? "1 message" : `${n} messages`);

  const label = "text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600";
  const muted = "text-black-700 dark:text-black-600";
</script>

<div class="flex flex-1 flex-col gap-4 overflow-y-auto p-4 text-xs" data-testid="session-panel">
  <section class="flex flex-col gap-1.5" data-testid="session-info">
    <h3 class={label}>Info</h3>
    <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1">
      <dt class={muted}>Started by</dt>
      <dd class="truncate font-medium text-black-900 dark:text-white-100" data-testid="session-started-by">{startedBy}</dd>
      <dt class={muted}>Project</dt>
      <dd class="truncate font-medium text-black-900 dark:text-white-100">{s?.project_name || s?.project_id || "—"}</dd>
      <dt class={muted}>Ticket</dt>
      <dd class="truncate" data-testid="session-ticket">
        {#if ticket && onOpenTicket}
          <button type="button" class="max-w-full truncate text-left font-medium text-green-600 hover:underline dark:text-green-400" onclick={() => onOpenTicket?.(ticket.id)}>🎫 {ticket.title || ticket.id}</button>
        {:else if ticket}
          <span class="font-medium text-black-900 dark:text-white-100">🎫 {ticket.title || ticket.id}</span>
        {:else}
          <span class={muted}>—</span>
        {/if}
      </dd>
      <dt class={muted}>Group</dt>
      <dd class={muted}>—</dd>
      <dt class={muted}>Started</dt>
      <dd class="font-medium text-black-900 dark:text-white-100">{s?.created_at ? timeAgo(s.created_at) : "—"}</dd>
    </dl>
  </section>

  <section class="flex flex-col gap-2" data-testid="session-agents">
    <h3 class={label}>Agents</h3>
    <div class="flex items-start gap-2" data-testid="session-agent-home">
      <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-green-500 text-[11px] font-semibold text-white-100" aria-hidden="true">{initial(homeName)}</span>
      <div class="min-w-0">
        <div class="flex flex-wrap items-center gap-1">
          <span class="font-semibold text-black-900 dark:text-white-100">{homeName}</span>
          {#if homeOwner}<span class={muted}>· {homeOwner}</span>{/if}
          <span class="rounded-full bg-white-300 px-1.5 py-px text-[10px] font-medium text-black-800 dark:bg-navy-600 dark:text-white-100">home</span>
        </div>
        <div class={muted}>Answers messages without @</div>
      </div>
    </div>
    {#each agents as a (a.key)}
      <div class="flex items-start gap-2" data-testid="session-agent">
        <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-navy-400 text-[11px] font-semibold text-white-100" aria-hidden="true">{initial(a.name)}</span>
        <div class="min-w-0">
          <div class="flex flex-wrap items-center gap-1">
            <span class="font-semibold text-black-900 dark:text-white-100">{a.name}</span>
            {#if a.owner}<span class={muted}>· {a.owner}</span>{/if}
          </div>
          <div class={muted}>{[a.joined, replies(a.replies)].filter(Boolean).join(" · ")}</div>
        </div>
      </div>
    {/each}
  </section>

  {#if people.length}
    <section class="flex flex-col gap-2" data-testid="session-people">
      <h3 class={label}>People</h3>
      {#each people as p (p.id)}
        <div class="flex items-start gap-2" data-testid="session-person">
          <span class="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-white-300 text-[11px] font-semibold text-black-900 dark:bg-navy-600 dark:text-white-100" aria-hidden="true">{initial(p.name || p.handle || p.id)}</span>
          <div class="min-w-0">
            <div class="truncate font-semibold text-black-900 dark:text-white-100">{personLabel(p)}</div>
            <div class={muted}>{[p.owner ? "owner" : "", messages(p.messages)].filter(Boolean).join(" · ")}</div>
          </div>
        </div>
      {/each}
    </section>
  {/if}

  {#if ticket && onDiscuss}
    <button
      type="button"
      class="rounded-lg border border-white-300 px-3 py-1.5 text-xs font-medium text-black-900 hover:bg-white-200 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-700"
      data-testid="session-discuss-in-group"
      onclick={() => onDiscuss?.()}
    >👥 Discuss in a group</button>
  {/if}
</div>
