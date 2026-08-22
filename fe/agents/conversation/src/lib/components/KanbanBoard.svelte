<script lang="ts">
  /* The ticket board: untracked chats on the left, status columns on the
     right, and two kinds of drag.

     Two drag types share one board, so the payload says which is which
     ("ticket:T-4F2A" vs "session:abc"). A COLUMN only accepts a ticket
     (that is a status change); a CARD only accepts a session (attach, or
     move it off whichever ticket it was on). Without the distinction a
     card would light up while a ticket is being dragged past it, and a
     column would swallow a session drop that meant nothing there. */
  import type {
    TicketBoard,
    TicketCard as TicketCardType,
    TicketFilter,
    TicketSessionRow,
  } from "../types/agents.js";
  import { toastError } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { attachSession, createTicket, updateTicket, type EmptiedTicket } from "../api/tickets.js";
  import TicketCard from "./TicketCard.svelte";
  import { timeAgo } from "../timeFormat.js";

  type Props = {
    base: string;
    projectId: string;
    board: TicketBoard;
    filter: TicketFilter;
    onFilter: (f: TicketFilter) => void;
    /* Opens the ticket's detail view (its sessions + notes). */
    onOpen: (ticketId: string) => void;
    onOpenSession: (sessionId: string) => void;
    /* Re-fetch after a change the board cannot apply locally. */
    onReload: () => void;
    /* Collapse state for the untracked rail. Lifted, because it decides
       whether the server sends that list at all — a project with hundreds
       of loose chats should not pay for a rail nobody has open. */
    showUntracked?: boolean;
    onToggleUntracked?: (show: boolean) => void;
    /* A ticket that just lost its last chat. The parent decides what to
       offer, since the answer ("don't ask again") is a user preference and
       not this component's business. */
    onEmptied?: (t: EmptiedTicket) => void;
  };

  let {
    base,
    projectId,
    board,
    filter,
    onFilter,
    onOpen,
    onOpenSession,
    onReload,
    showUntracked = true,
    onToggleUntracked,
    onEmptied,
  }: Props = $props();

  /* Local copy so a drag can move a card optimistically; resynced whenever
     the parent hands us a fresh board. */
  let items = $state<TicketCardType[]>([]);
  let untracked = $state<TicketSessionRow[]>([]);
  $effect(() => {
    items = board.tickets.map((t) => ({ ...t }));
    untracked = [...board.untracked];
  });

  /* The server sends one page and the true total separately, so the rail can
     say "25/142" rather than implying it holds them all. */
  const untrackedTotal = $derived(board.untracked_total ?? untracked.length);

  const statusLabels: Record<string, string> = {
    open: "Open",
    in_progress: "In Progress",
    waiting: "Waiting",
    done: "Done",
  };

  /* Column accents come from the status ramps — green stays the accent. */
  const columnPill: Record<string, string> = {
    open: "bg-prog-100 text-prog-400",
    in_progress: "bg-cau-100 text-cau-400",
    waiting: "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600",
    done: "bg-pos-100 text-pos-400",
  };

  /* Static class map so Tailwind's scanner sees every variant. */
  const lgCols: Record<number, string> = {
    1: "lg:grid-cols-1",
    2: "lg:grid-cols-2",
    3: "lg:grid-cols-3",
    4: "lg:grid-cols-4",
  };

  const activeStatuses = $derived(
    filter.statuses && filter.statuses.length > 0 ? filter.statuses : board.statuses,
  );

  function matchesAssignee(t: TicketCardType): boolean {
    const a = filter.assignee ?? "";
    if (a === "") return true;
    const target = a === "me" ? (board.me ?? "") : a;
    if (target === "") return true;
    return t.assignee === target;
  }

  const visible = $derived(items.filter(matchesAssignee));
  const columns = $derived(
    board.statuses
      .filter((s) => activeStatuses.includes(s))
      .map((s) => ({
        status: s,
        tickets: visible
          .filter((t) => t.status === s)
          .sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
      })),
  );

  const assigneeOptions = $derived.by(() => {
    const ids = new Set<string>();
    for (const t of items) if (t.assignee) ids.add(t.assignee);
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

  /* ── new ticket ── */
  let creating = $state(false);
  let newTitle = $state("");
  let saving = $state(false);
  /* Set when "+ New ticket" was reached from an untracked session, so the
     chat is attached to the ticket it just became. */
  let creatingFromSession = $state<string | null>(null);

  function startNew(fromSession?: string) {
    creating = true;
    creatingFromSession = fromSession ?? null;
    newTitle = fromSession
      ? (untracked.find((s) => s.id === fromSession)?.label ?? "")
      : "";
  }

  function submitNew() {
    const title = newTitle.trim();
    if (title === "") return;
    saving = true;
    Effect.runPromise(
      createTicket(base, projectId, {
        title,
        session_id: creatingFromSession ?? undefined,
      }).pipe(Effect.provide(WickClientLayer)),
    )
      .then(() => {
        newTitle = "";
        creating = false;
        creatingFromSession = null;
        onReload();
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to create ticket"))
      .finally(() => {
        saving = false;
      });
  }

  /* ── drag & drop ── */
  type Drag = { kind: "ticket" | "session"; id: string };
  let drag = $state<Drag | null>(null);
  let dropColumn = $state<string | null>(null);

  const sessionDragging = $derived(drag?.kind === "session");

  function startTicketDrag(e: DragEvent, ticketId: string) {
    drag = { kind: "ticket", id: ticketId };
    e.dataTransfer?.setData("text/plain", "ticket:" + ticketId);
    if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
  }

  function startSessionDrag(e: DragEvent, sessionId: string) {
    drag = { kind: "session", id: sessionId };
    e.dataTransfer?.setData("text/plain", "session:" + sessionId);
    if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
  }

  function dropOnColumn(status: string) {
    const d = drag;
    drag = null;
    dropColumn = null;
    if (!d) return;

    /* A SESSION dropped on a column becomes its own ticket, in that
       column's status. Dropping a chat onto "In Progress" plainly means
       "this is work, and it has started" — refusing it because no ticket
       exists yet would make the user do the same thing in two steps. */
    if (d.kind === "session") {
      const row =
        untracked.find((s) => s.id === d.id) ??
        items.flatMap((t) => t.session_rows ?? []).find((s) => s.id === d.id);
      const title = (row?.label ?? "").trim() || "New ticket";
      Effect.runPromise(
        createTicket(base, projectId, {
          title,
          status,
          session_id: d.id,
          // Dragging a chat into a column is someone saying "I am taking
          // this on", so it lands assigned to them rather than as an
          // unassigned card they then have to claim.
          assignee: board.me,
        }).pipe(Effect.provide(WickClientLayer)),
      )
        .then(onReload)
        .catch((e: unknown) =>
          toastError(e instanceof Error ? e.message : "Failed to create the ticket"),
        );
      return;
    }

    const t = items.find((x) => x.id === d.id);
    if (!t || t.status === status) return;
    const prev = t.status;
    t.status = status; // optimistic — the card jumps immediately
    Effect.runPromise(updateTicket(base, d.id, { status }).pipe(Effect.provide(WickClientLayer)))
      .then((updated) => {
        if (updated?.updated_at) t.updated_at = updated.updated_at;
        t.stale = false;
      })
      .catch((err) => {
        t.status = prev;
        toastError(err instanceof Error ? err.message : "Failed to move ticket");
      });
  }

  /* A session dropped on a card: attach it, or move it off whatever ticket
     it was on. The server does the detach, and the session's notes travel
     with it — so the board just re-reads afterwards. */
  function dropSessionOnTicket(ticketId: string) {
    const d = drag;
    drag = null;
    if (!d || d.kind !== "session") return;
    const alreadyHere = items.some(
      (t) => t.id === ticketId && (t.session_rows ?? []).some((s) => s.id === d.id),
    );
    if (alreadyHere) return;
    Effect.runPromise(
      attachSession(base, ticketId, d.id).pipe(Effect.provide(WickClientLayer)),
    )
      .then((r) => {
        // Moving the last chat off a ticket leaves nothing to track, so the
        // husk is offered for removal instead of being left on the board.
        if (r.emptied_ticket) onEmptied?.(r.emptied_ticket);
        onReload();
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to move the chat"));
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
            ? "border-green-500 bg-green-50 text-green-600 dark:bg-green-900/20 dark:text-green-400"
            : "border-white-400 bg-white-100 text-black-700 hover:bg-white-200 dark:border-navy-600 dark:bg-navy-700 dark:text-black-600 dark:hover:bg-navy-600",
        ].join(" ")}
      >
        {statusLabels[s] ?? s}
      </button>
    {/each}

    <select
      value={filter.assignee ?? ""}
      onchange={(e) => onFilter({ ...filter, assignee: (e.target as HTMLSelectElement).value })}
      class="ml-auto rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-xs text-black-800 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
    >
      <option value="">Assignee: All</option>
      {#if board.me}<option value="me">Assignee: Me</option>{/if}
      {#each assigneeOptions as u (u.id)}
        <option value={u.id}>Assignee: {u.name}</option>
      {/each}
    </select>

    {#if creating}
      <form
        onsubmit={(e) => { e.preventDefault(); submitNew(); }}
        class="flex w-full items-center gap-2 sm:w-auto"
      >
        <input
          bind:value={newTitle}
          placeholder="What needs doing?"
          aria-label="New ticket title"
          class="min-w-0 flex-1 rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-xs text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
        />
        <button
          type="submit"
          disabled={saving || newTitle.trim() === ""}
          class="rounded-lg bg-green-600 px-3 py-1.5 text-xs font-semibold text-white-100 transition-colors hover:bg-green-700 disabled:opacity-40"
        >{saving ? "Adding…" : creatingFromSession ? "Create & attach" : "Add"}</button>
        <button
          type="button"
          onclick={() => { creating = false; newTitle = ""; creatingFromSession = null; }}
          class="rounded-lg px-2 py-1.5 text-xs text-black-700 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-700"
        >Cancel</button>
      </form>
    {:else}
      <button
        type="button"
        onclick={() => startNew()}
        class="rounded-lg border border-green-500 px-3 py-1.5 text-xs font-medium text-green-600 transition-colors hover:bg-green-50 dark:text-green-400 dark:hover:bg-green-900/20"
      >+ New ticket</button>
    {/if}
  </div>

  <div class="flex flex-col gap-3 lg:flex-row">
    <!-- Untracked chats. Left rail rather than a column, because they have
         no status to be in — they are the pool you drag FROM.

         Collapsible, and the collapse reaches the server: with the rail
         shut the board stops asking for the list at all, so a project with
         hundreds of loose chats costs nothing to poll. -->
    <aside class={"shrink-0 " + (showUntracked ? "lg:w-56" : "")}>
      <div class="flex h-full flex-col gap-2 rounded-xl bg-white-200 p-2 dark:bg-navy-800">
        <button
          type="button"
          aria-expanded={showUntracked}
          onclick={() => onToggleUntracked?.(!showUntracked)}
          class="flex items-center gap-1.5 rounded px-2 py-1 text-left transition-colors hover:bg-white-300 dark:hover:bg-navy-600"
        >
          <svg
            viewBox="0 0 16 16"
            class={"h-3 w-3 shrink-0 text-black-700 transition-transform dark:text-black-600 " + (showUntracked ? "rotate-90" : "")}
            fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"
          >
            <path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path>
          </svg>
          {#if showUntracked}
            <span class="text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">
              Untracked
            </span>
            <span class="ml-auto rounded-full bg-white-100 px-2 py-0.5 text-[10px] font-semibold text-black-800 dark:bg-navy-700 dark:text-black-600">
              {untrackedTotal > untracked.length ? `${untracked.length}/${untrackedTotal}` : untracked.length}
            </span>
          {:else}
            <span class="text-[10px] font-semibold uppercase tracking-wide text-black-700 [writing-mode:vertical-rl] dark:text-black-600">
              Untracked
            </span>
          {/if}
        </button>

        {#if !showUntracked}
          <!-- Collapsed: nothing is fetched, so nothing is claimed. -->
        {:else if untracked.length === 0}
          <p class="px-2 py-4 text-center text-[11px] text-black-600 dark:text-black-700">
            Every chat here belongs to a ticket.
          </p>
        {:else}
          <p class="px-2 pb-1 text-[10px] leading-relaxed text-black-600 dark:text-black-700">
            Drag onto a ticket to attach, onto a column to make it a ticket.
          </p>
          <ul class="flex flex-col gap-1.5">
            {#each untracked as s (s.id)}
              <li>
                <div
                  role="button"
                  tabindex="0"
                  draggable="true"
                  data-testid={"untracked-" + s.id}
                  title="Drag onto a ticket to attach this chat"
                  ondragstart={(e) => startSessionDrag(e, s.id)}
                  onclick={() => onOpenSession(s.id)}
                  onkeydown={(e) => e.key === "Enter" && onOpenSession(s.id)}
                  class="cursor-grab rounded-lg border border-white-300 bg-white-100 p-2 transition-colors hover:border-green-500 active:cursor-grabbing dark:border-navy-600 dark:bg-navy-700"
                >
                  <p class="line-clamp-2 text-[11px] font-medium text-black-900 dark:text-white-100">
                    {s.label || s.id}
                  </p>
                  <div class="mt-1 flex items-center gap-1.5 text-[10px] text-black-700 dark:text-black-600">
                    {#if s.lifecycle === "working"}
                      <span class="rounded bg-pos-100 px-1 font-medium text-pos-400">live</span>
                    {/if}
                    {#if s.last_active}<span>{timeAgo(s.last_active)}</span>{/if}
                    <button
                      type="button"
                      title="Create a ticket from this chat"
                      onclick={(e) => { e.stopPropagation(); startNew(s.id); }}
                      class="ml-auto rounded px-1 text-[10px] font-medium text-green-600 transition-colors hover:bg-green-50 dark:text-green-400 dark:hover:bg-green-900/20"
                    >+ ticket</button>
                  </div>
                </div>
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    </aside>

    <!-- Columns -->
    <div class={"grid min-w-0 flex-1 grid-cols-1 gap-3 sm:grid-cols-2 " + (lgCols[columns.length] ?? "lg:grid-cols-4")}>
      {#each columns as col (col.status)}
        <div
          role="list"
          aria-label={statusLabels[col.status] ?? col.status}
          ondragover={(e) => {
            // A ticket lands here to change status; a session lands here to
            // BECOME a ticket at that status. A card intercepts the session
            // case first when the pointer is actually over one.
            e.preventDefault();
            dropColumn = col.status;
          }}
          ondragleave={() => { if (dropColumn === col.status) dropColumn = null; }}
          ondrop={(e) => { e.preventDefault(); dropOnColumn(col.status); }}
          class={[
            "flex min-h-[200px] flex-col gap-2 rounded-xl bg-white-200 p-2 transition-colors dark:bg-navy-800",
            dropColumn === col.status ? "ring-2 ring-inset ring-green-500" : "",
          ].join(" ")}
        >
          <div class="flex items-center justify-between px-2 pb-2 pt-1">
            <span class="text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">
              {statusLabels[col.status] ?? col.status}
            </span>
            <span class={"rounded-full px-2 py-0.5 text-[10px] font-semibold " + (columnPill[col.status] ?? "bg-white-300 text-black-800")}>
              {col.tickets.length}
            </span>
          </div>
          {#each col.tickets as t (t.id)}
            <div class={t.status === "done" ? "opacity-70" : ""}>
              <TicketCard
                ticket={t}
                schema={board.config.fields}
                users={board.users}
                {onOpen}
                {onOpenSession}
                {sessionDragging}
                onDragStart={startTicketDrag}
                onSessionDragStart={startSessionDrag}
                onSessionDrop={dropSessionOnTicket}
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
</div>
