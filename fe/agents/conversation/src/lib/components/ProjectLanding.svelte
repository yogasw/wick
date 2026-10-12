<script lang="ts">
  import { untrack } from "svelte";
  import type { ProjectOption, ProviderOption, SessionListItem } from "../types/agents.js";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import {
    boardQuery,
    getProjectTickets,
    getTicketFilter,
    saveTicketFilter,
    type EmptiedTicket,
  } from "../api/tickets.js";
  import { boardGen, cachedBoard, cachedFilter, rememberBoard, rememberFilter } from "../ticketBoardCache.js";
  import type { TicketBoard, TicketFilter, TicketSessionRow } from "../types/agents.js";
  import { assigneeParam, assigneesOf } from "../ticketAssigneeFilter.js";
  import TicketComposer from "./TicketComposer.svelte";
  import SessionList from "./SessionList.svelte";
  import KanbanBoard from "./KanbanBoard.svelte";
  import { mergeRail, keepPushedOff } from "../railPaging.js";
  import TicketDetail from "./TicketDetail.svelte";
  import EmptyTicketOffer from "./EmptyTicketOffer.svelte";
  import { hasTeam, teamTicketsHref } from "../teamTicketLinks.js";
  import { connectSessionsStream } from "../stores/sessionsStream.js";
  import OwnerTabs from "./OwnerTabs.svelte";
  import ProjectMenu from "./ProjectMenu.svelte";

  type Props = {
    base: string;
    project: ProjectOption;
    providers: ProviderOption[];
    sessions: SessionListItem[];
    /* Server-side match count for the current scope — the list's number and
       the breadcrumb's, both printed only once the list itself arrived. */
    sessionTotal?: number;
    ownerTab: "me" | "all";
    listLoading?: boolean;
    hasMore?: boolean;
    onLoadMore?: () => void;
    onOwnerTab: (v: "me" | "all") => void;
    onPin: () => void;
    onSelectSession: (id: string) => void;
  };

  let {
    base,
    project,
    providers,
    sessions,
    sessionTotal,
    ownerTab,
    listLoading = false,
    hasMore = false,
    onLoadMore,
    onOwnerTab,
    onPin,
    onSelectSession,
  }: Props = $props();

  let search = $state("");

  const chatCount = $derived(sessionTotal ?? sessions.length);

  /* ── ticket board (only when the project has ticket mode enabled) ── */
  let board = $state<TicketBoard | null>(null);
  let ticketFilter = $state<TicketFilter>({});
  /* The untracked rail's scope. Deliberately NOT part of the saved filter:
     every visit to a project board starts back at "yours" — widening to
     everyone's loose chats is a per-look choice, not a standing one. */
  let untrackedOwner = $state<"me" | "all">("me");
  /* The poll asks for the rail's FIRST page only. Older pages are fetched
     once each, on scroll, and kept here — so a rail scrolled back through a
     few hundred chats does not re-send all of them every 30 seconds. */
  const UNTRACKED_PAGE = 25;
  const UNTRACKED_MORE_PAGE = 50;
  let untrackedMore = $state<TicketSessionRow[]>([]);
  /* Where the kept pages end: the cursor the last of them came back with,
     "" when nothing follows. Meaningless while untrackedMore is empty — the
     first page's own untracked_next continues the rail then. */
  let untrackedMoreNext = $state("");
  let loadingMoreUntracked = false;
  /* The request the current board answered, so a poll can tell "the same
     list moved" from "a different list". */
  let boardKeyLoaded = "";
  /* Chats this page just put on a ticket. They leave the rail for the rest
     of the request even if a page loaded earlier still carries them. */
  let trackedHere = $state(new Set<string>());

  /* The filter IS the request: statuses, assignee and the untracked rail all
     decide what the server builds, so a switched-off column costs nothing to
     poll instead of arriving and being discarded. */
  /* Derived, not a plain function, so the fetch below re-runs on the three
     fields that change the REQUEST and not on view_mode, which only changes
     how the same data is drawn. */
  const boardOptions = $derived.by(() => {
    const saved = ticketFilter.statuses;
    /* An empty saved list means "all statuses" — the request omits the param
       so a project that later adds a stage shows it. The board spells "every
       chip off" with a single sentinel entry, which becomes an explicit empty
       set: no columns drawn, no cards fetched. */
    const statuses =
      !saved || saved.length === 0 ? undefined : saved[0] === " none" ? [] : saved;
    return {
      rows: 3,
      statuses,
      assignee: assigneeParam(assigneesOf(ticketFilter)),
      untracked: ticketFilter.show_untracked === true,
      untrackedLimit: UNTRACKED_PAGE,
      /* Your own loose chats by default — "all" is an explicit choice, and
         the count follows the scope so the rail's number and its rows always
         describe the same set. */
      untrackedOwner,
    };
  });

  /* The saved filter arrives first, then the board it describes: fetching
     the board before knowing the filter would request the wrong thing and
     immediately request it again. */
  let filterLoaded = $state(false);
  $effect(() => {
    // The filter read last time sends the board's request at once; the
    // saved one, when it differs, sends it again.
    const seen = cachedFilter(project.id);
    if (seen) {
      ticketFilter = seen;
      filterLoaded = true;
    }
    Effect.runPromise(getTicketFilter(base, project.id).pipe(Effect.provide(WickClientLayer)))
      .then((f) => {
        ticketFilter = f ?? {};
        rememberFilter(project.id, $state.snapshot(ticketFilter));
      })
      .catch(() => { /* filter optional — the defaults are a fine board */ })
      .finally(() => { filterLoaded = true; });
  });

  /* The fetch keys off this string rather than off boardOptions itself:
     every filter edit rebuilds that object, so an object dependency would
     re-fetch an identical board whenever the user merely switched
     list/card. */
  const boardRequestKey = $derived(project.id + "|" + JSON.stringify(boardOptions));

  $effect(() => {
    if (!filterLoaded) return;
    boardRequestKey; // the sole dependency: re-fetch when the request changes
    // A different request (scope, filter, project) is a different list:
    // the pages loaded under the old one no longer continue it.
    untrackedMore = [];
    untrackedMoreNext = "";
    trackedHere = new Set();
    reloadBoard();
  });

  const ticketEnabled = $derived(board?.config?.enabled === true);
  const viewMode = $derived(ticketEnabled && ticketFilter.view_mode === "card" ? "card" : "list");

  /* Which ticket's detail is open, if any. Read from ?ticket= on load so a
     link out of the conversation rail lands here. */
  let openTicketId = $state<string | null>(
    new URLSearchParams(window.location.search).get("ticket"),
  );

  /* Ticket navigation writes the URL, so back/forward walk board ↔ ticket
     like pages and a ticket's URL can be shared. Every place that opens or
     closes a ticket goes through here; popstate feeds the same state back. */
  function gotoTicket(id: string | null) {
    openTicketId = id;
    const url = new URL(window.location.href);
    if (id) url.searchParams.set("ticket", id);
    else url.searchParams.delete("ticket");
    if (url.toString() !== window.location.href) history.pushState({}, "", url);
  }

  $effect(() => {
    const onPop = () => {
      openTicketId = new URLSearchParams(window.location.search).get("ticket");
    };
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  });

  /* Next page of the untracked rail, appended — see railPaging.ts for why
     it is asked for by cursor and not by the drawn row count. */
  function loadMoreUntracked() {
    if (loadingMoreUntracked || !boardView?.untracked_next) return;
    loadingMoreUntracked = true;
    const key = boardRequestKey;
    const after = boardView.untracked_next;
    Effect.runPromise(
      getProjectTickets(base, project.id, {
        rows: 0,
        statuses: [], // rows only — no cards
        untracked: true,
        untrackedLimit: UNTRACKED_MORE_PAGE,
        untrackedAfter: after,
        untrackedOwner,
      }).pipe(Effect.provide(WickClientLayer)),
    )
      .then((b) => {
        if (key !== boardRequestKey) return; // the request changed meanwhile
        const have = new Set([...(board?.untracked ?? []), ...untrackedMore].map((r) => r.id));
        untrackedMore = [...untrackedMore, ...b.untracked.filter((r) => !have.has(r.id))];
        untrackedMoreNext = b.untracked_next ?? "";
      })
      .catch(() => { /* the sentinel offers it again on the next scroll */ })
      .finally(() => { loadingMoreUntracked = false; });
  }

  /* The board as drawn: the polled first page, then the pages loaded on
     scroll, minus any row the fresh first page now holds itself. Once pages
     are kept, the rail continues from where THEY end. */
  const boardView = $derived.by(() => {
    if (!board || untrackedMore.length === 0) return board;
    return {
      ...board,
      untracked: mergeRail(board.untracked, untrackedMore, trackedHere),
      untracked_next: untrackedMoreNext || undefined,
    };
  });

  /* A chat the board just attached or turned into a ticket: off the rail. */
  function sessionTracked(id: string) {
    trackedHere = new Set(trackedHere).add(id);
    untrackedMore = untrackedMore.filter((r) => r.id !== id);
  }

  function reloadBoard() {
    const key = boardRequestKey;
    const pid = project.id;
    const query = boardQuery(boardOptions);
    const gen = boardGen(pid);
    // Not drawn for this request yet: the last answer (ticketBoardCache)
    // first, the fresh one when it comes.
    // Untracked: the request effect calls this, and must not rerun on
    // the board it sets.
    untrack(() => {
      if (!board || boardKeyLoaded !== key) {
        const seen = cachedBoard(pid, query);
        if (seen) {
          board = seen;
          boardKeyLoaded = key;
        }
      }
    });
    Effect.runPromise(
      getProjectTickets(base, pid, boardOptions).pipe(Effect.provide(WickClientLayer)),
    )
      .then((b) => {
        rememberBoard(pid, query, b, gen);
        if (key !== boardRequestKey) return; // superseded by a newer request
        // Same list, moved: a chat pushed off page one stays on the rail.
        if (board && boardKeyLoaded === key) {
          untrackedMore = keepPushedOff(board.untracked, b.untracked, untrackedMore, trackedHere, UNTRACKED_PAGE);
          if (untrackedMore.length === 0) untrackedMoreNext = "";
        }
        board = b;
        boardKeyLoaded = key;
      })
      .catch(() => { /* keep the previous board on a transient failure */ });
  }

  /* ── keeping the board honest about time ──

     A ticket does not only change from this page. A sync writes one, an
     agent moves one, somebody else drags one — and until now the board only
     re-fetched when the FILTER changed, so it sat on whatever it was handed
     at open. "Updated 5h ago" stayed 5h ago for the rest of the day, and the
     column order with it, because the order IS the timestamp.

     So it listens: every ticket write sends a `ticket` signal on the shared
     /stream/sessions connection (stream_ticket.go, filtered to projects the
     caller may open), and a signal for this project refetches. A hidden
     tab only marks itself stale and refetches on return. Polling is the
     fallback while that stream is down, and a reconnect refetches once —
     signals sent during the gap are gone. Card ages ("5h ago") tick on
     their own clock (TicketCard), so time alone needs no fetch. */
  const BOARD_POLL_MS = 60_000;

  /* A drag pauses it. The board applies a move optimistically and confirms
     it with a PATCH; a poll landing in that gap would hand back the
     pre-move board and the card would visibly jump home and back. */
  let dragging = $state(false);
  $effect(() => {
    const on = () => { dragging = true; };
    const off = () => { dragging = false; };
    window.addEventListener("dragstart", on);
    window.addEventListener("dragend", off);
    window.addEventListener("drop", off);
    return () => {
      window.removeEventListener("dragstart", on);
      window.removeEventListener("dragend", off);
      window.removeEventListener("drop", off);
    };
  });

  $effect(() => {
    if (!filterLoaded) return;
    const pid = project.id;
    let streamUp = false;
    let dropped = false;
    let stale = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const due = () => {
      if (dragging || (typeof document !== "undefined" && document.hidden)) {
        stale = true;
        return;
      }
      stale = false;
      reloadBoard();
    };
    /* A burst (a sync touching many tickets) costs one fetch. */
    const soon = () => {
      if (timer !== null) clearTimeout(timer);
      timer = setTimeout(() => { timer = null; due(); }, 300);
    };
    const leave = connectSessionsStream(base, {
      onTicket: (t) => { if (t.project_id === pid) soon(); },
      onStatus: (st) => {
        streamUp = st === "connected";
        if (!streamUp) dropped = true;
        else if (dropped) { dropped = false; soon(); }
      },
    });
    const id = setInterval(() => { if (!streamUp) due(); }, BOARD_POLL_MS);
    const onVisible = () => { if (!document.hidden && (stale || !streamUp)) due(); };
    document.addEventListener("visibilitychange", onVisible);
    /* A drop ends a drag; a signal that arrived mid-drag is applied now. */
    const onDragEnd = () => { if (stale) soon(); };
    window.addEventListener("dragend", onDragEnd);
    window.addEventListener("drop", onDragEnd);
    return () => {
      leave();
      if (timer !== null) clearTimeout(timer);
      clearInterval(id);
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("dragend", onDragEnd);
      window.removeEventListener("drop", onDragEnd);
    };
  });


  /* With a Team, a ticket's page points at Team › Tickets, where several
     agents can work it; without one nothing changes. Read once a ticket
     page is open. */
  let teamOn = $state(false);
  $effect(() => {
    if (!openTicketId || teamOn) return;
    hasTeam(base).then((on) => { teamOn = on; });
  });

  /* A ticket that just lost its last chat: EmptyTicketOffer asks. */
  let emptyOffer = $state<ReturnType<typeof EmptyTicketOffer> | null>(null);
  const handleEmptied = (t: EmptiedTicket) => emptyOffer?.offer(t, project.id);

  /* Starting a session on a ticket is a two-step: create it in this
     project, then attach. The composer owns creation, so the ticket id is
     stashed and applied once the new session exists. */
  function newSessionInTicket(ticketId: string) {
    pendingTicketId = ticketId;
    gotoTicket(null);
  }
  let pendingTicketId = $state<string | null>(null);
  /* The ticket the next message lands on. SELECTING one is enough — having
     a ticket open on screen and typing into the composer above it can only
     mean the new chat belongs to it, and requiring "+ New session" first
     made the obvious path the one that silently did nothing. The stash
     still exists for that button, which closes the detail on its way out.
     Cleared explicitly, so "Start without a ticket" also drops the open
     one rather than being undone by it on the next render. */
  const activeTicketId = $derived(pendingTicketId ?? openTicketId);
  /* Resolved to its card so the composer can name it; a ticket picked
     before the board finished loading still has its id to fall back on. */
  const activeTicket = $derived(
    activeTicketId ? (board?.tickets.find((t) => t.id === activeTicketId) ?? null) : null,
  );
  const activeTicketLabel = $derived(
    activeTicket ? activeTicket.title || activeTicket.id : (activeTicketId ?? ""),
  );
  let filterSaveTimer: ReturnType<typeof setTimeout> | undefined;
  function applyFilter(f: TicketFilter) {
    ticketFilter = f;
    rememberFilter(project.id, $state.snapshot(ticketFilter));
    clearTimeout(filterSaveTimer);
    filterSaveTimer = setTimeout(() => {
      Effect.runPromise(
        saveTicketFilter(base, project.id, ticketFilter).pipe(Effect.provide(WickClientLayer)),
      ).catch(() => { /* saving the preference is best-effort */ });
    }, 400);
  }
  const setViewMode = (mode: "list" | "card") => applyFilter({ ...ticketFilter, view_mode: mode });
</script>

<!-- The page is full-bleed, but only the board and a ticket's page USE that
     width — they are work surfaces, like any ticketing tool. The back link,
     header, and composer stay a centered column no matter the view, so
     typing a message never happens in a viewport-wide input. -->
<div class="flex flex-col h-full p-6 mx-auto w-full gap-4">
  <!-- Session list / ticket board. The List|Card toggle appears only when
       this project has ticket mode enabled; the choice is saved per user.
       Width follows the job: the plain list stays the composer's column, the
       board gets room for four columns, a ticket's page sits in between —
       wide enough for a main column plus its properties rail, never
       edge-to-edge. -->
  <div
    class={"flex flex-col gap-3 flex-1 min-h-0 " +
      (ticketEnabled && openTicketId
        ? "mx-auto w-full max-w-7xl"
        : ticketEnabled && viewMode === "card"
          ? "w-full max-w-none"
          : "page-col")}
  >
    {#if ticketEnabled && !openTicketId}
      <div class="flex items-center justify-between">
        <div class="inline-flex overflow-hidden rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
          <button
            type="button"
            aria-pressed={viewMode === "list"}
            onclick={() => setViewMode("list")}
            class={"px-4 py-1.5 text-xs font-medium transition-colors " + (viewMode === "list"
              ? "bg-green-500 text-white-100"
              : "text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600")}
          >List</button>
          <button
            type="button"
            aria-pressed={viewMode === "card"}
            onclick={() => setViewMode("card")}
            class={"px-4 py-1.5 text-xs font-medium transition-colors " + (viewMode === "card"
              ? "bg-green-500 text-white-100"
              : "text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-600")}
          >Card</button>
        </div>
        <div class="ml-auto flex items-center gap-2">
          {#if viewMode === "list"}
            <OwnerTabs value={ownerTab} onChange={onOwnerTab} />
          {/if}
          <ProjectMenu {base} {project} {chatCount} {onPin} />
        </div>
      </div>
    {:else if !ticketEnabled && !openTicketId}
      <!-- No board here, but the same scope choice: your chats first, the
           whole project's on request. -->
      <div class="flex items-center justify-end gap-2">
        <OwnerTabs value={ownerTab} onChange={onOwnerTab} />
        <ProjectMenu {base} {project} {chatCount} {onPin} />
      </div>
    {:else if ticketEnabled && openTicketId}
      <!-- A ticket's page has its own Back; only the project's actions need
           somewhere to live. -->
      <div class="flex items-center justify-end">
        <ProjectMenu {base} {project} {chatCount} {onPin} />
      </div>
    {/if}

    {#if ticketEnabled && openTicketId}
      <!-- The page height is locked (each panel scrolls itself), so the
           ticket's long page scrolls in its own container. -->
      <div class="min-h-0 flex-1 overflow-y-auto pb-24">
        {#if teamOn}
          <div class="mb-3 flex items-center gap-3 rounded-lg border border-white-300 bg-white-200 px-3 py-2 text-xs dark:border-navy-600 dark:bg-navy-800" data-testid="open-in-team">
            <span class="min-w-0 flex-1 text-black-800 dark:text-black-600">Need several agents on this ticket? That happens in Team.</span>
            <a
              class="shrink-0 rounded-lg bg-green-500 px-3 py-1.5 font-semibold text-white-100 hover:bg-green-600"
              href={teamTicketsHref(base, project.id, openTicketId)}
            >Open in Team › Tickets ↗</a>
          </div>
        {/if}
        <TicketDetail
          {base}
          ticketId={openTicketId}
          onBack={() => { gotoTicket(null); reloadBoard(); }}
          onOpenSession={onSelectSession}
          onNewSession={newSessionInTicket}
          projectId={project.id}
        />
      </div>
    {:else if ticketEnabled && viewMode === "card" && boardView}
      <KanbanBoard
        {base}
        projectId={project.id}
        board={boardView}
        filter={ticketFilter}
        {untrackedOwner}
        onUntrackedOwner={(v) => { untrackedOwner = v; }}
        onUntrackedMore={loadMoreUntracked}
        onSessionTracked={sessionTracked}
        onFilter={applyFilter}
        onOpen={(id) => { gotoTicket(id); }}
        onOpenSession={onSelectSession}
        onReload={reloadBoard}
        onEmptied={handleEmptied}
      />
    {:else}
      <SessionList
        {sessions}
        total={sessionTotal}
        loading={listLoading}
        {hasMore}
        {onLoadMore}
        {search}
        onSearch={(s) => { search = s; }}
        onSelect={onSelectSession}
      />
    {/if}
  </div>

  <!-- The floating composer: every view's reply box. Starting a chat from
       a ticket page joins that ticket; from the list or board it is a
       plain new session in the project. On a ticket's page it sits in the
       main column — the same grid as TicketDetail — so it is centred on
       the work, never under the fields rail. -->
  <div class={ticketEnabled && openTicketId ? "mx-auto grid w-full max-w-7xl gap-4 lg:grid-cols-[minmax(0,1fr)_300px]" : "contents"} data-testid="composer-column">
    <div class="min-w-0">
      <TicketComposer
        {base}
        {project}
        {providers}
        ticketId={activeTicketId}
        ticketLabel={activeTicketLabel}
        onClearTicket={() => { pendingTicketId = null; gotoTicket(null); }}
      />
    </div>
  </div>
</div>

<EmptyTicketOffer bind:this={emptyOffer} {base} onChanged={reloadBoard} />
