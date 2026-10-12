<script lang="ts">
  /* Team › Tickets (/team/t…): the tickets of the projects picked in Team
     settings › Tickets, combined. Three states, by route and selection:
       - nothing picked yet: the picker (every project the caller can open
         with ticket mode on) and Done, which saves ticket_projects;
       - /team/t: the combined board of the picked projects;
       - /team/t/<project>/<ticket>: that ticket's page.
     The header (title + ⚙) is AgentsApp's. */
  import { onMount, untrack } from "svelte";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { runApi, saveTeamSettings, type TeamSettings } from "../../api/team.js";
  import { getProjectTickets, getTicketFilter, saveTicketFilter } from "../../api/tickets.js";
  import type { TicketsRoute } from "../../agentsRouter.js";
  import type { ProjectOption, TicketBoard, TicketFilter, TicketSessionRow } from "../../types/agents.js";
  import { TEAM_FILTER_KEY, ticketProjectOptions, withProject } from "../../ticketProjects.js";
  import type { BoardProject } from "../../ticketBoardMerge.js";
  import { keepPushedOff, mergeRail } from "../../railPaging.js";
  import { connectSessionsStream } from "../../stores/sessionsStream.js";
  import KanbanBoard from "../KanbanBoard.svelte";
  import TicketDetail from "../TicketDetail.svelte";
  import EmptyTicketOffer from "../EmptyTicketOffer.svelte";
  import TicketRoom from "./TicketRoom.svelte";

  type Props = {
    base: string;
    /** The saved selection; null while Team settings are still loading. */
    selected: string[] | null;
    /** Team settings could not be read: no picker (Done would replace a
        selection we could not see), the reason and Retry instead. */
    loadError?: string;
    onRetry?: () => void;
    /** The pickable projects once read (the header names them). */
    onProjects?: (p: ProjectOption[]) => void;
    route: TicketsRoute;
    /** Done saved the selection; the confirmed settings. */
    onPicked: (s: TeamSettings) => void;
    /** Move within Tickets ({} = the board). */
    onRoute: (r: TicketsRoute) => void;
    /** A chat row (board or ticket page) was clicked. Absent = open it in
        Agents › Projects. */
    onOpenSession?: (sessionId: string, projectId?: string) => void;
  };
  let { base, selected, loadError: settingsError = "", onRetry, onProjects, route, onPicked, onRoute, onOpenSession }: Props = $props();

  let projects = $state<ProjectOption[] | null>(null);
  let loadError = $state("");
  onMount(async () => {
    try {
      projects = await ticketProjectOptions(base);
      onProjects?.(projects);
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  });
  const nameOf = (id: string) => projects?.find((p) => p.id === id)?.name ?? id;

  /* Picker */
  let draft = $state<string[]>([]);
  let saving = $state(false);
  let saveError = $state("");
  async function done() {
    if (!draft.length || saving) return;
    saving = true;
    saveError = "";
    try {
      onPicked(await runApi(saveTeamSettings(base, { ticket_projects: $state.snapshot(draft) })));
    } catch (e) {
      saveError = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }

  /* ── the combined board ──
     One request per picked project, all at once; each board is drawn as it
     arrives, and a project that fails says so on its own row instead of
     leaving a gap nobody can explain. */
  type Slot = {
    board?: TicketBoard;
    error?: string;
    /* Rail pages loaded on "load more", and where they end ("" = the
       first page's own cursor continues the rail). */
    more: TicketSessionRow[];
    moreNext: string;
    /* The request `board` answered, so a reload can tell "the same list
       moved" from "a different list". */
    key?: string;
  };
  let slots = $state<Record<string, Slot>>({});
  const slotOf = (id: string): Slot => slots[id] ?? { more: [], moreNext: "" };

  /* The combined filter, saved under its own key so a project's own
     board filter is never touched. */
  let filter = $state<TicketFilter>({});
  let filterLoaded = $state(false);
  let untrackedOwner = $state<"me" | "all">("me");
  let trackedHere = $state(new Set<string>());
  const UNTRACKED_PAGE = 25;
  const UNTRACKED_MORE_PAGE = 50;

  onMount(() => {
    Effect.runPromise(getTicketFilter(base, TEAM_FILTER_KEY).pipe(Effect.provide(WickClientLayer)))
      .then((f) => { filter = f ?? {}; })
      .catch(() => { /* the defaults are a fine board */ })
      .finally(() => { filterLoaded = true; });
  });

  /* Same request as a project board makes (ProjectLanding), sent to each. */
  const boardOptions = $derived.by(() => {
    const saved = filter.statuses;
    const statuses = !saved || saved.length === 0 ? undefined : saved[0] === "\u0000none" ? [] : saved;
    return {
      rows: 3,
      statuses,
      assignee: filter.assignee || undefined,
      untracked: filter.show_untracked === true,
      untrackedLimit: UNTRACKED_PAGE,
      untrackedOwner,
    };
  });
  const showBoard = $derived(!(route.project && route.ticket) && !settingsError && !!selected?.length);
  const requestKey = $derived((selected ?? []).join(",") + "|" + JSON.stringify(boardOptions));

  $effect(() => {
    if (!filterLoaded || !showBoard) return;
    requestKey; // a different request is a different list
    untrack(() => {
      /* Each board already drawn stays until its replacement arrives (as
         on a project's own board), so a filter click never empties the
         board or remounts it — the project select, a running board
         button's panel and a half-typed ticket survive. Only the rail
         pages of the old request go: they no longer continue the list. */
      const keep = new Set(selected ?? []);
      const next: Record<string, Slot> = {};
      for (const [id, s] of Object.entries(slots)) {
        if (keep.has(id) && s.board) next[id] = { board: s.board, key: s.key, more: [], moreNext: "" };
      }
      slots = next;
      trackedHere = new Set();
      for (const id of selected ?? []) loadBoard(id);
    });
  });

  /* Per project, the newest load wins: a slow answer to an older request
     (two quick reloads, a filter click mid-load) is dropped on arrival. */
  const loadSeq: Record<string, number> = {};
  function loadBoard(id: string) {
    const key = requestKey;
    const n = (loadSeq[id] = (loadSeq[id] ?? 0) + 1);
    Effect.runPromise(getProjectTickets(base, id, boardOptions).pipe(Effect.provide(WickClientLayer)))
      .then((b) => {
        if (key !== requestKey || n !== loadSeq[id]) return; // superseded
        const cur = slotOf(id);
        // Same list, moved: a chat pushed off page one stays on the rail.
        let more = cur.more;
        if (cur.board && cur.key === key) more = keepPushedOff(cur.board.untracked, b.untracked, cur.more, trackedHere, UNTRACKED_PAGE);
        slots[id] = { ...cur, board: b, key, error: undefined, more, moreNext: more.length > 0 ? cur.moreNext : "" };
      })
      .catch((e: unknown) => {
        if (key !== requestKey || n !== loadSeq[id]) return;
        // A board already drawn stays on a transient failure.
        if (slotOf(id).board) return;
        slots[id] = { ...slotOf(id), error: e instanceof Error ? e.message : String(e) };
      });
  }

  /* One more page of one project's rail. */
  let loadingMore = new Set<string>();
  function loadMoreUntracked(id?: string) {
    if (!id || loadingMore.has(id)) return;
    const slot = slotOf(id);
    const after = slot.more.length > 0 ? slot.moreNext : slot.board?.untracked_next;
    if (!after) return;
    loadingMore.add(id);
    const key = requestKey;
    Effect.runPromise(
      getProjectTickets(base, id, {
        rows: 0,
        statuses: [], // rows only — no cards
        untracked: true,
        untrackedLimit: UNTRACKED_MORE_PAGE,
        untrackedAfter: after,
        untrackedOwner,
      }).pipe(Effect.provide(WickClientLayer)),
    )
      .then((b) => {
        if (key !== requestKey) return;
        const cur = slotOf(id);
        const have = new Set([...(cur.board?.untracked ?? []), ...cur.more].map((r) => r.id));
        slots[id] = { ...cur, more: [...cur.more, ...b.untracked.filter((r) => !have.has(r.id))], moreNext: b.untracked_next ?? "" };
      })
      .catch(() => { /* offered again on the next click */ })
      .finally(() => { loadingMore.delete(id); });
  }

  /* The boards handed to KanbanBoard: the arrived ones, in picked order,
     each rail with its loaded pages. */
  const boardProjects = $derived.by((): BoardProject[] => {
    const out: BoardProject[] = [];
    for (const id of selected ?? []) {
      const s = slots[id];
      if (!s?.board) continue;
      const board =
        s.more.length === 0
          ? s.board
          : { ...s.board, untracked: mergeRail(s.board.untracked, s.more, trackedHere), untracked_next: s.moreNext || undefined };
      out.push({ id, name: nameOf(id), board });
    }
    return out;
  });

  function sessionTracked(id: string) {
    trackedHere = new Set(trackedHere).add(id);
    for (const [pid, s] of Object.entries(slots)) {
      if (s.more.some((r) => r.id === id)) slots[pid] = { ...s, more: s.more.filter((r) => r.id !== id) };
    }
  }

  function reload(id?: string) {
    if (id) loadBoard(id);
    else for (const p of selected ?? []) loadBoard(p);
  }

  /* ── keeping the board current ──
     The same mechanism as a project's own board (ProjectLanding): a
     `ticket` signal on /stream/sessions for one of the picked projects
     refetches that project; polling every 60 s is the fallback while the
     stream is down, and a reconnect refetches all of them once. A hidden
     tab or a drag in progress only marks the project stale; it is fetched
     on return / on drop. */
  const BOARD_POLL_MS = 60_000;
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

  const selectedKey = $derived((selected ?? []).join(","));
  $effect(() => {
    if (!filterLoaded || !showBoard) return;
    const picked = new Set(selectedKey.split(","));
    let streamUp = false;
    let dropped = false;
    const stale = new Set<string>();
    const timers = new Map<string, ReturnType<typeof setTimeout>>();
    const due = (pid: string) => {
      if (dragging || (typeof document !== "undefined" && document.hidden)) {
        stale.add(pid);
        return;
      }
      stale.delete(pid);
      loadBoard(pid);
    };
    /* A burst (a sync touching many tickets) costs one fetch per project. */
    const soon = (pid: string) => {
      const t = timers.get(pid);
      if (t !== undefined) clearTimeout(t);
      timers.set(pid, setTimeout(() => { timers.delete(pid); due(pid); }, 300));
    };
    const all = (f: (pid: string) => void) => { for (const pid of picked) f(pid); };
    const leave = connectSessionsStream(base, {
      onTicket: (t) => { if (picked.has(t.project_id)) soon(t.project_id); },
      onStatus: (st) => {
        streamUp = st === "connected";
        if (!streamUp) dropped = true;
        else if (dropped) { dropped = false; all(soon); }
      },
    });
    const id = setInterval(() => { if (!streamUp) all(due); }, BOARD_POLL_MS);
    const onVisible = () => {
      if (document.hidden) return;
      if (!streamUp) all(due);
      else for (const pid of [...stale]) due(pid);
    };
    document.addEventListener("visibilitychange", onVisible);
    const onDragEnd = () => { for (const pid of [...stale]) soon(pid); };
    window.addEventListener("dragend", onDragEnd);
    window.addEventListener("drop", onDragEnd);
    return () => {
      leave();
      for (const t of timers.values()) clearTimeout(t);
      clearInterval(id);
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("dragend", onDragEnd);
      window.removeEventListener("drop", onDragEnd);
    };
  });

  let filterSaveTimer: ReturnType<typeof setTimeout> | undefined;
  function applyFilter(f: TicketFilter) {
    filter = f;
    clearTimeout(filterSaveTimer);
    filterSaveTimer = setTimeout(() => {
      Effect.runPromise(
        saveTicketFilter(base, TEAM_FILTER_KEY, $state.snapshot(filter)).pipe(Effect.provide(WickClientLayer)),
      ).catch(() => { /* saving the preference is best-effort */ });
    }, 400);
  }

  function openSession(id: string, projectId?: string) {
    if (onOpenSession) onOpenSession(id, projectId);
    else window.location.href = `${base}/sessions/${encodeURIComponent(id)}`;
  }

  /* "+ New session" on a ticket page: the chat that joins a ticket is
     started from the project's own ticket page (its composer), until the
     room gives this page a composer of its own. */
  function newSessionOn(project: string, ticketId: string) {
    window.location.href = `${base}/sessions?project=${encodeURIComponent(project)}&ticket=${encodeURIComponent(ticketId)}`;
  }

  /* A ticket a move just emptied: offered for deletion as on a project's
     own board. */
  let emptyOffer = $state<ReturnType<typeof EmptyTicketOffer> | null>(null);

  /* The room's own session, kept out of the ticket's Sessions list: the
     room block above it is where that conversation is read. */
  let roomSession = $state("");

  const muted = "text-black-800 dark:text-black-600";
</script>

<div class="h-full overflow-y-auto" data-testid="tickets-page">
  {#if route.project && route.ticket}
    <!-- Ticket page. A link to a ticket opens it even before (or without)
         its project being picked; a ticket that cannot be read says so
         inside TicketDetail. -->
    <div class="p-4" data-testid="tickets-ticket" data-project={route.project} data-ticket={route.ticket}>
      {#key route.project + "/" + route.ticket}
        <TicketDetail
          {base}
          ticketId={route.ticket}
          onBack={() => onRoute({})}
          onOpenSession={(sid) => openSession(sid, route.project)}
          onNewSession={(tid) => newSessionOn(route.project ?? "", tid)}
          hideSession={roomSession}
        >
          {#snippet room()}
            <TicketRoom {base} ticketId={route.ticket ?? ""} projectName={nameOf(route.project ?? "")} onSession={(sid) => (roomSession = sid)} />
          {/snippet}
        </TicketDetail>
      {/key}
    </div>
  {:else if settingsError}
    <div class="p-4 text-sm" data-testid="tickets-load-error">
      <span class="text-neg-400">Couldn't load your Tickets settings: {settingsError}</span>
      <button type="button" class="ml-2 font-medium text-green-600 hover:underline" onclick={() => onRetry?.()}>Retry</button>
    </div>
  {:else if selected === null}
    <p class="p-4 text-sm {muted}">Loading…</p>
  {:else if selected.length === 0}
    <div class="mx-auto my-10 max-w-[480px] px-6 text-center" data-testid="tickets-picker">
      <div class="text-2xl" aria-hidden="true">🎫</div>
      <h2 class="mt-1 text-[15px] font-semibold text-black-900 dark:text-white-100">Select projects first</h2>
      <p class="mt-1 text-sm {muted}">
        Their tickets are combined on one board. Board buttons like Sync run on the selected projects only.
        Change it later in Team settings › Tickets.
      </p>
      {#if loadError}
        <p class="mt-4 text-sm text-neg-400">{loadError}</p>
      {:else if !projects}
        <p class="mt-4 text-sm {muted}">Loading projects…</p>
      {:else if projects.length === 0}
        <p class="mt-4 text-sm {muted}" data-testid="tickets-picker-empty">None of the projects you can open has ticket mode on.</p>
      {:else}
        <div class="my-3 flex flex-wrap justify-center gap-1.5" role="group" aria-label="Projects">
          {#each projects as p (p.id)}
            {@const on = draft.includes(p.id)}
            <label
              class="flex cursor-pointer items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-medium {on
                ? 'border-green-500 bg-green-50 text-green-700 dark:bg-navy-600 dark:text-green-400'
                : 'border-white-300 text-black-900 hover:bg-white-200 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-700'}"
            >
              <input type="checkbox" class="accent-green-500" checked={on} onchange={(e) => (draft = withProject(draft, p.id, e.currentTarget.checked))} />
              📁 {p.name}
            </label>
          {/each}
        </div>
        <button
          type="button"
          class="rounded-lg bg-green-500 px-4 py-2 text-sm font-semibold text-white-100 hover:bg-green-600 disabled:cursor-not-allowed disabled:opacity-50"
          disabled={!draft.length || saving}
          onclick={done}
        >{saving ? "Saving…" : draft.length ? `Done · ${draft.length} selected` : "Done"}</button>
        {#if saveError}<p class="mt-2 text-sm text-neg-400">{saveError}</p>{/if}
      {/if}
      <p class="mt-3 text-xs {muted}">Only projects you can already open. Your setting; nothing changes in the projects.</p>
    </div>
  {:else}
    <div class="flex h-full min-h-0 flex-col gap-2 p-4" data-testid="tickets-board">
      <!-- A row per project still on its way or refused; the board below
           draws whatever has arrived. -->
      {#each selected as id (id)}
        {@const s = slots[id]}
        {#if s?.error}
          <div class="flex items-center gap-2 rounded-lg border border-neg-100 px-3 py-2 text-xs" data-testid={"tickets-board-error-" + id}>
            <span class="min-w-0 truncate text-neg-400">📁 {nameOf(id)}: couldn't load its tickets — {s.error}</span>
            <button type="button" class="ml-auto shrink-0 font-medium text-green-600 hover:underline" onclick={() => loadBoard(id)}>Retry</button>
          </div>
        {:else if !s?.board}
          <p class="text-xs {muted}" data-testid={"tickets-board-loading-" + id}>Loading 📁 {nameOf(id)}…</p>
        {/if}
      {/each}
      {#if boardProjects.length > 0}
        <div class="min-h-0 flex-1">
          <KanbanBoard
            {base}
            projects={boardProjects}
            {filter}
            {untrackedOwner}
            onUntrackedOwner={(v) => { untrackedOwner = v; }}
            onUntrackedMore={loadMoreUntracked}
            onSessionTracked={sessionTracked}
            onFilter={applyFilter}
            onOpen={(ticket, project) => { if (project) onRoute({ project, ticket }); }}
            onOpenSession={openSession}
            onReload={reload}
            onEmptied={(t, pid) => emptyOffer?.offer(t, pid)}
          />
        </div>
      {/if}
    </div>
  {/if}
</div>

<EmptyTicketOffer bind:this={emptyOffer} {base} onChanged={reload} />
