<script lang="ts">
  /* Team › Tickets (/team/tickets…): the tickets of the projects picked in Team
     settings › Tickets, combined. Three states, by route and selection:
       - nothing picked yet: the picker (every project the caller can open
         with ticket mode on) and Done, which saves ticket_projects;
       - /team/tickets: the combined board of the picked projects;
       - /team/tickets/<project>/<ticket>: that ticket's page.
     It is drawn at once, before the roster is in: the projects, the filter
     and every picked board are read in parallel, and the last answers
     (ticketBoardCache) are drawn while they are. With nothing cached, a
     loading splash stands in until the first board arrives.
     The header (title + ⚙) is AgentsApp's. */
  import { onMount, untrack } from "svelte";
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { runApi, saveTeamSettings, type TeamSettings } from "../../api/team.js";
  import { boardQuery, getProjectTickets, getTicketFilter, saveTicketFilter } from "../../api/tickets.js";
  import {
    boardGen,
    cachedBoard,
    cachedFilter,
    cachedProjectOptions,
    rememberBoard,
    rememberFilter,
    rememberProjectOptions,
  } from "../../ticketBoardCache.js";
  import type { TicketsRoute } from "../../agentsRouter.js";
  import type { ProjectOption, TicketBoard, TicketFilter, TicketSessionRow } from "../../types/agents.js";
  import { TEAM_FILTER_KEY, ticketProjectOptions, withProject } from "../../ticketProjects.js";
  import type { BoardProject } from "../../ticketBoardMerge.js";
  import { keepPushedOff, mergeRail } from "../../railPaging.js";
  import { connectSessionsStream } from "../../stores/sessionsStream.js";
  import KanbanBoard from "../KanbanBoard.svelte";
  import TicketDetail from "../TicketDetail.svelte";
  import EmptyTicketOffer from "../EmptyTicketOffer.svelte";
  import TicketComposer from "../TicketComposer.svelte";
  import { getProviderOptions } from "../../api/options.js";
  import type { ProviderOption } from "../../types/agents.js";

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
    /** The caller's Captain, named on the ticket page's composer. */
    captainName?: string;
  };
  let { base, selected, loadError: settingsError = "", onRetry, onProjects, route, onPicked, onRoute, onOpenSession, captainName = "" }: Props = $props();

  /* The last list read names the boards at once; the fresh one replaces it. */
  let projects = $state<ProjectOption[] | null>(cachedProjectOptions() ?? null);
  let loadError = $state("");
  onMount(async () => {
    if (projects) onProjects?.(projects);
    try {
      projects = await ticketProjectOptions(base);
      rememberProjectOptions(projects);
      onProjects?.(projects);
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  });
  const nameOf = (id: string) => projects?.find((p) => p.id === id)?.name ?? id;

  /* The ticket page's composer: the same one as Agents › Projects, with
     the project's providers. Read once a ticket page is open. */
  let providers = $state<ProviderOption[]>([]);
  let providersRead = false;
  $effect(() => {
    if (!(route.project && route.ticket) || providersRead) return;
    providersRead = true;
    Effect.runPromise(getProviderOptions(base).pipe(Effect.provide(WickClientLayer)))
      .then((p) => { providers = p ?? []; })
      .catch(() => { /* the composer falls back to the server default */ });
  });
  const ticketProject = $derived(
    projects?.find((p) => p.id === route.project) ?? (route.project ? ({ id: route.project, name: route.project } as ProjectOption) : null),
  );

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
  /* The filter read last time sends the boards' requests at once; the
     saved one, when it differs, sends them again. */
  const filterSeen = cachedFilter(TEAM_FILTER_KEY);
  let filter = $state<TicketFilter>(filterSeen ?? {});
  let filterLoaded = $state(filterSeen !== undefined);
  let untrackedOwner = $state<"me" | "all">("me");
  let trackedHere = $state(new Set<string>());
  const UNTRACKED_PAGE = 25;
  const UNTRACKED_MORE_PAGE = 50;

  onMount(() => {
    Effect.runPromise(getTicketFilter(base, TEAM_FILTER_KEY).pipe(Effect.provide(WickClientLayer)))
      .then((f) => {
        filter = f ?? {};
        rememberFilter(TEAM_FILTER_KEY, $state.snapshot(filter));
      })
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
     (two quick reloads, a filter click mid-load) is dropped on arrival.
     A board not drawn for this request yet is drawn from the cache first. */
  const loadSeq: Record<string, number> = {};
  function loadBoard(id: string) {
    const key = requestKey;
    const query = boardQuery(boardOptions);
    const gen = boardGen(id);
    const n = (loadSeq[id] = (loadSeq[id] ?? 0) + 1);
    const cur = slotOf(id);
    if (!cur.board || cur.key !== key) {
      const seen = cachedBoard(id, query);
      if (seen) slots[id] = { board: seen, key, more: [], moreNext: "" };
    }
    Effect.runPromise(getProjectTickets(base, id, boardOptions).pipe(Effect.provide(WickClientLayer)))
      .then((b) => {
        rememberBoard(id, query, b, gen);
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

  /* ── the loading splash ──
     Only while nothing is drawn yet: shown past SPLASH_DELAY_MS, "Still
     loading…" and Retry past SPLASH_SLOW_MS, and the reason with Retry
     once every picked project has failed. A cached board skips it. */
  const SPLASH_DELAY_MS = 150;
  const SPLASH_SLOW_MS = 8_000;
  let splashShown = $state(false);
  let splashSlow = $state(false);
  let splashTry = $state(0);
  const splashError = $derived.by(() => {
    const ids = selected ?? [];
    if (ids.length === 0 || !ids.every((id) => slots[id]?.error)) return "";
    return slots[ids[0]]?.error ?? "";
  });
  const splashPending = $derived(
    !(route.project && route.ticket) && !settingsError && (selected === null || (selected.length > 0 && boardProjects.length === 0)),
  );
  $effect(() => {
    if (!splashPending) {
      splashShown = false;
      splashSlow = false;
      return;
    }
    splashTry; // Retry starts the slow clock again
    splashSlow = false;
    const show = setTimeout(() => { splashShown = true; }, SPLASH_DELAY_MS);
    const slow = setTimeout(() => { splashSlow = true; }, SPLASH_SLOW_MS);
    return () => {
      clearTimeout(show);
      clearTimeout(slow);
    };
  });
  function retryAll() {
    splashTry++;
    for (const id of selected ?? []) {
      if (slots[id]?.error) slots[id] = { ...slotOf(id), error: undefined };
      loadBoard(id);
    }
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
    rememberFilter(TEAM_FILTER_KEY, $state.snapshot(filter));
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

  /* "+ New session" on a ticket page: the composer below already starts
     one on this ticket, so the button puts the cursor there. */
  function newSessionOn() {
    document.querySelector<HTMLTextAreaElement>('[data-testid="ticket-composer"] textarea')?.focus();
  }

  /* A ticket a move just emptied: offered for deletion as on a project's
     own board. */
  let emptyOffer = $state<ReturnType<typeof EmptyTicketOffer> | null>(null);

  const muted = "text-black-800 dark:text-black-600";
</script>

<div class="h-full overflow-y-auto" data-testid="tickets-page">
  {#if route.project && route.ticket}
    <!-- Ticket page. A link to a ticket opens it even before (or without)
         its project being picked; a ticket that cannot be read says so
         inside TicketDetail. -->
    <div class="flex min-h-full flex-col gap-4 p-4" data-testid="tickets-ticket" data-project={route.project} data-ticket={route.ticket}>
      {#key route.project + "/" + route.ticket}
        <TicketDetail
          {base}
          ticketId={route.ticket}
          onBack={() => onRoute({})}
          onOpenSession={(sid) => openSession(sid, route.project)}
          onNewSession={newSessionOn}
          projectId={route.project}
          backLabel="Tickets"
          projectName={nameOf(route.project ?? "")}
        />
        <!-- The same composer as Agents › Projects; here the new session
             answers as the caller's Captain. -->
        {#if ticketProject}
          <div class="mt-auto">
            <TicketComposer
              {base}
              project={ticketProject}
              {providers}
              ticketId={route.ticket ?? null}
              teamCaptain
              {captainName}
              onCreated={(sid) => openSession(sid, route.project)}
            />
          </div>
        {/if}
      {/key}
    </div>
  {:else if settingsError}
    <div class="p-4 text-sm" data-testid="tickets-load-error">
      <span class="text-neg-400">Couldn't load your Tickets settings: {settingsError}</span>
      <button type="button" class="ml-2 font-medium text-green-600 hover:underline" onclick={() => onRetry?.()}>Retry</button>
    </div>
  {:else if selected === null}
    {@render splash()}
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
      {#each boardProjects.length > 0 ? selected : [] as id (id)}
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
      {#if boardProjects.length === 0}
        {@render splash()}
      {:else}
        <div class="tickets-fade-in min-h-0 flex-1">
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

{#snippet splash()}
  <!-- The first load with nothing cached: shown only past SPLASH_DELAY_MS
       so a quick answer never flashes it. -->
  <div class="flex h-full min-h-[240px] items-center justify-center p-6" data-testid="tickets-splash" aria-busy={splashError ? undefined : "true"}>
    {#if splashError}
      <div class="tickets-fade-in max-w-[420px] text-center" data-testid="tickets-splash-error">
        <p class="text-sm font-medium text-neg-400">Couldn't load tickets</p>
        <p class="mt-1 text-xs {muted}">{splashError}</p>
        <button type="button" class="mt-3 rounded-lg bg-green-500 px-3 py-1.5 text-xs font-semibold text-white-100 hover:bg-green-600" onclick={retryAll}>Retry</button>
      </div>
    {:else if splashShown}
      <div class="tickets-fade-in max-w-[420px] text-center" role="status">
        <svg class="mx-auto h-6 w-6 animate-spin text-green-500 motion-reduce:animate-none" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
          <path d="M8 2a6 6 0 016 6" stroke-linecap="round"></path>
        </svg>
        <p class="mt-3 text-sm font-medium text-black-900 dark:text-white-100">{splashSlow ? "Still loading…" : "Loading tickets…"}</p>
        {#if selected?.length}
          <p class="mt-1 truncate text-xs {muted}" data-testid="tickets-splash-projects">{selected.map((id) => "📁 " + nameOf(id)).join(" · ")}</p>
        {/if}
        {#if splashSlow}
          <button type="button" class="mt-3 rounded-lg border border-white-300 px-3 py-1.5 text-xs font-medium text-black-900 hover:bg-white-200 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-700" onclick={retryAll}>Retry</button>
        {/if}
      </div>
    {/if}
  </div>
{/snippet}

<style>
  .tickets-fade-in {
    animation: tickets-fade-in 180ms ease-out;
  }
  @keyframes tickets-fade-in {
    from {
      opacity: 0;
    }
    to {
      opacity: 1;
    }
  }
  @media (prefers-reduced-motion: reduce) {
    .tickets-fade-in {
      animation: none;
    }
  }
</style>

<EmptyTicketOffer bind:this={emptyOffer} {base} onChanged={reload} />
