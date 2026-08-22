<script lang="ts">
  import type { TicketItem, TicketField } from "../types/agents.js";
  import { timeAgo } from "../timeFormat.js";

  type Props = {
    ticket: TicketItem;
    /* Project schema, to render field labels instead of raw keys. */
    schema?: TicketField[];
    /* user id → display name (owners + assignees on this board). */
    users?: Record<string, string>;
    onSelect: (sessionId: string) => void;
    onDragStart: (e: DragEvent, sessionId: string) => void;
  };

  let { ticket, schema, users, onSelect, onDragStart }: Props = $props();

  const shortId = $derived(ticket.session_id.slice(0, 8));

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

  function initial(name: string): string {
    return name.trim().charAt(0).toUpperCase() || "?";
  }
</script>

<div
  role="button"
  tabindex="0"
  draggable="true"
  data-testid={"ticket-card-" + ticket.session_id}
  ondragstart={(e) => onDragStart(e, ticket.session_id)}
  onclick={() => onSelect(ticket.session_id)}
  onkeydown={(e) => e.key === "Enter" && onSelect(ticket.session_id)}
  class="group cursor-pointer rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-3 shadow-sm hover:shadow-md hover:border-green-500 dark:hover:border-green-500 transition-all active:cursor-grabbing"
>
  <p class="text-sm font-medium text-black-900 dark:text-white-100 line-clamp-2">
    {ticket.title || "Untitled session"}
  </p>

  {#if fieldEntries.length > 0}
    <div class="mt-2 flex flex-wrap gap-1">
      {#each fieldEntries as f (f.label)}
        <span class="rounded bg-white-200 dark:bg-navy-800 px-1.5 py-0.5 text-[10px] text-black-800 dark:text-black-600">
          {f.label}: {f.value}
        </span>
      {/each}
    </div>
  {/if}

  <div class="mt-2 flex items-center gap-2 text-[11px] text-black-700 dark:text-black-600">
    {#if assigneeName}
      <span class="inline-flex items-center gap-1 min-w-0" title={assigneeName}>
        <span class="flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-green-500 text-[9px] font-semibold text-white-100">
          {initial(assigneeName)}
        </span>
        <span class="truncate max-w-[96px]">{assigneeName}</span>
      </span>
    {:else}
      <span class="text-black-600 dark:text-black-700">unassigned</span>
    {/if}
    <span class="font-mono">#{shortId}</span>
    <span class="ml-auto shrink-0">{timeAgo(ticket.updated_at || ticket.last_active)}</span>
  </div>

  {#if ticket.stale || ticket.lifecycle === "working"}
    <div class="mt-2 flex items-center gap-2">
      {#if ticket.stale}
        <span class="rounded bg-neg-100 px-1.5 py-0.5 text-[10px] font-medium text-neg-400">stale</span>
      {/if}
      {#if ticket.lifecycle === "working"}
        <span class="rounded bg-pos-100 px-1.5 py-0.5 text-[10px] font-medium text-pos-400">working</span>
      {/if}
    </div>
  {/if}
</div>
