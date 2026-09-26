<script lang="ts">
  /* Importing this project's earlier sessions, on its own page.

     It was a card at the bottom of the memory tab and that was the wrong
     place twice over (Yoga, 2026-09-26: "mending halaman terpisah import ini
     jangan di bawah gini, jadi lebih flexibel"). It is rare, one of its
     buttons duplicates data irreversibly, and it needs room to show its
     working — which is the other half of the ask: "yang jangan bohong harus
     real dan ada datanya dan preview nya, biar aku bisa cek".

     So everything on this page is the backend's own output, shown with the
     folder it was measured in. wick does not go reading ~/.claude or
     ~/.codex itself to produce a prettier answer: that would be a second
     opinion about which sessions belong to a project, sitting next to the
     one that actually does the importing, and the two would drift. Where
     ai-memory cannot answer — it has no way to LIST what it found — the page
     says so. */
  import { Button, ConfirmDialog } from "@wick-fe/common-ui";
  import Section from "./Section.svelte";
  import StatGrid from "./StatGrid.svelte";
  import { MANAGE_ADMIN_ONLY } from "./format.js";
  import {
    BACKFILL_EXPLAINER,
    BACKFILL_SELECTED_NOTE,
    backfillCapWarning,
    backfillConfirmBody,
    backfillSummary,
    countsNote,
    harnessRows,
    IMPORT_NO_LIST,
    localTotal,
  } from "./projects.js";
  import type { StatItem } from "./stats.js";
  import type { BackfillReport, BackfillRequestEcho, HealthReport, ProjectScope } from "./types.js";

  type Props = {
    scope: ProjectScope | null;
    canManage: boolean;
    busy: boolean;
    /* health carries doctor's per-harness rows for THIS project, from the
       same call the Health tab makes. It is scoped, so its numbers were
       measured in this project's folder and not in wick's. */
    health: HealthReport | null;
    healthLoading: boolean;
    healthError: string;
    backfill: BackfillReport | null;
    backfillReq: BackfillRequestEcho | null;
    backfillError: string;
    onPreview: () => void;
    onRun: () => void;
    onForce: () => void;
    onRefresh: () => void;
    onBack: () => void;
  };
  let {
    scope,
    canManage,
    busy,
    health,
    healthLoading,
    healthError,
    backfill,
    backfillReq,
    backfillError,
    onPreview,
    onRun,
    onForce,
    onRefresh,
    onBack,
  }: Props = $props();

  let confirmImport = $state(false);
  let confirmForce = $state(false);

  const bucket = $derived(scope ? `${scope.workspace}/${scope.project}` : "");
  const doctor = $derived(health?.doctor ?? null);
  const rows = $derived(harnessRows(doctor));
  const local = $derived(localTotal(doctor));
  // Only a preview's selection is comparable with doctor's totals; a real
  // run's report answers a different question.
  const selected = $derived(backfill?.dry_run ? backfill.selected : null);
  const agreement = $derived(countsNote(selected, local));
  const capWarning = $derived(backfillCapWarning(backfill ?? undefined, backfillReq));

  const totals = $derived<StatItem[]>(
    local === null
      ? []
      : [
          {
            label: "In the folder",
            value: String(local),
            note: "harness transcript files the check found here",
          },
          {
            label: "Already captured",
            value: String(rows.reduce((n, r) => n + r.captured, 0)),
            note: "sessions this project's memory already holds",
          },
        ],
  );
</script>

<div class="mx-auto w-full max-w-4xl space-y-5 px-4 py-5 sm:px-6 sm:py-6">
  <div class="flex flex-wrap items-start justify-between gap-3">
    <div class="min-w-0">
      <h2 class="truncate text-base font-semibold text-black-900 dark:text-white-100" data-testid="import-title">
        Import earlier sessions
      </h2>
      {#if bucket}
        <p class="mt-0.5 text-xs text-black-800 dark:text-black-600">
          into <span class="font-mono text-black-900 dark:text-white-100">{bucket}</span>
        </p>
      {/if}
    </div>
    <div class="flex flex-shrink-0 flex-wrap items-center gap-2">
      <Button variant="secondary" size="sm" disabled={busy || healthLoading} onclick={onRefresh}>Re-check</Button>
      <Button variant="ghost" size="sm" onclick={onBack}>Back to memory</Button>
    </div>
  </div>

  <!-- What it does, in full, before anything is offered. -->
  <Section title="What an import does" scope="this project only">
    <p class="px-5 py-4 text-xs leading-relaxed text-black-800 dark:text-black-600" data-testid="import-explainer">
      {BACKFILL_EXPLAINER}
    </p>
  </Section>

  <!-- The folder. Every number below was measured here, and a claim about
       "this project's folder" is unverifiable until the path is on screen. -->
  <Section title="The folder it reads" scope="this project only">
    {#if scope?.folder}
      <p class="break-all px-5 py-4 font-mono text-xs text-black-900 dark:text-white-100" data-testid="import-folder">
        {scope.folder}
      </p>
    {:else}
      <p class="px-5 py-4 text-xs leading-relaxed text-black-700 dark:text-black-600" data-testid="import-folder">
        This project's folder could not be resolved, so there is nothing to import from.
      </p>
    {/if}
  </Section>

  <!-- Doctor's own decomposition of the count, per harness. -->
  <Section
    title="What is in it"
    scope="this project only"
    note="From the backend's own harness check, run in the folder above — the same discovery the import uses."
  >
    {#if healthLoading}
      <p class="px-5 py-8 text-center text-xs text-black-700 dark:text-black-600">Checking this project's folder…</p>
    {:else if healthError || doctor?.error}
      <p class="px-5 py-4 text-xs leading-relaxed text-rose-700 dark:text-rose-300" data-testid="import-health-error">
        The harness check did not answer: {healthError || doctor?.error}
      </p>
    {:else if rows.length === 0}
      <p class="px-5 py-4 text-xs leading-relaxed text-black-700 dark:text-black-600" data-testid="import-no-harness">
        The check found no harness with sessions in this folder.
      </p>
    {:else}
      <StatGrid items={totals} cols={2} large={true} testid="import-totals" />
      <ul class="divide-y divide-white-300 border-t border-white-300 dark:divide-navy-600 dark:border-navy-600" data-testid="harness-rows">
        {#each rows as r (r.agent)}
          <li class="flex flex-wrap items-baseline justify-between gap-2 px-5 py-3">
            <span class="font-mono text-sm text-black-900 dark:text-white-100">{r.agent}</span>
            <span class="text-xs tabular-nums text-black-800 dark:text-black-600">
              {r.local} in the folder · {r.recent} recent · {r.captured} already captured
            </span>
          </li>
        {/each}
      </ul>
      {#if rows.some((r) => r.uncaptured)}
        <p class="border-t border-white-300 px-5 py-3 text-xs leading-relaxed text-cau-600 dark:border-navy-600 dark:text-cau-400">
          A harness here has run sessions that never reached the store. That is what an import is for.
        </p>
      {/if}
    {/if}

    <!-- The limit, said rather than worked around. -->
    <p
      class="border-t border-white-300 px-5 py-3 text-[0.6875rem] leading-relaxed text-black-700 dark:border-navy-600 dark:text-black-600"
      data-testid="import-no-list"
    >
      {IMPORT_NO_LIST}
    </p>
  </Section>

  <!-- Preview, then the two writes. -->
  <Section title="Preview and import" scope="this project only" note={BACKFILL_SELECTED_NOTE}>
    <div class="px-5 py-4">
      {#if canManage}
        <div class="flex flex-wrap items-center gap-2">
          <Button variant="secondary" size="sm" disabled={busy || !scope} onclick={onPreview}>Preview import</Button>
          <Button variant="danger" size="sm" disabled={busy || !scope} onclick={() => (confirmImport = true)}>
            Import now
          </Button>
          <Button variant="danger" size="sm" disabled={busy || !scope} onclick={() => (confirmForce = true)}>
            Force re-import
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
        {#if agreement}
          <p class="mt-1 text-xs leading-relaxed text-black-800 dark:text-black-600" data-testid="counts-note">
            {agreement}
          </p>
        {/if}
        <!-- The cap is only worth a line when it actually left something
             out. A ceiling nobody reached is not news (Yoga: "2k session itu
             maksut nya gimana, di project itu aku cuma init 16 kali"). -->
        {#if capWarning}
          <p class="mt-1 text-xs leading-relaxed text-rose-700 dark:text-rose-300" data-testid="backfill-cap">
            {capWarning}
          </p>
        {/if}
      {/if}
    </div>
  </Section>
</div>

<ConfirmDialog
  open={confirmImport}
  title="Import this project's history?"
  body={bucket ? backfillConfirmBody(bucket, false) : ""}
  confirmLabel="Import now"
  destructive={true}
  onConfirm={() => {
    confirmImport = false;
    onRun();
  }}
  onCancel={() => (confirmImport = false)}
/>

<ConfirmDialog
  open={confirmForce}
  title="Force a re-import?"
  body={bucket ? backfillConfirmBody(bucket, true) : ""}
  confirmLabel="Force re-import"
  destructive={true}
  onConfirm={() => {
    confirmForce = false;
    onForce();
  }}
  onCancel={() => (confirmForce = false)}
/>
