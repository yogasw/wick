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
    TicketButton,
    TicketCard as TicketCardType,
    TicketFilter,
    TicketSessionRow,
  } from "../types/agents.js";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import {
    attachSession,
    createTicket,
    runBoardAction,
    updateTicket,
    type ActionResult,
    type EmptiedTicket,
  } from "../api/tickets.js";
  import TicketCard from "./TicketCard.svelte";
  import BoardActionResult from "./BoardActionResult.svelte";
  import OwnerTabs from "./OwnerTabs.svelte";
  import ScrollSentinel from "./ScrollSentinel.svelte";
  import { timeAgo } from "../timeFormat.js";
  import { sessionRowBadge } from "../lifecycleCls.js";
  import { now } from "../stores/now.js";
  import {
    buttonCaption,
    cardKey,
    groupBoardButtons,
    hasStatus,
    mergeBoards,
    statusesFor,
    type BoardButtonGroup,
    type BoardProject,
  } from "../ticketBoardMerge.js";
  import { roomAgentOptions, withRoomAgent } from "../ticketRoom.js";
  import { activeStatusKeys, terminalKeys, toggledStatuses } from "../ticketBoardStatuses.js";

  type Props = {
    base: string;
    /* Single-project mode: one board and the project it belongs to. */
    projectId?: string;
    board?: TicketBoard;
    /* Multi-project mode (Team › Tickets): several projects' boards drawn
       as one. When set, projectId and board are not read; every card, chat
       and button acts on its own project, and the callbacks below name it. */
    projects?: BoardProject[];
    filter: TicketFilter;
    /* The untracked rail's scope — parent-owned, per-visit state (never
       saved): every board open starts back at "me". */
    untrackedOwner: "me" | "all";
    onUntrackedOwner: (v: "me" | "all") => void;
    /* Scrolling the rail to its end asks the parent for the next page of
       untracked rows (it owns the request's limit). */
    onUntrackedMore: (projectId?: string) => void;
    onFilter: (f: TicketFilter) => void;
    /* Opens the ticket's detail view (its sessions + notes). */
    onOpen: (ticketId: string, projectId?: string) => void;
    onOpenSession: (sessionId: string, projectId?: string) => void;
    /* Re-fetch after a change the board cannot apply locally (multi mode:
       the project it happened in). */
    onReload: (projectId?: string) => void;
    /* A ticket that just lost its last chat. The parent decides what to
       offer, since the answer ("don't ask again") is a user preference and
       not this component's business. */
    onEmptied?: (t: EmptiedTicket, projectId?: string) => void;
    /* A rail chat this board just put on a ticket, so the parent can drop
       it from pages it loaded earlier — a re-read only refreshes page one. */
    onSessionTracked?: (sessionId: string) => void;
  };

  let {
    base,
    projectId,
    board,
    projects,
    filter,
    untrackedOwner,
    onUntrackedOwner,
    onUntrackedMore,
    onFilter,
    onOpen,
    onOpenSession,
    onReload,
    onEmptied,
    onSessionTracked,
  }: Props = $props();

  /* ── multi-project mode ──
     The project select narrows what is drawn and what a board button
     fires on. Per visit and never saved: the saved filter is the server's
     TicketFilter, which has no project in it. "" = every project. */
  const multi = $derived(projects !== undefined);
  let projectFilter = $state("");
  const shownProjects = $derived.by(() => {
    if (!projects) return [];
    const one = projects.filter((p) => p.id === projectFilter);
    return one.length > 0 ? one : projects;
  });
  // A picked project that has left the list (deselected, failed reload)
  // would leave the select on a value it has no option for.
  $effect(() => {
    if (projectFilter !== "" && !projects?.some((p) => p.id === projectFilter)) projectFilter = "";
  });
  const merged = $derived(projects ? mergeBoards(shownProjects) : null);
  /* What the toolbar and the columns read in either mode. */
  const boardView = $derived<TicketBoard>(merged ? merged.board : board!);
  /* "Agent in room" (multi mode): only the tickets whose room has that
     member. Per visit, like the project select. "" = any. */
  let roomAgent = $state("");
  const roomAgents = $derived(multi ? roomAgentOptions(boardView.tickets) : []);
  $effect(() => {
    if (roomAgent !== "" && !roomAgents.some((o) => o.key === roomAgent)) roomAgent = "";
  });
  const view = $derived<TicketBoard>(roomAgent ? { ...boardView, tickets: withRoomAgent(boardView.tickets, roomAgent) } : boardView);
  const projectById = (id: string | undefined) => projects?.find((p) => p.id === id);
  /* A card's project rides on the card (multi mode): ids repeat across
     projects, so nothing is looked up by a bare ticket id. */
  const projectOfTicket = (t: { project_id?: string } | undefined) => t?.project_id;
  const projectOfSession = (id: string) => merged?.sessionProject.get(id);
  // A card names its project only once two of them share the board.
  const showProjectTag = $derived((projects?.length ?? 0) >= 2);

  /* Single mode keeps calling back with the one argument it always had. */
  function openSession(id: string) {
    if (multi) onOpenSession(id, projectOfSession(id));
    else onOpenSession(id);
  }
  function reload(pid: string | undefined) {
    if (multi) onReload(pid);
    else onReload();
  }

  /* The untracked rail is part of the filter, not a piece of chrome with its
     own collapse: with it off the server never builds that list. Off by
     default — the board is about tickets, and a project's loose chats are
     something one asks to see. */
  const showUntracked = $derived(filter.show_untracked === true);

  /* Local copy so a drag can move a card optimistically; resynced whenever
     the parent hands us a fresh board. */
  let items = $state<TicketCardType[]>([]);
  let untracked = $state<TicketSessionRow[]>([]);
  $effect(() => {
    items = view.tickets.map((t) => ({ ...t }));
    untracked = [...view.untracked];
  });

  /* The server sends one page and the true total separately, so the rail can
     say "25/142" rather than implying it holds them all. The rail's own
     scroll loads the rest a page at a time, up to a thousand rows — past
     that the chat list's search is the better tool. Whether more exist is
     the server's cursor, not a count: a drawn row that has since left the
     set would otherwise make the rail stop one page early. */
  const UNTRACKED_RAIL_MAX = 1000;
  const untrackedTotal = $derived(view.untracked_total ?? untracked.length);
  const untrackedHasMore = $derived(
    !!view.untracked_next && untracked.length < UNTRACKED_RAIL_MAX,
  );

  /* Columns come from the project — a team names its own stages — so keys
     and labels both arrive on the board payload. */
  const statusKeys = $derived(view.statuses.map((s) => s.key));
  const labelOf = (key: string) =>
    view.statuses.find((s) => s.key === key)?.label || key;
  const terminalKey = $derived(
    view.statuses.find((s) => s.terminal)?.key ??
      view.statuses[view.statuses.length - 1]?.key ??
      "",
  );

  /* Column accents: a fixed ramp walked in board order, with the terminal
     stage always reading as "finished". Keying off names would leave a
     renamed board grey. The header wears it as a STATUS DOT (the Jira
     idiom) rather than tinting the count — the count is data, the colour
     is identity. */
  const dotAccents = ["bg-prog-400", "bg-cau-400", "bg-black-600", "bg-link-400"];
  function dotFor(key: string, index: number): string {
    if (key === terminalKey) return "bg-pos-400";
    return dotAccents[index % dotAccents.length];
  }

  /* An empty saved list means the default columns — every one but the
     terminal stage (ticketBoardStatuses.ts says why) — and keeps meaning that
     across a project that later renames or adds a stage. Turning every chip
     off is spelled with the sentinel below instead, so the two cannot be
     confused. */
  const NONE = " none";
  const terminalStatusKeys = $derived(terminalKeys(view.statuses));
  const activeStatuses = $derived(activeStatusKeys(filter.statuses, statusKeys, terminalStatusKeys, NONE));

  /* Search over the board that is already on screen.

     Deliberately CLIENT-side and deliberately not part of the saved filter:
     a search is a moment ("where is the one about the voucher"), not a view
     you want to come back to tomorrow and wonder why the board looks empty.
     It reads the cards that arrived — which is also why what it matches is
     stated on screen, so an empty column reads as "nothing matches" rather
     than "nothing there". */
  let query = $state("");
  const q = $derived(query.trim().toLowerCase());
  function namesOf(t: TicketCardType): string {
    const ids = t.assignees?.length ? t.assignees : t.assignee ? [t.assignee] : [];
    return ids.map((id) => view.users?.[id] ?? id).join(" ");
  }
  function matches(t: TicketCardType): boolean {
    if (q === "") return true;
    return (
      t.id.toLowerCase().includes(q) ||
      (t.title ?? "").toLowerCase().includes(q) ||
      namesOf(t).toLowerCase().includes(q)
    );
  }

  /* The server already filtered by status and assignee — this only splits
     what arrived into its columns (and applies the local search). Filtering
     by status again here would hide nothing and would quietly mask a request
     that asked for the wrong thing. */
  /* A search also reaches the collapsed terminal column: "where is the one
     about the voucher" should not miss it because it is finished. Only a
     column with a match opens, and only for the search — board buttons
     still send activeStatuses. */
  const columnKeys = $derived(
    q === ""
      ? activeStatuses
      : statusKeys.filter(
          (k) => activeStatuses.includes(k) || (terminalStatusKeys.includes(k) && items.some((t) => t.status === k && matches(t))),
        ),
  );
  const columns = $derived(
    columnKeys
      .filter((s) => statusKeys.includes(s))
      .map((s) => ({
        status: s,
        tickets: items
          .filter((t) => t.status === s && matches(t))
          .sort((a, b) => b.updated_at.localeCompare(a.updated_at)),
      })),
  );
  // Counted across the columns on screen, so the chip's number and the column
  // headers can never disagree.
  const matchCount = $derived(columns.reduce((n, c) => n + c.tickets.length, 0));

  /* Everyone the project's tickets name, from the payload's user map rather
     than the cards: with an assignee filter applied only one person's cards
     arrive, and a dropdown that forgot the others could never be reset from
     within itself. */
  const assigneeOptions = $derived.by(() => {
    const me = view.me ?? "";
    return Object.entries(view.users ?? {})
      .filter(([id]) => id !== "" && id !== me)
      .map(([id, name]) => ({ id, name: name || id }));
  });

  /* ── the list's own custom buttons ──
     The project's board-placed buttons (Project settings → Ticket system →
     Integrations). A click sends the filter this toolbar is showing, so the
     receiver acts on the same set the clicker is looking at — not on the
     whole board. A button with no id has never been saved and has nothing
     to fire. */
  const boardButtons = $derived(
    (view.config?.integrations?.buttons ?? []).filter(
      (b) => b.placement === "board" && (b.id ?? "") !== "",
    ),
  );

  /* One button at a time: the id doubles as the busy flag, so a slow
     receiver cannot be double-fired by an impatient second click. */
  let actionBusy = $state("");
  /* The last click's answer, rendered as a panel above the columns. A
     second click while a run is going gets whatever the receiver says about
     it ("already running, 12/40") — that is the receiver's job, not ours,
     and it is why the click is not blocked once the request returns. */
  /* Multi mode answers once per project, so the panels are a list; single
     mode only ever holds one. */
  type ActionRun = { projectId: string; projectName: string; buttonId: string; label: string; result: ActionResult };
  let actionRuns = $state<ActionRun[]>([]);

  function runBoardButton(b: TicketButton) {
    if (!b.id || actionBusy !== "") return;
    actionBusy = b.id;
    Effect.runPromise(
      runBoardAction(base, projectId!, b.id, {
        /* The saved filter, not the derived list: "" and "me" are the
           toolbar's own words, and the server resolves "me" against the
           clicker. activeStatuses is sent instead of filter.statuses so an
           empty saved filter goes out as the columns it actually means
           (never the collapsed Done) —
           and every-chip-off keeps the sentinel, because an empty list
           reads as "no filter" on the wire and would send the whole board
           while the clicker is looking at nothing. */
        assignee: filter.assignee ?? "",
        statuses: activeStatuses.length > 0 ? activeStatuses : [NONE],
      }).pipe(Effect.provide(WickClientLayer)),
    )
      .then((r) => {
        /* The panel owns the detail from here — progress, counters, the
           receiver's own HTML, and the refresh when the run ends. The toast
           stays for the one thing a panel cannot do: tell somebody who has
           already looked away that it failed outright. */
        actionRuns = [{ projectId: projectId!, projectName: "", buttonId: b.id!, label: b.label, result: r }];
        if (!r.ok) toastError(`${b.label} failed: ${r.error || r.message || "HTTP " + r.status}`);
      })
      .catch((e: unknown) => {
        const msg = e instanceof Error ? e.message : `${b.label} failed`;
        actionRuns = [{ projectId: projectId!, projectName: "", buttonId: b.id!, label: b.label, result: { ok: false, error: msg } }];
        toastError(msg);
      })
      .finally(() => {
        actionBusy = "";
      });
  }

  /* Multi mode: one label, one call per project that has it — each with
     its own button and the filter narrowed to its own columns. Only the
     projects on screen take part (groupBoardButtons gets shownProjects),
     so an unselected or filtered-out project is never fired. */
  const buttonGroups = $derived<BoardButtonGroup[]>(projects ? groupBoardButtons(shownProjects) : []);

  function runBoardGroup(g: BoardButtonGroup) {
    if (actionBusy !== "" || g.runs.length === 0) return;
    actionBusy = "group:" + g.label;
    actionRuns = [];
    Promise.all(
      g.runs.map((r) => {
        const p = projectById(r.projectId);
        const run = (result: ActionResult) => {
          actionRuns = [...actionRuns, { projectId: r.projectId, projectName: r.projectName, buttonId: r.button.id!, label: g.label, result }];
        };
        return Effect.runPromise(
          runBoardAction(base, r.projectId, r.button.id!, {
            assignee: filter.assignee ?? "",
            statuses: activeStatuses.length > 0 && p ? statusesFor(activeStatuses, p, NONE) : [NONE],
          }).pipe(Effect.provide(WickClientLayer)),
        )
          .then((res) => {
            run(res);
            if (!res.ok) toastError(`${g.label} failed in ${r.projectName}: ${res.error || res.message || "HTTP " + res.status}`);
          })
          .catch((e: unknown) => {
            const msg = e instanceof Error ? e.message : `${g.label} failed`;
            run({ ok: false, error: msg });
            toastError(`${r.projectName}: ${msg}`);
          });
      }),
    ).finally(() => {
      actionBusy = "";
    });
  }

  function toggleStatus(s: string) {
    onFilter({ ...filter, statuses: toggledStatuses(activeStatuses, s, statusKeys, terminalStatusKeys, NONE) });
  }

  /* ── new ticket ── */
  let creating = $state(false);
  let newTitle = $state("");
  let saving = $state(false);
  /* Set when "+ New ticket" was reached from an untracked session, so the
     chat is attached to the ticket it just became. */
  let creatingFromSession = $state<string | null>(null);
  /* Multi mode: the project the new ticket goes to. A chat's ticket goes
     to the chat's project; otherwise the one filtered to, the only one, or
     nothing yet — Add waits until somebody picks. */
  let newProject = $state("");

  function startNew(fromSession?: string) {
    creating = true;
    creatingFromSession = fromSession ?? null;
    if (projects) {
      newProject = fromSession
        ? (projectOfSession(fromSession) ?? "")
        : shownProjects.length === 1
          ? shownProjects[0].id
          : "";
    }
    newTitle = fromSession
      ? (untracked.find((s) => s.id === fromSession)?.label ?? "")
      : "";
  }

  function submitNew() {
    const title = newTitle.trim();
    if (title === "") return;
    const target = multi ? newProject : projectId!;
    if (target === "") return;
    saving = true;
    Effect.runPromise(
      createTicket(base, target, {
        title,
        session_id: creatingFromSession ?? undefined,
      }).pipe(Effect.provide(WickClientLayer)),
    )
      .then(() => {
        if (creatingFromSession) onSessionTracked?.(creatingFromSession);
        newTitle = "";
        creating = false;
        creatingFromSession = null;
        reload(target);
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to create ticket"))
      .finally(() => {
        saving = false;
      });
  }

  /* ── drag & drop ── */
  /* A ticket drag carries the card's project: the same id can sit in two
     projects on the combined board. */
  type Drag = { kind: "ticket" | "session"; id: string; project?: string };
  let drag = $state<Drag | null>(null);
  let dropColumn = $state<string | null>(null);

  const sessionDragging = $derived(drag?.kind === "session");

  /* Multi mode: a column the dragged card's (or chat's) own project does
     not have. It is dimmed while dragging and refuses the drop — the
     status would mean nothing to that project. */
  function dragProject(d: Drag): BoardProject | undefined {
    return projectById(d.kind === "ticket" ? d.project : projectOfSession(d.id));
  }
  function refuses(d: Drag | null, status: string): boolean {
    return multi && d !== null && !hasStatus(dragProject(d), status);
  }
  function refuseDrop(d: Drag, status: string) {
    const name = dragProject(d)?.name ?? "This project";
    toastError(`${name} has no "${labelOf(status)}" column`);
  }

  function startTicketDrag(e: DragEvent, ticketId: string, project?: string) {
    drag = { kind: "ticket", id: ticketId, project };
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
    if (refuses(d, status)) {
      refuseDrop(d, status);
      return;
    }
    const target = multi ? projectOfSession(d.id) : projectId;

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
        createTicket(base, target!, {
          title,
          status,
          session_id: d.id,
          // Dragging a chat into a column is someone saying "I am taking
          // this on", so it lands assigned to them rather than as an
          // unassigned card they then have to claim.
          assignee: view.me,
        }).pipe(Effect.provide(WickClientLayer)),
      )
        .then(() => {
          onSessionTracked?.(d.id);
          reload(target);
        })
        .catch((e: unknown) =>
          toastError(e instanceof Error ? e.message : "Failed to create the ticket"),
        );
      return;
    }

    const t = items.find((x) => x.id === d.id && (!multi || x.project_id === d.project));
    if (!t || t.status === status) return;
    const prev = t.status;
    t.status = status; // optimistic — the card jumps immediately
    Effect.runPromise(updateTicket(base, d.id, { status }, multi ? d.project : projectId).pipe(Effect.provide(WickClientLayer)))
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
  function dropSessionOnTicket(ticketId: string, ticketProject?: string) {
    const d = drag;
    drag = null;
    if (!d || d.kind !== "session") return;
    const alreadyHere = items.some(
      (t) =>
        t.id === ticketId &&
        (!multi || t.project_id === ticketProject) &&
        (t.session_rows ?? []).some((s) => s.id === d.id),
    );
    if (alreadyHere) return;
    /* A chat lives in one project; a ticket of another cannot hold it. */
    const pid = multi ? projectOfSession(d.id) : projectId;
    if (multi && pid !== ticketProject) {
      toastError("A chat can only go on a ticket of its own project");
      return;
    }
    Effect.runPromise(
      attachSession(base, ticketId, d.id, pid).pipe(Effect.provide(WickClientLayer)),
    )
      .then((r) => {
        // Moving the last chat off a ticket leaves nothing to track, so the
        // husk is offered for removal instead of being left on the board.
        if (r.emptied_ticket) onEmptied?.(r.emptied_ticket, multi ? pid : undefined);
        onSessionTracked?.(d.id);
        reload(pid);
      })
      .catch((e: unknown) => toastError(e instanceof Error ? e.message : "Failed to move the chat"));
  }
</script>

{#snippet railRow(s: TicketSessionRow)}
    <div
      role="button"
      tabindex="0"
      draggable="true"
      data-testid={"untracked-" + s.id}
      title="Drag onto a ticket to attach this chat"
      ondragstart={(e) => startSessionDrag(e, s.id)}
      onclick={() => openSession(s.id)}
      onkeydown={(e) => e.key === "Enter" && openSession(s.id)}
      class="cursor-grab rounded-lg border border-white-300 bg-white-100 p-2 transition-colors hover:border-green-500 active:cursor-grabbing dark:border-navy-600 dark:bg-navy-700"
    >
      <p class="line-clamp-2 text-[11px] font-medium text-black-900 dark:text-white-100">
        {s.label || s.id}
      </p>
      <div class="mt-1 flex items-center gap-1.5 text-[10px] text-black-700 dark:text-black-600">
        {#if sessionRowBadge(s.lifecycle)}
          <span class="rounded bg-pos-100 px-1 font-medium text-pos-400">{sessionRowBadge(s.lifecycle)}</span>
        {/if}
        {#if s.last_active}<span>{timeAgo(s.last_active, $now)}</span>{/if}
        <button
          type="button"
          title="Create a ticket from this chat"
          onclick={(e) => { e.stopPropagation(); startNew(s.id); }}
          class="ml-auto rounded px-1 text-[10px] font-medium text-green-600 transition-colors hover:bg-green-50 dark:text-green-400 dark:hover:bg-green-900/20"
        >+ ticket</button>
      </div>
    </div>
{/snippet}

<!-- The board is pinned to the viewport: the PAGE never scrolls — each
     column and the untracked rail scroll on their own, under their own
     floating header, so scanning one long column keeps every other state
     in sight. -->
<div class="flex h-full min-h-0 flex-col gap-4">
  <!-- Filter bar -->
  <div class="flex flex-wrap items-center gap-2">
    {#each statusKeys as s (s)}
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
        {labelOf(s)}
      </button>
    {/each}

    <!-- Untracked sits with the status chips because it is the same kind of
         choice: which of the board's parts to fetch. Its count comes down
         either way, so the cost of switching it on is on the label. -->
    <button
      type="button"
      aria-pressed={showUntracked}
      data-testid="chip-untracked"
      onclick={() => onFilter({ ...filter, show_untracked: !showUntracked })}
      class={[
        "rounded-full border px-3 py-1 text-xs font-medium transition-colors",
        showUntracked
          ? "border-green-500 bg-green-50 text-green-600 dark:bg-green-900/20 dark:text-green-400"
          : "border-dashed border-white-400 bg-white-100 text-black-700 hover:bg-white-200 dark:border-navy-600 dark:bg-navy-700 dark:text-black-600 dark:hover:bg-navy-600",
      ].join(" ")}
    >
      Untracked{untrackedTotal > 0 ? ` (${untrackedTotal})` : ""}
    </button>

    <!-- Search. Sits between the chips it narrows and the assignee filter it
         works alongside: all three say "which of these am I looking at". -->
    <div class="relative ml-auto">
      <svg
        viewBox="0 0 16 16"
        class="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-black-600 dark:text-black-700"
        fill="none"
        stroke="currentColor"
        stroke-width="1.6"
        aria-hidden="true"
      >
        <circle cx="7" cy="7" r="4.5"></circle>
        <path d="M10.5 10.5L14 14" stroke-linecap="round"></path>
      </svg>
      <input
        bind:value={query}
        type="search"
        data-testid="board-search"
        placeholder="Search tickets…"
        aria-label="Search tickets by id, title or assignee"
        onkeydown={(e) => { if (e.key === "Escape") query = ""; }}
        class="w-44 rounded-lg border border-white-400 bg-white-100 py-1.5 pl-8 pr-2 text-xs text-black-900 transition-all focus:w-56 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
      />
    </div>

    {#if q !== ""}
      <!-- The query as a chip, next to the status chips: a search is a filter
           like any other, and a board that has quietly dropped two thirds of
           its cards must say what did it — and in which words. -->
      <button
        type="button"
        data-testid="board-search-chip"
        title="Clear the search"
        onclick={() => { query = ""; }}
        class="inline-flex items-center gap-1.5 rounded-full border border-green-500 bg-green-50 px-3 py-1 text-xs font-medium text-green-600 transition-colors hover:bg-green-100 dark:bg-green-900/20 dark:text-green-400 dark:hover:bg-green-900/40"
      >
        <span class="max-w-[12rem] truncate">“{query.trim()}”</span>
        <span class="rounded-full bg-green-100 px-1.5 text-[10px] font-semibold text-green-700 dark:bg-green-900/40 dark:text-green-300">
          {matchCount}
        </span>
        <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true">
          <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path>
        </svg>
      </button>
    {/if}

    {#if projects && projects.length >= 2}
      <select
        value={projectFilter}
        data-testid="board-project"
        aria-label="Project"
        onchange={(e) => { projectFilter = (e.target as HTMLSelectElement).value; }}
        class="rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-xs text-black-800 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
      >
        <option value="">Project: All ({projects.length})</option>
        {#each projects as p (p.id)}
          <option value={p.id}>Project: {p.name}</option>
        {/each}
      </select>
    {/if}

    {#if roomAgents.length > 0}
      <select
        value={roomAgent}
        data-testid="board-room-agent"
        aria-label="Agent in room"
        onchange={(e) => { roomAgent = (e.target as HTMLSelectElement).value; }}
        class="rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-xs text-black-800 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
      >
        <option value="">Agent in room: Any</option>
        {#each roomAgents as o (o.key)}
          <option value={o.key}>Agent in room: {o.label}</option>
        {/each}
      </select>
    {/if}

    <select
      value={filter.assignee ?? ""}
      onchange={(e) => onFilter({ ...filter, assignee: (e.target as HTMLSelectElement).value })}
      class="rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-xs text-black-800 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
    >
      <option value="">Assignee: All</option>
      {#if view.me}<option value="me">Assignee: Me</option>{/if}
      {#each assigneeOptions as u (u.id)}
        <option value={u.id}>Assignee: {u.name}</option>
      {/each}
    </select>

    <!-- Custom list buttons, between the filter they act on and the one
         control that makes a ticket instead of acting on the ones there. -->
    {#if projects}
      {#each buttonGroups as g (g.label)}
        <button
          type="button"
          disabled={actionBusy !== ""}
          data-testid={"board-button-" + g.label}
          title={`Runs on ${g.runs.map((r) => r.projectName).join(", ")}, each with this list's filter`}
          onclick={() => runBoardGroup(g)}
          class="rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-xs font-medium text-black-800 transition-colors hover:border-green-500 hover:text-green-600 disabled:opacity-40 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100 dark:hover:text-green-400"
        >{actionBusy === "group:" + g.label ? "Sending…" : buttonCaption(g)}</button>
      {/each}
    {:else}
    {#each boardButtons as b (b.id)}
      <button
        type="button"
        disabled={actionBusy !== ""}
        title={`Sends this list's filter to ${b.url}`}
        onclick={() => runBoardButton(b)}
        class="rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-xs font-medium text-black-800 transition-colors hover:border-green-500 hover:text-green-600 disabled:opacity-40 dark:border-navy-600 dark:bg-navy-700 dark:text-white-100 dark:hover:text-green-400"
      >{actionBusy === b.id ? "Sending…" : b.label}</button>
    {/each}
    {/if}

    {#if creating}
      <form
        onsubmit={(e) => { e.preventDefault(); submitNew(); }}
        class="flex w-full items-center gap-2 sm:w-auto"
      >
        <!-- Asked only when there is a choice — or when a chat's project
             could not be told, so Add is never dead without a reason. -->
        {#if projects && (newProject === "" || (shownProjects.length >= 2 && !creatingFromSession))}
          <select
            bind:value={newProject}
            aria-label="Project of the new ticket"
            data-testid="new-ticket-project"
            class="rounded-lg border border-white-400 bg-white-100 px-2 py-1.5 text-xs text-black-800 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
          >
            <option value="" disabled>Project…</option>
            {#each shownProjects as p (p.id)}
              <option value={p.id}>📁 {p.name}</option>
            {/each}
          </select>
        {/if}
        <input
          bind:value={newTitle}
          placeholder="What needs doing?"
          aria-label="New ticket title"
          class="min-w-0 flex-1 rounded-lg border border-white-400 bg-white-100 px-3 py-1.5 text-xs text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-700 dark:text-white-100"
        />
        <button
          type="submit"
          disabled={saving || newTitle.trim() === "" || (multi && newProject === "")}
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

  {#each actionRuns as run (run.projectId + "/" + run.buttonId)}
    <BoardActionResult
      {base}
      projectId={run.projectId}
      buttonId={run.buttonId}
      label={multi ? `${run.label} · 📁 ${run.projectName}` : run.label}
      result={run.result}
      onFinished={multi ? () => onReload(run.projectId) : onReload}
      onDismiss={() => { actionRuns = actionRuns.filter((x) => x !== run); }}
    />
  {/each}

  <div class="flex min-h-0 flex-1 flex-col gap-3 lg:flex-row">
    <!-- Untracked chats. Left rail rather than a column, because they have
         no status to be in — they are the pool you drag FROM.

         Present only when the chip above asked for it, and absent rather
         than collapsed: the rail has no switch of its own, because the
         choice is which parts of the board to fetch and that belongs with
         the other filters. -->
    {#if showUntracked}
    <aside class="min-h-0 shrink-0 lg:w-56">
      <div class="flex h-full max-h-full min-h-0 flex-col gap-2 rounded-xl bg-white-200 p-2 dark:bg-navy-800">
        <div class="flex items-center gap-1.5 px-2 py-1">
          <span class="text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">
            Untracked
          </span>
          <span class="ml-auto rounded-full bg-white-100 px-2 py-0.5 text-[10px] font-semibold text-black-800 dark:bg-navy-700 dark:text-black-600">
            {untrackedTotal > untracked.length ? `${untracked.length}/${untrackedTotal}` : untracked.length}
          </span>
        </div>

        <!-- Same scope switch as the sessions list: yours by default,
             everyone's loose chats on request. Per-visit — never saved. -->
        <div class="px-2">
          <OwnerTabs
            value={untrackedOwner}
            onChange={onUntrackedOwner}
            mineLabel="Yours"
            allLabel="All"
            size="xs"
          />
        </div>

        {#if untracked.length === 0}
          <p class="px-2 py-4 text-center text-[11px] text-black-600 dark:text-black-700">
            {untrackedOwner === "me"
              ? "No untracked chats of yours here."
              : "Every chat here belongs to a ticket."}
          </p>
        {:else}
          <p class="px-2 pb-1 text-[10px] leading-relaxed text-black-600 dark:text-black-700">
            Drag onto a ticket to attach, onto a column to make it a ticket.
          </p>
          <!-- The rail scrolls on its own under its floating header; its
               end triggers the next page, so 25/91 never needs the whole
               page scrolled to grow. -->
          {#if projects}
          <!-- One group per project: its name, its own page-of-total and
               its own "load more", since each project pages its own rail. -->
          <ul class="flex min-h-0 flex-1 flex-col gap-1.5 overflow-y-auto" data-testid="rail-multi">
            {#each shownProjects as p (p.id)}
              {@const rows = untracked.filter((r) => projectOfSession(r.id) === p.id)}
              {@const total = p.board.untracked_total ?? rows.length}
              {#if rows.length > 0}
                <li class="flex items-center gap-1.5 px-1 pt-1 text-[10px] font-semibold text-black-700 dark:text-black-600" data-testid={"rail-project-" + p.id}>
                  <span class="min-w-0 truncate">📁 {p.name}</span>
                  <span class="ml-auto">{total > rows.length ? `${rows.length}/${total}` : rows.length}</span>
                </li>
                {#each rows as s (s.id)}
                  <li>
                    {@render railRow(s)}
                  </li>
                {/each}
                {#if p.board.untracked_next && rows.length < UNTRACKED_RAIL_MAX}
                  <li>
                    <button
                      type="button"
                      data-testid={"rail-more-" + p.id}
                      onclick={() => onUntrackedMore(p.id)}
                      class="w-full rounded px-2 py-1 text-[10px] font-medium text-green-600 transition-colors hover:bg-green-50 dark:text-green-400 dark:hover:bg-green-900/20"
                    >Load more from {p.name}</button>
                  </li>
                {/if}
              {/if}
            {/each}
          </ul>
          {:else}
          <ul class="flex min-h-0 flex-1 flex-col gap-1.5 overflow-y-auto">
            {#each untracked as s (s.id)}
              <li>
                {@render railRow(s)}
              </li>
            {/each}
            <li>
              <ScrollSentinel
                canMore={untrackedHasMore}
                epoch={untracked.length}
                onMore={onUntrackedMore}
              />
            </li>
          </ul>
          {/if}
        {/if}
      </div>
    </aside>
    {/if}

    <!-- Columns. None of them is a legitimate state — every chip off means
         no cards were fetched — so it gets said rather than shown as a
         blank strip next to the rail. -->
    {#if columns.length === 0}
      <p class="flex-1 rounded-xl bg-white-200 px-4 py-10 text-center text-xs text-black-600 dark:bg-navy-800 dark:text-black-700">
        No columns selected. Pick a status above to load its tickets.
      </p>
    {:else}
    <!-- Columns are kanban-shaped: fixed width, one row, never wrapping —
         a board wider than the screen scrolls sideways, the way every
         kanban tool works. The inner wrapper shrink-wraps the columns and
         centres them when they fit, without clipping the first column when
         they do not (margin:auto collapses to 0 under overflow). -->
    <div class="min-h-0 min-w-0 flex-1 overflow-x-auto pb-2">
    <div class="mx-auto flex h-full w-fit gap-3">
      {#each columns as col, i (col.status)}
        <div
          role="list"
          aria-label={labelOf(col.status)}
          data-refused={refuses(drag, col.status) ? "true" : undefined}
          ondragover={(e) => {
            // A column the dragged item's project lacks: not cancelled, so
            // the browser shows "no drop" — the drop handler refuses too.
            if (refuses(drag, col.status)) {
              if (e.dataTransfer) e.dataTransfer.dropEffect = "none";
              dropColumn = null;
              return;
            }
            // A ticket lands here to change status; a session lands here to
            // BECOME a ticket at that status. A card intercepts the session
            // case first when the pointer is actually over one.
            e.preventDefault();
            dropColumn = col.status;
          }}
          ondragleave={() => { if (dropColumn === col.status) dropColumn = null; }}
          ondrop={(e) => { e.preventDefault(); dropOnColumn(col.status); }}
          class={[
            "flex max-h-full min-h-[280px] w-72 shrink-0 flex-col gap-2 rounded-xl border border-white-300 bg-white-200 p-2 transition-colors dark:border-navy-600 dark:bg-navy-800",
            dropColumn === col.status ? "ring-2 ring-inset ring-green-500" : "",
            refuses(drag, col.status) ? "opacity-50" : "",
          ].join(" ")}
        >
          <!-- The header sits OUTSIDE the scroller below, so it stays pinned
               while the column's cards scroll under it. -->
          <div class="flex items-center gap-2 border-b border-white-300 px-2 pb-2 pt-1 dark:border-navy-600">
            <span class={"h-2 w-2 shrink-0 rounded-full " + dotFor(col.status, i)} aria-hidden="true"></span>
            <span class="min-w-0 truncate text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">
              {labelOf(col.status)}
            </span>
            <span class="ml-auto rounded-full bg-white-100 px-2 py-0.5 text-[10px] font-semibold text-black-800 dark:bg-navy-700 dark:text-black-600">
              {col.tickets.length}
            </span>
          </div>
          <!-- Per-column scroller: only THIS column moves when it scrolls;
               its neighbours (and the page) stay put. -->
          <div class="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto">
            {#each col.tickets as t (cardKey(t))}
              <div class={t.status === terminalKey ? "opacity-70" : ""}>
                <TicketCard
                  {base}
                  ticket={t}
                  schema={projects ? projectById(projectOfTicket(t))?.board.config?.fields : view.config.fields}
                  users={view.users}
                  project={showProjectTag ? projectById(projectOfTicket(t))?.name : undefined}
                  projectId={multi ? projectOfTicket(t) : projectId}
                  highlight={q}
                  onOpen={multi ? (id) => onOpen(id, projectOfTicket(t)) : onOpen}
                  onOpenSession={multi ? openSession : onOpenSession}
                  {sessionDragging}
                  sessionRefused={multi && drag?.kind === "session" && projectOfSession(drag.id) !== projectOfTicket(t)}
                  onDragStart={(e, id) => startTicketDrag(e, id, projectOfTicket(t))}
                  onSessionDragStart={startSessionDrag}
                  onSessionDrop={(id) => dropSessionOnTicket(id, projectOfTicket(t))}
                />
              </div>
            {/each}
            {#if col.tickets.length === 0}
              <div class="mx-1 my-2 flex flex-1 items-center justify-center rounded-lg border border-dashed border-white-400 py-8 dark:border-navy-600">
                <!-- An empty column under a search means "nothing matched",
                     not "nothing here" — inviting a drag would be a lie. -->
                <p class="px-2 text-center text-xs text-black-600 dark:text-black-700">
                  {q === "" ? "No tickets — drag one here" : "No match here"}
                </p>
              </div>
            {/if}
          </div>
        </div>
      {/each}
    </div>
    </div>
    {/if}
  </div>
</div>
