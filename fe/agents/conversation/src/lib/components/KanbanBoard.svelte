<script lang="ts">
  import type { TicketBoard, TicketFilter, TicketItem } from "../types/agents.js";
  import { toastError } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { updateSessionTicket } from "../api/tickets.js";
  import TicketCard from "./TicketCard.svelte";

  type Props = {
    base: string;
    board: TicketBoard;
    filter: TicketFilter;
    onFilter: (f: TicketFilter) => void;
    onSelect: (sessionId: string) => void;
  };

  let { base, board, filter, onFilter, onSelect }: Props = $props();

  /* Local copy so a drag can move a card optimistically; resynced whenever
     the parent hands us a fresh board. */
  let items = $state<TicketItem[]>([]);
  $effect(() => {
    items = board.tickets.map((t) => ({ ...t }));
  });

  const statusLabels: Record<string, string> = {
    open: "Open",
    in_progress: "In Progress",
    waiting: "Waiting",
    done: "Done",
  };

  /* Column accents: status ramps only (green stays the accent color). */
  const columnPill: Record<string, string> = {
    open: "bg-prog-100 text-prog-400",
    in_progress: "bg-cau-100 text-cau-400",
    waiting: "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600",
    done: "bg-pos-100 text-pos-400",
  };

  const activeStatuses = $derived(
    filter.statuses && filter.statuses.length > 0 ? filter.statuses : board.statuses,
  );

  function matchesAssignee(t: TicketItem): boolean {
    const a = filter.assignee ?? "";
    if (a === "") return true;
    const target = a === "me" ? (board.me ?? "") : a;
    if (target === "") return true;
    // "Mine" = assigned to me, or unassigned but owned by me.
    return t.assignee === target || (!t.assignee && t.owner_id === target);
  }

  const visible = $derived(items.filter(matchesAssignee));
  const columns = $derived(
    board.statuses
      .filter((s) => activeStatuses.includes(s))
      .map((s) => ({
        status: s,
        tickets: visible
          .filter((t) => t.status === s)
          .sort((a, b) => (b.updated_at ?? b.last_active).localeCompare(a.updated_at ?? a.last_active)),
      })),
  );

  /* Assignee choices: everyone appearing on the board, by name. */
  const assigneeOptions = $derived.by(() => {
    const ids = new Set<string>();
    for (const t of items) {
      if (t.assignee) ids.add(t.assignee);
      if (t.owner_id) ids.add(t.owner_id);
    }
    ids.delete(board.me ?? "");
    return [...ids].map((id) => ({ id, name: board.users?.[id] ?? id }));
  });

  function toggleStatus(s: string) {
    const cur = new Set(activeStatuses);
    if (cur.has(s)) {
      if (cur.size === 1) return; // never filter the board down to nothing
      cur.delete(s);
    } else {
      cur.add(s);
    }
    const all = board.statuses.every((x) => cur.has(x));
    onFilter({ ...filter, statuses: all ? [] : board.statuses.filter((x) => cur.has(x)) });
  }

  /* Static class map so Tailwind's scanner sees every variant. */
  const lgCols: Record<number, string> = {
    1: "lg:grid-cols-1",
    2: "lg:grid-cols-2",
    3: "lg:grid-cols-3",
    4: "lg:grid-cols-4",
  };

  /* ── drag & drop ── */
  let dragId = $state<string | null>(null);
  let dropTarget = $state<string | null>(null);

  function handleDragStart(e: DragEvent, sessionId: string) {
    dragId = sessionId;
    e.dataTransfer?.setData("text/plain", sessionId);
    if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
  }

  function handleDrop(status: string) {
    const id = dragId;
    dragId = null;
    dropTarget = null;
    if (!id) return;
    const t = items.find((x) => x.session_id === id);
    if (!t || t.status === status) return;
    const prev = t.status;
    t.status = status; // optimistic — the card jumps immediately
    Effect.runPromise(
      updateSessionTicket(base, id, { status }).pipe(Effect.provide(WickClientLayer)),
    )
      .then((r) => {
        if (r.ticket?.updated_at) t.updated_at = r.ticket.updated_at;
        t.stale = false;
      })
      .catch((err) => {
        t.status = prev;
        toastError(err instanceof Error ? err.message : "Failed to move ticket");
      });
  }
</script>

<div class="flex flex-col gap-4">
  <!-- Filter bar -->
  <div class="flex flex-wrap items-center gap-2">
    {#each board.statuses as s (s)}
      {@const on = activeStatuses.includes(s)}
      <button
        type="button"
        aria-pressed={on}
        onclick={() => toggleStatus(s)}
        class={[
          "rounded-full border px-3 py-1 text-xs font-medium transition-colors",
          on
            ? "border-green-500 bg-green-50 dark:bg-green-900/20 text-green-600 dark:text-green-400"
            : "border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600",
        ].join(" ")}
      >
        {statusLabels[s] ?? s}
      </button>
    {/each}

    <select
      value={filter.assignee ?? ""}
      onchange={(e) => onFilter({ ...filter, assignee: (e.target as HTMLSelectElement).value })}
      class="ml-auto rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-xs text-black-800 dark:text-white-100 focus:border-green-500 focus:outline-none"
    >
      <option value="">Assignee: All</option>
      {#if board.me}<option value="me">Assignee: Me</option>{/if}
      {#each assigneeOptions as u (u.id)}
        <option value={u.id}>Assignee: {u.name}</option>
      {/each}
    </select>
  </div>

  <!-- Columns -->
  <div class={"grid grid-cols-1 sm:grid-cols-2 gap-3 " + (lgCols[columns.length] ?? "lg:grid-cols-4")}>
    {#each columns as col (col.status)}
      <div
        role="list"
        aria-label={statusLabels[col.status] ?? col.status}
        ondragover={(e) => { e.preventDefault(); dropTarget = col.status; }}
        ondragleave={() => { if (dropTarget === col.status) dropTarget = null; }}
        ondrop={(e) => { e.preventDefault(); handleDrop(col.status); }}
        class={[
          "flex min-h-[200px] flex-col gap-2 rounded-xl bg-white-200 dark:bg-navy-800 p-2 transition-colors",
          dropTarget === col.status ? "ring-2 ring-green-500 ring-inset" : "",
        ].join(" ")}
      >
        <div class="flex items-center justify-between px-2 pt-1 pb-2">
          <span class="text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">
            {statusLabels[col.status] ?? col.status}
          </span>
          <span class={"rounded-full px-2 py-0.5 text-[10px] font-semibold " + (columnPill[col.status] ?? "bg-white-300 text-black-800")}>
            {col.tickets.length}
          </span>
        </div>
        {#each col.tickets as t (t.session_id)}
          <div class={t.status === "done" ? "opacity-70" : ""}>
            <TicketCard
              ticket={t}
              schema={board.config.fields}
              users={board.users}
              {onSelect}
              onDragStart={handleDragStart}
            />
          </div>
        {/each}
        {#if col.tickets.length === 0}
          <p class="px-2 py-6 text-center text-xs text-black-600 dark:text-black-700">No tickets</p>
        {/if}
      </div>
    {/each}
  </div>
</div>
