<script lang="ts">
  /* ONE project's memory, opened from its "⋯" menu (PLAN §22).

     This is not the global panel with a filter on it. The global tabs answer
     store-wide questions — every project's analytics, the daemon, the
     settings — and showing them here would invite reading a store-wide
     number as this project's, which is the confusion §13.5 opens with. So
     the tab strip is gone and the page answers exactly two questions: what
     does this project remember, and can I fix it.

     The scope is never derived here. It arrives resolved from the server
     (/agentmemory/project-scope), because the mapping from a wick project to
     a memory bucket lives next to the marker writer that pins it — two
     sources of truth for "which bucket" is the failure §22.2 exists to
     prevent. */
  import { Button, ConfirmDialog, Select, TextInput } from "@wick-fe/common-ui";
  import Section from "./Section.svelte";
  import BlockedState from "./BlockedState.svelte";
  import PageEditor from "./PageEditor.svelte";
  import { blockedBy, MANAGE_ADMIN_ONLY } from "./format.js";
  import {
    backfillCapWarning,
    backfillConfirmBody,
    backfillSummary,
    BRIEFING_UNAVAILABLE,
    scopeCaveat,
    scopeOrigin,
  } from "./projects.js";
  import {
    cardAge,
    cardsFrom,
    cardsFromHits,
    counterRow,
    freshness,
    kindMix,
    linkRow,
    paceOf,
    pathError,
    scopeHeading,
    snippetOf,
    timelineCaveat,
    writeTimeline,
  } from "./projectview.js";
  import type { PageCard } from "./projectview.js";
  import type {
    BackfillReport,
    BackfillRequestEcho,
    Checkpoint,
    Page,
    ProjectBriefing,
    ProjectPolicy,
    ProjectScope,
    ProjectsResponse,
    SearchResponse,
  } from "./types.js";

  type Props = {
    scope: ProjectScope | null;
    scopeError: string;
    projects: ProjectsResponse | null;
    briefing: ProjectBriefing | null;
    loading: boolean;
    busy: boolean;
    canManage: boolean;
    /* backendName names the store this project's memory lives in. Shown
       always, not only when there is a choice of backend: "some store,
       somewhere" is not an answer, and more backends are coming. */
    backendName?: string;
    /* policy is whether this project uses Agent Memory at all — the switch
       that makes trying it on ONE project possible. Null while it loads, or
       on a host where nothing wired it. */
    policy?: ProjectPolicy | null;
    policyBusy?: boolean;
    onPolicy?: (value: ProjectPolicy["value"]) => void;
    /* Importing the history that predates capture. It is the answer to an
       empty project: the sessions are still on disk, they were simply never
       captured. Preview writes nothing; Import does, which is why it is
       confirmed and admin-only. Optional so a surface that does not offer
       the import renders without it. */
    backfill?: BackfillReport | null;
    backfillReq?: BackfillRequestEcho | null;
    backfillError?: string;
    onPreviewBackfill?: () => void;
    onRunBackfill?: () => void;
    /* The open page and its editor state, owned by App so a re-render never
       drops a draft. */
    openPath: string;
    page: Page | null;
    pageLoading: boolean;
    pageError: string;
    draft: string;
    draftTitle: string;
    draftKind: string;
    committedNote: string;
    saveMsg: string;
    saveFailed: boolean;
    checkpoints: Checkpoint[] | null;
    checkpointsLoading: boolean;
    search: SearchResponse | null;
    searching: boolean;
    query: string;
    onQuery: (q: string) => void;
    onSearch: () => void;
    onOpen: (path: string) => void;
    onClose: () => void;
    onDraft: (v: string) => void;
    onTitle: (v: string) => void;
    onKind: (v: string) => void;
    onSave: () => void;
    onDelete: () => void;
    onNewPage: (path: string) => void;
    onLoadCheckpoints: () => void;
    onRestore: (oid: string) => void;
    onRefresh: () => void;
    onGoGlobal: () => void;
  };
  let {
    scope,
    scopeError,
    projects,
    briefing,
    loading,
    busy,
    canManage,
    backendName = "",
    policy = null,
    policyBusy = false,
    onPolicy,
    backfill = null,
    backfillReq = null,
    backfillError = "",
    onPreviewBackfill,
    onRunBackfill,
    openPath,
    page,
    pageLoading,
    pageError,
    draft,
    draftTitle,
    draftKind,
    committedNote,
    saveMsg,
    saveFailed,
    checkpoints,
    checkpointsLoading,
    search,
    searching,
    query,
    onQuery,
    onSearch,
    onOpen,
    onClose,
    onDraft,
    onTitle,
    onKind,
    onSave,
    onDelete,
    onNewPage,
    onLoadCheckpoints,
    onRestore,
    onRefresh,
    onGoGlobal,
  }: Props = $props();

  let newPath = $state("");
  let newPathTouched = $state(false);
  let confirmImport = $state(false);

  // canImport is false on a surface that did not wire the import — the
  // section is then absent rather than present and dead.
  const canImport = $derived(Boolean(onPreviewBackfill && onRunBackfill));
  const capWarning = $derived(backfillCapWarning(backfill ?? undefined, backfillReq));
  const importScope = $derived(scope ? `${scope.workspace}/${scope.project}` : "");

  const heading = $derived(scopeHeading(scope));
  const caveat = $derived(scope ? scopeCaveat(scope.source) : null);
  const blocked = $derived(blockedBy(projects));
  const counters = $derived(counterRow(briefing));
  // Analytics that are genuinely about THIS project: its own pages over
  // time, its own pace, what its memory is made of, and how stale it is.
  // Nothing store-wide is borrowed (PLAN §13.5).
  const timeline = $derived(writeTimeline(briefing?.recent_pages, 30));
  const timelineNote = $derived(timelineCaveat(briefing?.recent_pages));
  const timelinePeak = $derived(Math.max(1, ...timeline.map((d) => d.count)));
  const timelineTotal = $derived(timeline.reduce((n, d) => n + d.count, 0));
  const pace = $derived(paceOf(briefing));
  const mix = $derived(kindMix(briefing?.recent_pages));
  const fresh = $derived(freshness(briefing));
  const links = $derived(linkRow(briefing));
  const cards = $derived(cardsFrom(briefing?.recent_pages, search?.hits));
  const extraCards = $derived(cardsFromHits(search?.hits, cards));
  const newPathProblem = $derived(newPathTouched ? pathError(newPath) : null);
  // A page the store has never held is still an empty project, not a broken
  // one: the difference decides whether the empty state teaches or apologises.
  const empty = $derived(!loading && cards.length === 0 && extraCards.length === 0);

  function confirmAndImport(): void {
    confirmImport = false;
    onRunBackfill?.();
  }

  function addPage(): void {
    newPathTouched = true;
    if (pathError(newPath)) return;
    onNewPage(newPath.trim());
    newPath = "";
    newPathTouched = false;
  }

  // The snippet on an opened card comes from the body that was actually
  // read — nothing here invents one from the title.
  function snippetFor(c: PageCard): string {
    if (c.path === openPath && page?.body) return snippetOf(page.body);
    return c.snippet ?? "";
  }
</script>

<div class="mx-auto w-full max-w-5xl space-y-5 px-4 py-5 sm:px-6 sm:py-6">
  <!-- Header: the project clicked, and the bucket agents actually write to -->
  <div class="flex flex-wrap items-start justify-between gap-3">
    <div class="min-w-0">
      <h2 class="truncate text-base font-semibold text-black-900 dark:text-white-100" data-testid="project-title">
        {heading.title}
      </h2>
      {#if scope}
        <p class="mt-0.5 text-xs text-black-800 dark:text-black-600">
          <span class="font-mono text-black-900 dark:text-white-100">{heading.bucket}</span>
          — {scopeOrigin(scope.source)}{#if backendName}, in
            <span class="font-medium text-black-900 dark:text-white-100">{backendName}</span>{/if}.
        </p>
      {/if}
    </div>
    <div class="flex flex-shrink-0 flex-wrap items-center gap-2">
      <Button variant="secondary" size="sm" disabled={loading} onclick={onRefresh}>Refresh</Button>
      <Button variant="ghost" size="sm" onclick={onGoGlobal}>All projects</Button>
    </div>
  </div>

  <!-- Whether this project uses Agent Memory at all. First, because every
       number below it is worth nothing if the answer is "no" — and because
       this is the switch that makes trying the feature on one project
       possible without turning it on everywhere. -->
  {#if policy}
    <section
      class="rounded-xl border border-white-300 bg-white-100 px-5 py-4 dark:border-navy-600 dark:bg-navy-700"
      data-testid="policy-card"
    >
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div class="min-w-0">
          <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">This project</p>
          <p
            class={`mt-0.5 text-sm font-medium ${policy.allowed ? "text-green-600 dark:text-green-300" : "text-cau-600 dark:text-cau-400"}`}
            data-testid="policy-state"
          >
            {policy.allowed ? "Recording and recalling" : "Not recording or recalling"}
          </p>
        </div>
        {#if canManage && onPolicy}
          <div class="w-full shrink-0 sm:w-64">
            <Select
              value={policy.value}
              disabled={policyBusy}
              onChange={(v) => onPolicy?.(v as ProjectPolicy["value"])}
              options={[
                { label: "Follow the agent's setting", value: "" },
                { label: "On — include this project", value: "on" },
                { label: "Off — keep memory out", value: "off" },
              ]}
            />
          </div>
        {/if}
      </div>
      <p class="mt-2 text-xs leading-relaxed text-black-800 dark:text-black-600" data-testid="policy-reason">
        {policy.reason}
      </p>
      {#if policy.trial_mode}
        <!-- The clause people trip over, said where it bites: opting one
             project in is what makes the others go quiet. -->
        <p class="mt-1 text-[0.6875rem] leading-relaxed text-black-700 dark:text-black-600">
          While any project is switched on, projects left on “Follow the agent's setting” do not record or recall.
          That is what makes a one-project trial possible.
        </p>
      {:else if canManage}
        <!-- Said BEFORE the click, not after. Nobody is switching this on to
             start a host-wide trial; they are switching it on to try one
             project. The consequence has to be on screen while the decision
             is still being made, or the first person to press it silently
             stops capture everywhere else. -->
        <p
          class="mt-1 text-[0.6875rem] leading-relaxed text-cau-600 dark:text-cau-400"
          data-testid="policy-trial-warning"
        >
          Switching this project on starts a trial: every project still on “Follow the agent's setting” stops
          recording and recalling until this one is set back. Nothing already stored is lost.
        </p>
      {/if}
      {#if !canManage}
        <p class="mt-2 text-xs leading-relaxed text-black-700 dark:text-black-600">{MANAGE_ADMIN_ONLY}</p>
      {/if}
    </section>
  {/if}

  {#if caveat}
    <p class="text-xs leading-relaxed text-cau-600 dark:text-cau-400" data-testid="scope-caveat">{caveat}</p>
  {/if}

  {#if scopeError}
    <div class="rounded-xl border border-rose-300 bg-rose-100 px-5 py-3 dark:border-rose-700 dark:bg-navy-800">
      <p class="text-sm font-medium text-rose-700 dark:text-rose-300">This project's memory could not be located</p>
      <p class="mt-1 text-xs leading-relaxed text-black-800 dark:text-black-600">{scopeError}</p>
    </div>
  {:else if blocked}
    <BlockedState {blocked} onAction={onGoGlobal} />
  {:else}
    <!-- The project's own numbers. Store-wide totals with the same names are
         on the global Analytics tab and are never mixed in here. -->
    {#if counters.length}
      <dl class="grid grid-cols-2 gap-x-6 gap-y-3 rounded-xl border border-white-300 bg-white-100 px-5 py-4 sm:grid-cols-5 dark:border-navy-600 dark:bg-navy-700">
        {#each counters as c (c.label)}
          <div class="min-w-0">
            <dt class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{c.label}</dt>
            <dd class="mt-0.5 truncate text-sm tabular-nums text-black-900 dark:text-white-100">{c.value}</dd>
          </div>
        {/each}
      </dl>
    {/if}

    <!-- Activity over time. One bar a day, the project's own page writes —
         a shape four static numbers cannot show: whether its memory is
         still being written to, or stopped a fortnight ago. -->
    {#if briefing}
      <Section title="Activity" scope="this project only" note="Page writes per day over the last 30 days, from this project's own pages.">
        <div class="px-5 py-4">
          <div class="flex items-baseline justify-between gap-3">
            <p class="text-sm text-black-900 dark:text-white-100">
              {timelineTotal} page write{timelineTotal === 1 ? "" : "s"} in 30 days
            </p>
            <p class={`text-xs font-medium ${pace.ratio >= 1.25 ? "text-green-600 dark:text-green-300" : pace.ratio === 0 ? "text-cau-600 dark:text-cau-400" : "text-black-800 dark:text-black-600"}`}>
              {pace.label}
            </p>
          </div>

          <!-- 30 bars, one accent colour, an empty day drawn as a floor so
               the gap is visible rather than missing. -->
          <div class="mt-3 flex h-16 items-end gap-0.5" role="img" aria-label={`Page writes per day: ${timelineTotal} in the last 30 days`}>
            {#each timeline as d (d.date)}
              <div
                class={`flex-1 rounded-sm ${d.count > 0 ? "bg-green-500 dark:bg-green-400" : "bg-white-300 dark:bg-navy-600"}`}
                style={`height: ${d.count > 0 ? Math.max(8, Math.round((d.count / timelinePeak) * 100)) : 4}%`}
                title={`${d.date}: ${d.count} page write${d.count === 1 ? "" : "s"}`}
              ></div>
            {/each}
          </div>
          <div class="mt-1 flex justify-between text-[0.6875rem] text-black-700 dark:text-black-600">
            <span>{timeline[0]?.date ?? ""}</span>
            <span>today</span>
          </div>

          <p class="mt-2 text-xs leading-relaxed text-black-800 dark:text-black-600">{pace.detail}</p>
          {#if timelineNote}
            <p class="mt-1 text-[0.6875rem] leading-relaxed text-black-700 dark:text-black-600" data-testid="timeline-caveat">
              {timelineNote}
            </p>
          {/if}
        </div>

        <!-- Freshness and composition: how long since anything was written,
             and what this project's memory is actually made of. -->
        <div class="grid gap-4 border-t border-white-300 px-5 py-4 sm:grid-cols-2 dark:border-navy-600">
          <div class="min-w-0">
            <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">Last captured</p>
            <p
              class={`mt-0.5 text-sm ${fresh.stale ? "text-cau-600 dark:text-cau-400" : "text-black-900 dark:text-white-100"}`}
              data-testid="freshness"
            >
              {fresh.label}
            </p>
            <p class="mt-1 text-xs leading-relaxed text-black-700 dark:text-black-600">{fresh.detail}</p>
          </div>
          <div class="min-w-0">
            <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">What it is made of</p>
            {#if mix.length === 0}
              <p class="mt-1 text-xs leading-relaxed text-black-700 dark:text-black-600">
                No pages to break down yet.
              </p>
            {:else}
              <ul class="mt-1.5 space-y-1" data-testid="kind-mix">
                {#each mix as k (k.kind)}
                  <li class="flex items-center gap-2 text-xs">
                    <span class="w-24 shrink-0 truncate text-black-900 dark:text-white-100">{k.kind}</span>
                    <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-white-300 dark:bg-navy-600">
                      <span class="block h-full rounded-full bg-green-500 dark:bg-green-400" style={`width: ${Math.round(k.share * 100)}%`}></span>
                    </span>
                    <span class="w-8 shrink-0 text-right tabular-nums text-black-700 dark:text-black-600">{k.count}</span>
                  </li>
                {/each}
              </ul>
            {/if}
          </div>
        </div>

        <!-- Where this project sits among the others. Zero is the usual
             answer and worth stating: it is what says this memory stands on
             its own rather than leaning on another client's. -->
        {#if links.length}
          <dl class="grid grid-cols-2 gap-x-6 gap-y-3 border-t border-white-300 px-5 py-4 sm:grid-cols-4 dark:border-navy-600">
            {#each links as l (l.label)}
              <div class="min-w-0">
                <dt class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{l.label}</dt>
                <dd class="mt-0.5 text-sm tabular-nums text-black-900 dark:text-white-100" title={l.note ?? ""}>{l.value}</dd>
              </div>
            {/each}
          </dl>
        {/if}
      </Section>
    {:else if !loading}
      <!-- No briefing for this project: say which numbers are missing and
           why, rather than drawing a chart of zeros (PLAN §13.5 point 4). -->
      <Section title="Activity" scope="this project only">
        <p class="px-5 py-4 text-xs leading-relaxed text-black-700 dark:text-black-600" data-testid="no-briefing">
          {BRIEFING_UNAVAILABLE}
        </p>
      </Section>
    {/if}

    <Section
      title="Pages"
      scope="this project only"
      note="What agents wrote down here. Open a page to read it in full; the search box also finds pages older than this list."
    >
      {#snippet actions()}
        <div class="flex flex-wrap items-center gap-2">
          <div class="w-48">
            <TextInput value={query} onChange={onQuery} placeholder="Search this project" ariaLabel="Search this project" />
          </div>
          <Button variant="secondary" size="sm" disabled={searching || !query.trim()} onclick={onSearch}>
            {searching ? "Searching…" : "Search"}
          </Button>
        </div>
      {/snippet}

      {#if loading}
        <p class="px-5 py-8 text-center text-xs text-black-700 dark:text-black-600">Reading this project's memory…</p>
      {:else if empty}
        <div class="px-5 py-8 text-center">
          <p class="text-sm font-medium text-black-900 dark:text-white-100">Nothing has been written here yet</p>
          <p class="mx-auto mt-1.5 max-w-md text-xs leading-relaxed text-black-700 dark:text-black-600">
            Pages appear as agents work in this project. You can also write the first one yourself — a fact worth
            remembering, a rule about this codebase — and agents will recall it from their next session.
          </p>
          {#if canImport}
            <p class="mx-auto mt-1.5 max-w-md text-xs leading-relaxed text-black-700 dark:text-black-600">
              Sessions that ran here before capture was switched on are still on disk — import them below.
            </p>
          {/if}
        </div>
      {:else}
        <ul class="divide-y divide-white-300 dark:divide-navy-600" data-testid="page-cards">
          {#each [...cards, ...extraCards] as c (c.path)}
            <li>
              <button
                type="button"
                onclick={() => onOpen(c.path)}
                class={`w-full px-5 py-3 text-left transition-colors hover:bg-white-200 dark:hover:bg-navy-600 ${
                  c.path === openPath ? "bg-green-200 dark:bg-green-800" : ""
                }`}
              >
                <div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
                  <span class="min-w-0 truncate text-sm font-medium text-black-900 dark:text-white-100">{c.title}</span>
                  {#if c.kind}
                    <span class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{c.kind}</span>
                  {/if}
                  <span class="text-[0.6875rem] text-black-700 dark:text-black-600">{cardAge(c)}</span>
                </div>
                <p class="mt-0.5 truncate font-mono text-[0.6875rem] text-black-700 dark:text-black-600">{c.path}</p>
                {#if snippetFor(c)}
                  <!-- The snippet is the page's own first line, or the
                       search's match — never a restatement of the title. -->
                  <p class="mt-1 line-clamp-2 text-xs leading-relaxed text-black-800 dark:text-black-600">
                    {@html snippetFor(c)}
                  </p>
                {/if}
              </button>
            </li>
          {/each}
        </ul>
      {/if}

      {#if canManage}
        <div class="border-t border-white-300 px-5 py-3 dark:border-navy-600">
          <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">New page</p>
          <div class="mt-2 flex flex-wrap items-start gap-2">
            <div class="w-full sm:w-72">
              <TextInput
                value={newPath}
                onChange={(v) => {
                  newPath = v;
                  newPathTouched = true;
                }}
                placeholder="notes/deploy.md"
                ariaLabel="New page path"
                disabled={busy}
              />
            </div>
            <Button variant="secondary" size="sm" disabled={busy} onclick={addPage}>Write a page</Button>
          </div>
          {#if newPathProblem}
            <p class="mt-2 text-xs text-rose-700 dark:text-rose-300" data-testid="new-path-error">{newPathProblem}</p>
          {/if}
        </div>
      {:else}
        <p class="border-t border-white-300 px-5 py-3 text-xs leading-relaxed text-black-700 dark:border-navy-600 dark:text-black-600">
          {MANAGE_ADMIN_ONLY}
        </p>
      {/if}
    </Section>

    {#if canImport}
      <!-- Import: the same backfill the global panel runs, scoped to THIS
           project's bucket. It is here because this is where an empty project
           is discovered — sending someone to the store-wide panel to fix a
           project-shaped problem is how the two surfaces get confused. -->
      <Section
        title="Import earlier sessions"
        scope="this project only"
        note="Bring in this project's local harness history — the sessions that ran before capture was switched on. Preview writes nothing; it reports exactly what an import would take."
      >
        <div class="px-5 py-4">
          {#if canManage}
            <div class="flex flex-wrap items-center gap-2">
              <Button variant="secondary" size="sm" disabled={busy || !scope} onclick={() => onPreviewBackfill?.()}>
                Preview import
              </Button>
              <Button variant="danger" size="sm" disabled={busy || !scope} onclick={() => (confirmImport = true)}>
                Import
              </Button>
            </div>
          {:else}
            <p class="text-xs leading-relaxed text-black-700 dark:text-black-600">{MANAGE_ADMIN_ONLY}</p>
          {/if}

          {#if backfillError}
            <p class="mt-3 text-xs leading-relaxed text-rose-700 dark:text-rose-300" data-testid="backfill-error">
              {backfillError}
            </p>
          {:else if backfill}
            <p class="mt-3 text-xs leading-relaxed text-black-900 dark:text-white-100" data-testid="backfill-summary">
              <span class="font-medium">{backfill.dry_run ? "Preview" : "Imported"}:</span>
              {backfillSummary(backfill)}
            </p>
            {#if capWarning}
              <p class="mt-1 text-xs leading-relaxed text-rose-700 dark:text-rose-300" data-testid="backfill-cap">
                {capWarning}
              </p>
            {/if}
          {/if}
        </div>
      </Section>
    {/if}

    {#if openPath}
      <PageEditor
        path={openPath}
        {page}
        loading={pageLoading}
        error={pageError}
        {busy}
        {canManage}
        {draft}
        title={draftTitle}
        kind={draftKind}
        {committedNote}
        {saveMsg}
        {saveFailed}
        {checkpoints}
        {checkpointsLoading}
        {onDraft}
        {onTitle}
        {onKind}
        {onSave}
        {onDelete}
        {onClose}
        {onLoadCheckpoints}
        {onRestore}
      />
    {/if}
  {/if}
</div>

<ConfirmDialog
  open={confirmImport}
  title="Import this project's history?"
  body={importScope ? backfillConfirmBody(importScope, false) : ""}
  confirmLabel="Import"
  destructive={true}
  onConfirm={confirmAndImport}
  onCancel={() => (confirmImport = false)}
/>
