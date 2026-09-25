<script lang="ts">
  /* Projects tab — every workspace/project the store knows, and one project's
     detail (PLAN §13.3).

     Two things about the shape of this tab:

     - The project list comes from /api/v1/projects, which only exists when
       the daemon runs with --enable-web. With it off the server answers a
       named reason, and this renders that as something to switch on. An
       empty table would say "you have no memory", which is a lie about a
       setting (PLAN §13.1).
     - Every number on this tab is PER PROJECT. Sessions, observations and
       7d/30d activity come from the backend's briefing — the one MCP call
       this dashboard makes (PLAN §13.2.1) — and the store-wide totals with
       the same names live on the Analytics tab. They are never mixed here,
       and a project whose briefing failed says so instead of borrowing the
       store-wide figure: mixing those two scopes is the confusion §13.5
       opens with. Every cell resolves through projectMetrics(). */
  import { Button, ConfirmDialog } from "@wick-fe/common-ui";
  import Section from "./Section.svelte";
  import BlockedState from "./BlockedState.svelte";
  import {
    BRIEFING_UNAVAILABLE,
    backfillCapWarning,
    backfillConfirmBody,
    backfillSummary,
    handoffCellText,
    handoffDetail,
    latestPages,
    metricText,
    metricTitle,
    pageLabel,
    projectKey,
    projectMetrics,
    scopeCaveat,
    scopeKeyOf,
    scopeOrigin,
    sortProjects,
  } from "./projects.js";
  import type { HandoffCell } from "./projects.js";
  import { blockedBy, MANAGE_ADMIN_ONLY, relativeTime } from "./format.js";
  import type { BackfillReport, BackfillRequestEcho, ProjectRow, ProjectScope, ProjectsResponse } from "./types.js";

  type Props = {
    res: ProjectsResponse | null;
    loading: boolean;
    busy: boolean;
    selected: string;
    handoffs: Record<string, HandoffCell>;
    backfill: BackfillReport | null;
    // What the server actually ran. Carries the resolved max-sessions cap,
    // which the report itself does not.
    backfillReq: BackfillRequestEcho | null;
    backfillError: string;
    // canManage false = a viewer: the import controls are left out, because
    // a backfill writes into the store (PLAN §23.2).
    canManage: boolean;
    // scope is set when the panel was opened from ONE wick project's menu:
    // the bucket the server resolved for it, which the tab opens on. Null is
    // the ordinary store-wide view (PLAN §22).
    scope: ProjectScope | null;
    // scopeError is a resolution that failed — a project that is gone, or a
    // panel opened with an id nobody has. Said out loud rather than silently
    // falling back to the whole store.
    scopeError: string;
    onSelect: (key: string) => void;
    onPreviewBackfill: (row: ProjectRow) => void;
    onRunBackfill: (row: ProjectRow) => void;
    onRefresh: () => void;
    onGoSettings: () => void;
  };
  let {
    res,
    loading,
    busy,
    selected,
    handoffs,
    backfill,
    backfillReq,
    backfillError,
    canManage,
    scope,
    scopeError,
    onSelect,
    onPreviewBackfill,
    onRunBackfill,
    onRefresh,
    onGoSettings,
  }: Props = $props();

  let confirmImport = $state(false);

  const blocked = $derived(blockedBy(res));
  const rows = $derived(sortProjects(res?.projects ?? []));
  const current = $derived(rows.find((r) => projectKey(r) === selected) ?? null);
  const metrics = $derived(current ? projectMetrics(current) : null);
  const capWarning = $derived(backfillCapWarning(backfill ?? undefined, backfillReq));
  const handoffLine = $derived(handoffDetail(handoffs[selected]));
  const pages = $derived(latestPages(current));
  // The legend under the table only earns its space when a row actually
  // shows "n/a". On a healthy store every cell is filled and a standing
  // paragraph about unreadable numbers would just be noise.
  const anyUnbriefed = $derived(rows.some((r) => !r.briefing));

  // What the panel was opened for, and whether the store has anything for it.
  // "Resolved but absent" is its own state: the project exists in wick and its
  // bucket is known, and nothing has ever been captured into it — which is an
  // invitation to backfill, not an error (PLAN §22.3).
  const scopeKey = $derived(scope ? scopeKeyOf(scope) : "");
  const scopeRow = $derived(scopeKey ? (rows.find((r) => projectKey(r) === scopeKey) ?? null) : null);
  const scopeMissing = $derived(Boolean(scope) && !scopeRow);
  const caveat = $derived(scope ? scopeCaveat(scope.source) : null);

  // The synthetic row an empty bucket's backfill runs against. Only the
  // workspace/project are read by the call, and they come from the server's
  // own resolution — never from a name built here (PLAN §22.2).
  const scopeAsRow = $derived<ProjectRow | null>(
    scope ? { workspace: scope.workspace, project: scope.project, page_count: 0 } : null,
  );

  // What an Import would act on: the selected row, or — when the panel was
  // opened for a project the store has never seen — the resolved bucket.
  const importTarget = $derived<ProjectRow | null>(current ?? (scopeMissing ? scopeAsRow : null));

  function confirmAndImport(): void {
    confirmImport = false;
    if (importTarget) onRunBackfill(importTarget);
  }
</script>

<div class="mx-auto w-full max-w-5xl space-y-5 px-4 py-5 sm:px-6 sm:py-6">
  <!-- Opened from one project's "⋯" menu: say which bucket that project maps
       to and how that was decided, before any table. The name is the SERVER's
       answer — the panel showing a bucket the agents do not write to is the
       exact failure §22.2 exists to prevent. -->
  {#if scopeError}
    <div class="rounded-xl border border-rose-300 bg-rose-100 px-5 py-3 dark:border-rose-700 dark:bg-navy-800">
      <p class="text-sm font-medium text-rose-700 dark:text-rose-300">This project's memory could not be located</p>
      <p class="mt-1 text-xs leading-relaxed text-black-800 dark:text-black-600">{scopeError}</p>
    </div>
  {:else if scope}
    <section
      class="rounded-xl border border-white-300 bg-white-100 px-5 py-4 dark:border-navy-600 dark:bg-navy-700"
      data-testid="scope-card"
    >
      <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">Project</p>
      <p class="mt-0.5 text-sm font-medium text-black-900 dark:text-white-100">{scope.name}</p>
      <p class="mt-1.5 text-xs text-black-800 dark:text-black-600">
        Memory bucket
        <span class="font-mono text-black-900 dark:text-white-100">{scope.workspace}/{scope.project}</span>
        — {scopeOrigin(scope.source)}.
      </p>
      {#if caveat}
        <p class="mt-1.5 text-xs leading-relaxed text-cau-600 dark:text-cau-400" data-testid="scope-caveat">{caveat}</p>
      {/if}

      {#if scopeMissing}
        <!-- Resolved, and empty. A zero with no explanation reads as "this
             feature is broken"; what is actually true is that nothing has
             been captured here yet, and the history that predates the hook
             can be imported — preview first (PLAN §22.3, §13.5 point 4). -->
        <div class="mt-3 border-t border-white-300 pt-3 dark:border-navy-600">
          <p class="text-xs leading-relaxed text-black-800 dark:text-black-600">
            Nothing has been captured into this bucket yet. Sessions that ran here before the hook was installed are
            still on disk — a preview reports exactly what an import would take, without writing anything.
          </p>
          {#if canManage}
            <div class="mt-3 flex flex-wrap items-center gap-2">
              <Button
                variant="secondary"
                size="sm"
                disabled={busy || !scopeAsRow}
                onclick={() => scopeAsRow && onPreviewBackfill(scopeAsRow)}
              >
                Preview import
              </Button>
              <Button variant="danger" size="sm" disabled={busy} onclick={() => (confirmImport = true)}>Import</Button>
            </div>
          {:else}
            <p class="mt-2 text-xs leading-relaxed text-black-700 dark:text-black-600">{MANAGE_ADMIN_ONLY}</p>
          {/if}
          {#if backfillError}
            <p class="mt-2 text-xs leading-relaxed text-rose-700 dark:text-rose-300">{backfillError}</p>
          {:else if backfill}
            <p class="mt-2 text-xs leading-relaxed text-black-900 dark:text-white-100">
              <span class="font-medium">{backfill.dry_run ? "Preview" : "Imported"}:</span>
              {backfillSummary(backfill)}
            </p>
          {/if}
        </div>
      {/if}
    </section>
  {/if}

  {#if blocked}
    <BlockedState {blocked} onAction={onGoSettings} />
  {:else if loading && !res}
    <div
      class="rounded-xl border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-10 text-center text-xs text-black-700 dark:text-black-600"
    >
      Listing projects…
    </div>
  {:else if rows.length === 0}
    <div
      class="rounded-xl border border-dashed border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-10 text-center"
    >
      <p class="text-sm font-medium text-black-900 dark:text-white-100">The store holds no projects yet</p>
      <p class="mx-auto mt-1.5 max-w-md text-xs leading-relaxed text-black-700 dark:text-black-600">
        A project appears here the first time a session is captured in it. If agents have already been working, the
        hook is probably not installed — the Health tab compares what ran locally against what was captured and names
        the command that fixes it.
      </p>
      <div class="mt-4">
        <Button variant="secondary" size="md" onclick={onRefresh}>Refresh</Button>
      </div>
    </div>
  {:else}
    <Section
      title="Projects"
      scope="one row per workspace/project"
      note="Every number in this table is this project's own. The store-wide totals with the same names are on the Analytics tab and are never mixed in here; a project whose counters could not be read shows n/a rather than borrowing them."
    >
      {#snippet actions()}
        <Button variant="secondary" size="sm" disabled={loading} onclick={onRefresh}>Refresh</Button>
      {/snippet}
      <div class="overflow-x-auto">
        <table class="w-full text-left text-xs">
          <thead class="border-b border-white-300 dark:border-navy-600">
            <tr class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">
              <th class="px-5 py-2 font-medium">Project</th>
              <th class="px-5 py-2 font-medium">Sessions</th>
              <th class="px-5 py-2 font-medium">Observations</th>
              <th class="px-5 py-2 font-medium">Pages</th>
              <th class="px-5 py-2 font-medium">Activity 7d/30d</th>
              <th class="px-5 py-2 font-medium">Last active</th>
              <th class="px-5 py-2 font-medium">Handoffs pending</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-white-300 dark:divide-navy-600">
            {#each rows as r (projectKey(r))}
              {@const m = projectMetrics(r)}
              {@const key = projectKey(r)}
              <tr
                class={`cursor-pointer transition-colors ${
                  key === selected
                    ? "bg-green-200 dark:bg-green-800"
                    : "hover:bg-white-200 dark:hover:bg-navy-600"
                }`}
                onclick={() => onSelect(key)}
              >
                <td class="px-5 py-2">
                  <span class="font-mono text-black-900 dark:text-white-100">{r.project}</span>
                  <span class="ml-1.5 text-black-700 dark:text-black-600">{r.workspace}</span>
                </td>
                <td class="px-5 py-2 tabular-nums text-black-700 dark:text-black-600" title={metricTitle(m.sessions)}
                  >{metricText(m.sessions)}</td
                >
                <td
                  class="px-5 py-2 tabular-nums text-black-700 dark:text-black-600"
                  title={metricTitle(m.observations)}>{metricText(m.observations)}</td
                >
                <td class="px-5 py-2 tabular-nums text-black-900 dark:text-white-100" title={metricTitle(m.pages)}
                  >{metricText(m.pages)}</td
                >
                <td class="px-5 py-2 tabular-nums text-black-700 dark:text-black-600" title={metricTitle(m.activity)}
                  >{metricText(m.activity)}</td
                >
                <td class="px-5 py-2 text-black-900 dark:text-white-100" title={metricTitle(m.lastActive)}
                  >{metricText(m.lastActive)}</td
                >
                <td class="px-5 py-2 tabular-nums text-black-900 dark:text-white-100">
                  {handoffCellText(handoffs[key])}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if anyUnbriefed}
        <p
          class="border-t border-white-300 dark:border-navy-600 px-5 py-2.5 text-[0.6875rem] leading-relaxed text-black-700 dark:text-black-600"
        >
          n/a — {BRIEFING_UNAVAILABLE}
        </p>
      {/if}
    </Section>

    {#if current && metrics}
      <Section title={`${current.workspace}/${current.project}`} scope="this project only">
        <dl class="grid grid-cols-2 gap-x-6 gap-y-3 px-5 py-4 sm:grid-cols-5">
          {#each [["Pages", metrics.pages], ["Last active", metrics.lastActive], ["Sessions", metrics.sessions], ["Observations", metrics.observations], ["Activity 7d/30d", metrics.activity]] as [k, cell] (k)}
            <div class="min-w-0">
              <dt class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{k}</dt>
              <dd
                class={`mt-0.5 truncate text-sm tabular-nums ${
                  cell.available ? "text-black-900 dark:text-white-100" : "text-black-700 dark:text-black-600"
                }`}
                title={metricTitle(cell)}
              >
                {metricText(cell)}
              </dd>
            </div>
          {/each}
        </dl>

        <!-- Latest pages — the project's most recently updated pages, newest
             first. "Briefed but empty" and "could not be briefed" are
             different lines on purpose: one is a fact about the project, the
             other is a fact about the read. -->
        <div class="border-t border-white-300 dark:border-navy-600 px-5 py-3">
          <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">Latest pages</p>
          {#if pages.state === "ok"}
            <ul class="mt-1.5 space-y-1">
              {#each pages.pages as p (p.path)}
                <li class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 text-xs">
                  <span class="min-w-0 truncate text-black-900 dark:text-white-100" title={p.path}>{pageLabel(p)}</span>
                  {#if p.kind}
                    <span class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{p.kind}</span>
                  {/if}
                  <span class="text-[0.6875rem] text-black-700 dark:text-black-600" title={p.updated_at ?? ""}>
                    {relativeTime(p.updated_at)}
                  </span>
                </li>
              {/each}
            </ul>
          {:else if pages.state === "empty"}
            <p class="mt-1 text-xs leading-relaxed text-black-700 dark:text-black-600">
              No pages yet — this project has been read and holds none. Full page browsing belongs to the Wiki tab.
            </p>
          {:else}
            <p class="mt-1 text-xs leading-relaxed text-black-700 dark:text-black-600">{pages.reason}</p>
          {/if}
        </div>

        <!-- Cross-project links. Zero here is the answer most of the time,
             and it is worth stating: it is what says this project's memory is
             self-contained rather than leaning on another client's. -->
        {#if current.briefing}
          <div class="border-t border-white-300 dark:border-navy-600 px-5 py-3">
            <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">Cross-project links</p>
            <p class="mt-1 text-xs leading-relaxed text-black-900 dark:text-white-100">
              {current.briefing.cross_project_dependencies} dependenc{current.briefing.cross_project_dependencies === 1
                ? "y"
                : "ies"} on other projects, {current.briefing.cross_project_dependents} other project{current.briefing
                .cross_project_dependents === 1
                ? ""
                : "s"} depending on this one.
            </p>
          </div>
        {/if}

        <div class="border-t border-white-300 dark:border-navy-600 px-5 py-3">
          <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">Pending handoffs</p>
          <p
            class={`mt-1 text-xs ${
              handoffLine.error ? "text-rose-700 dark:text-rose-300" : "text-black-900 dark:text-white-100"
            }`}
          >
            {handoffLine.text}
          </p>
        </div>

        <!-- Backfill: preview first, always. The import itself is the
             dangerous one — a forced run re-imports captured sessions and
             observations do not dedupe (PLAN §11.1) — so it is a danger
             button behind a confirm that says so. -->
        <div class="border-t border-white-300 dark:border-navy-600 px-5 py-3">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div class="min-w-0">
              <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">Backfill</p>
              <p class="mt-1 text-xs leading-relaxed text-black-700 dark:text-black-600">
                Import this project's local harness history — the sessions that happened before the hook was installed.
                Preview first: it reports exactly what an import would take.
              </p>
            </div>
            {#if canManage}
              <div class="flex flex-shrink-0 flex-wrap items-center gap-2">
                <Button variant="secondary" size="sm" disabled={busy} onclick={() => onPreviewBackfill(current)}>
                  Preview
                </Button>
                <Button variant="danger" size="sm" disabled={busy} onclick={() => (confirmImport = true)}>Import</Button>
              </div>
            {:else}
              <p class="max-w-xs flex-shrink-0 text-xs leading-relaxed text-black-700 dark:text-black-600">
                {MANAGE_ADMIN_ONLY}
              </p>
            {/if}
          </div>

          {#if backfillError}
            <p class="mt-2 text-xs leading-relaxed text-rose-700 dark:text-rose-300">{backfillError}</p>
          {:else if backfill}
            <p class="mt-2 text-xs leading-relaxed text-black-900 dark:text-white-100">
              <span class="font-medium">{backfill.dry_run ? "Preview" : "Imported"}:</span>
              {backfillSummary(backfill)}
            </p>
            {#if capWarning}
              <p class="mt-1 text-xs leading-relaxed text-rose-700 dark:text-rose-300">{capWarning}</p>
            {/if}
          {/if}
        </div>
      </Section>
    {:else}
      <p class="px-1 text-xs text-black-700 dark:text-black-600">
        Select a project above to see its detail and import its history.
      </p>
    {/if}
  {/if}
</div>

<ConfirmDialog
  open={confirmImport}
  title="Import this project's history?"
  body={importTarget ? backfillConfirmBody(`${importTarget.workspace}/${importTarget.project}`, false) : ""}
  confirmLabel="Import"
  destructive={true}
  onConfirm={confirmAndImport}
  onCancel={() => (confirmImport = false)}
/>
