<script lang="ts">
  import type { TicketCard, TicketField } from "../types/agents.js";
  import { timeAgo } from "../timeFormat.js";

  type Props = {
    ticket: TicketCard;
    /* Project schema, so fields render with their labels, not raw keys. */
    schema?: TicketField[];
    /* user id → display name (assignees on this board). */
    users?: Record<string, string>;
    onOpen: (ticketId: string) => void;
    onDragStart: (e: DragEvent, ticketId: string) => void;
    /* A session row is dragged out of this card, towards another one. */
    onSessionDragStart: (e: DragEvent, sessionId: string) => void;
    /* A session was dropped ON this card — attach or move it here. */
    onSessionDrop: (ticketId: string) => void;
    onOpenSession: (sessionId: string) => void;
    /* True only while a SESSION is in flight. The card lights up as a drop
       target then, so dragging a ticket between columns does not make
       every other card glow as if it could receive it. */
    sessionDragging?: boolean;
  };

  let {
    ticket,
    schema,
    users,
    onOpen,
    onDragStart,
    onSessionDragStart,
    onSessionDrop,
    onOpenSession,
    sessionDragging = false,
  }: Props = $props();

  let dropHover = $state(false);

  const assigneeName = $derived(
    ticket.assignee ? (users?.[ticket.assignee] ?? ticket.assignee) : "",
  );

  const fieldEntries = $derived.by(() => {
    const f = ticket.fields ?? {};
    const out: { label: string; value: string }[] = [];
    // Schema order first so cards in one column line up.
    for (const def of schema ?? []) {
      if (f[def.key]) out.push({ label: def.label || def.key, value: f[def.key] });
    }
    for (const [k, v] of Object.entries(f)) {
      if (!(schema ?? []).some((d) => d.key === k) && v) out.push({ label: k, value: v });
    }
    return out;
  });

  const sessionRows = $derived(ticket.session_rows ?? []);

  function initial(name: string): string {
    return name.trim().charAt(0).toUpperCase() || "?";
  }
</script>

<div
  role="button"
  tabindex="0"
  draggable="true"
  data-testid={"ticket-card-" + ticket.id}
  ondragstart={(e) => onDragStart(e, ticket.id)}
  ondragover={(e) => {
    if (!sessionDragging) return;
    e.preventDefault();
    e.stopPropagation();
    dropHover = true;
  }}
  ondragleave={() => { dropHover = false; }}
  ondrop={(e) => {
    if (!sessionDragging) return;
    e.preventDefault();
    e.stopPropagation();
    dropHover = false;
    onSessionDrop(ticket.id);
  }}
  onclick={() => onOpen(ticket.id)}
  onkeydown={(e) => e.key === "Enter" && onOpen(ticket.id)}
  class={[
    "group cursor-pointer rounded-lg border bg-white-100 p-3 shadow-sm transition-all hover:shadow-md active:cursor-grabbing dark:bg-navy-700",
    dropHover
      ? "border-green-500 ring-2 ring-inset ring-green-500"
      : "border-white-300 hover:border-green-500 dark:border-navy-600 dark:hover:border-green-500",
  ].join(" ")}
>
  <!-- The ticket code leads: it is what gets quoted in chat and standups. -->
  <div class="flex items-center gap-2">
    <span class="rounded bg-white-200 px-1.5 py-0.5 font-mono text-[10px] font-semibold text-black-800 dark:bg-navy-800 dark:text-black-600">
      {ticket.id}
    </span>
    {#if ticket.stale}
      <span class="rounded bg-neg-100 px-1.5 py-0.5 text-[10px] font-medium text-neg-400">stale</span>
    {/if}
  </div>

  <p class="mt-1.5 line-clamp-2 text-sm font-medium text-black-900 dark:text-white-100">
    {ticket.title || "Untitled ticket"}
  </p>

  {#if fieldEntries.length > 0}
    <div class="mt-2 flex flex-wrap gap-1">
      {#each fieldEntries as f (f.label)}
        <span class="rounded bg-white-200 px-1.5 py-0.5 text-[10px] text-black-800 dark:bg-navy-800 dark:text-black-600">
          {f.label}: {f.value}
        </span>
      {/each}
    </div>
  {/if}

  <div class="mt-2 flex items-center gap-2 text-[11px] text-black-700 dark:text-black-600">
    {#if assigneeName}
      <span class="inline-flex min-w-0 items-center gap-1" title={assigneeName}>
        <span class="flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-green-500 text-[9px] font-semibold text-white-100">
          {initial(assigneeName)}
        </span>
        <span class="max-w-[88px] truncate">{assigneeName}</span>
      </span>
    {:else}
      <span>unassigned</span>
    {/if}
    <span class="ml-auto shrink-0">{timeAgo(ticket.updated_at)}</span>
  </div>

  <!-- The ticket's sessions, listed rather than counted: a row is the drag
       handle for moving that conversation to another ticket, which a number
       could never be. -->
  {#if sessionRows.length > 0}
    <ul class="mt-2 flex flex-col gap-1 border-t border-white-300 pt-2 dark:border-navy-600">
      {#each sessionRows as s (s.id)}
        <li>
          <div
            role="button"
            tabindex="0"
            draggable="true"
            data-testid={"session-row-" + s.id}
            title="Drag onto another ticket to move this chat"
            ondragstart={(e) => { e.stopPropagation(); onSessionDragStart(e, s.id); }}
            onclick={(e) => { e.stopPropagation(); onOpenSession(s.id); }}
            onkeydown={(e) => { if (e.key === "Enter") { e.stopPropagation(); onOpenSession(s.id); } }}
            class="flex cursor-grab items-center gap-1.5 rounded bg-white-200 px-1.5 py-1 text-[11px] transition-colors hover:bg-white-300 active:cursor-grabbing dark:bg-navy-800 dark:hover:bg-navy-600"
          >
            <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0 text-black-600 dark:text-black-700" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
              <path d="M6 4h.01M6 8h.01M6 12h.01M10 4h.01M10 8h.01M10 12h.01" stroke-linecap="round"></path>
            </svg>
            <span class="min-w-0 flex-1 truncate text-black-800 dark:text-black-600">{s.label || s.id}</span>
            {#if s.lifecycle === "working"}
              <span class="shrink-0 rounded bg-pos-100 px-1 text-[9px] font-medium text-pos-400">live</span>
            {/if}
          </div>
        </li>
      {/each}
      <!-- The card carries a page of rows, not all of them: a ticket with
           twenty sessions must not stretch the column. The rest are on the
           ticket's own page. -->
      {#if ticket.sessions > sessionRows.length}
        <li>
          <button
            type="button"
            onclick={(e) => { e.stopPropagation(); onOpen(ticket.id); }}
            class="w-full rounded px-1.5 py-0.5 text-left text-[10px] text-black-700 transition-colors hover:text-green-600 dark:text-black-600 dark:hover:text-green-400"
          >+{ticket.sessions - sessionRows.length} more session{ticket.sessions - sessionRows.length === 1 ? "" : "s"}</button>
        </li>
      {/if}
    </ul>
  {/if}

  {#if ticket.notes > 0 || ticket.open_tasks > 0}
    <div class="mt-2 flex items-center gap-3 text-[11px] text-black-700 dark:text-black-600">
      {#if ticket.notes > 0}
        <span class="inline-flex items-center gap-1">
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <path d="M4 2.5h8v11H4z" stroke-linejoin="round"></path>
            <path d="M6 5.5h4M6 8h4M6 10.5h2.5" stroke-linecap="round"></path>
          </svg>
          {ticket.notes} note{ticket.notes === 1 ? "" : "s"}
        </span>
      {/if}
      {#if ticket.open_tasks > 0}
        <span class="inline-flex items-center gap-1 text-cau-400">
          <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
            <rect x="2.5" y="2.5" width="11" height="11" rx="2"></rect>
            <path d="M5.5 8l2 2 3.5-4" stroke-linecap="round" stroke-linejoin="round"></path>
          </svg>
          {ticket.open_tasks} open
        </span>
      {/if}
    </div>
  {/if}
</div>
