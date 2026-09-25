<script lang="ts">
  /* Analytics tab — what the store is doing, store-wide (PLAN §13.3).

     Every number here comes from `status`, which takes no --project. That is
     stated on every card rather than assumed: a store-wide figure sitting
     next to a project's own figure without a label is the single biggest
     source of confusion this feature has (PLAN §13.5 point 1), so the two
     never share a screen — per-project numbers live on the Projects tab. */
  import { Button, ConfirmDialog } from "@wick-fe/common-ui";
  import Section from "./Section.svelte";
  import BlockedState from "./BlockedState.svelte";
  import {
    GROWTH_NOTE,
    PROVIDER_SPLIT_NOTE,
    STORE_SCOPE_LABEL,
    compactConfirmBody,
    embeddingTriples,
    growthFacts,
    indexCoverage,
    ingestBreakdown,
    ingestVerdict,
    providerSplit,
    spoolFacts,
    storageFacts,
  } from "./analytics.js";
  import { blockedBy, formatCount, MANAGE_ADMIN_ONLY, pctWidth } from "./format.js";
  import type { Overview } from "./types.js";

  type Props = {
    ov: Overview | null;
    loading: boolean;
    busy: boolean;
    // canManage false = a viewer: Compact is left out, since it rewrites the
    // database (PLAN §23.2).
    canManage: boolean;
    onCompact: () => void;
    onGoOverview: () => void;
  };
  let { ov, loading, busy, canManage, onCompact, onGoOverview }: Props = $props();

  let confirmCompact = $state(false);

  // The store read can fail for a reason the user can fix; that reason is
  // carried on the Overview payload, so the tab reuses it rather than
  // probing again.
  const blocked = $derived(blockedBy(ov ? { error: ov.store_error, reason: ov.store_reason } : null));
  const store = $derived(ov?.store);
  const growth = $derived(growthFacts(store?.counts));
  const ingest = $derived(ingestBreakdown(store?.ingest));
  const spool = $derived(spoolFacts(store?.spool));
  const coverage = $derived(indexCoverage(store?.index));
  const triples = $derived(embeddingTriples(store?.raw));
  const split = $derived(providerSplit(ov?.used_by));
  const storage = $derived(storageFacts(store));

  function doCompact(): void {
    confirmCompact = false;
    onCompact();
  }
</script>

<div class="mx-auto w-full max-w-4xl space-y-5 px-4 py-5 sm:px-6 sm:py-6">
  {#if blocked}
    <BlockedState {blocked} onAction={onGoOverview} />
  {:else if !store}
    <div
      class="rounded-xl border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-10 text-center text-xs text-black-700 dark:text-black-600"
    >
      {loading ? "Reading the store…" : "This backend exposes no store numbers."}
    </div>
  {:else}
    <!-- Growth -->
    <Section title="Totals" scope={STORE_SCOPE_LABEL} note={GROWTH_NOTE}>
      <div class="grid grid-cols-2 gap-4 px-5 py-4 sm:grid-cols-4">
        {#each growth as g (g.label)}
          <div class="min-w-0">
            <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{g.label}</p>
            <p class="mt-0.5 text-xl font-semibold tabular-nums text-black-900 dark:text-white-100">{g.value}</p>
            <p class="mt-0.5 text-[0.6875rem] leading-tight text-black-700 dark:text-black-600">{g.note}</p>
          </div>
        {/each}
      </div>
    </Section>

    <!-- Ingest -->
    <Section title="Ingest" scope={STORE_SCOPE_LABEL} note={ingestVerdict(ingest)}>
      <div class="space-y-3 px-5 py-4">
        {#each ingest.parts as p (p.id)}
          <div class="min-w-0">
            <div class="flex flex-wrap items-baseline justify-between gap-2">
              <span class="text-xs font-medium text-black-900 dark:text-white-100">{p.label}</span>
              <span class="font-mono text-xs tabular-nums text-black-800 dark:text-black-600">
                {formatCount(p.value)} · {p.pct}
              </span>
            </div>
            <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-white-300 dark:bg-navy-600">
              <div class={`h-full rounded-full ${p.bar}`} style={`width:${pctWidth(p.value, ingest.total)}%`}></div>
            </div>
            <p class="mt-1 text-[0.6875rem] leading-tight text-black-700 dark:text-black-600">{p.note}</p>
          </div>
        {/each}
      </div>
    </Section>

    <!-- Hook spool -->
    <Section title="Hook spool" scope={STORE_SCOPE_LABEL} note={spool.verdict}>
      <dl class="grid grid-cols-3 gap-x-6 gap-y-3 px-5 py-4">
        {#each [["Pending", formatCount(spool.pending)], ["Oldest waiting", spool.oldest], ["Retries", formatCount(spool.retries)]] as [k, v] (k)}
          <div class="min-w-0">
            <dt class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{k}</dt>
            <dd
              class={`mt-0.5 font-mono text-sm tabular-nums ${
                spool.healthy ? "text-black-900 dark:text-white-100" : "text-rose-700 dark:text-rose-300"
              }`}
            >
              {v}
            </dd>
          </div>
        {/each}
      </dl>
    </Section>

    <!-- Index coverage -->
    <Section title="Index coverage" scope={STORE_SCOPE_LABEL}>
      <ul class="divide-y divide-white-300 dark:divide-navy-600">
        {#each coverage as c (c.id)}
          <li class="px-5 py-3">
            <div class="flex flex-wrap items-baseline justify-between gap-2">
              <span class="text-xs font-medium text-black-900 dark:text-white-100">{c.label}</span>
              <span
                class={`font-mono text-xs tabular-nums ${
                  c.ok ? "text-black-800 dark:text-black-600" : "text-rose-700 dark:text-rose-300"
                }`}
              >
                {formatCount(c.covered)} / {formatCount(c.total)} · {c.pct}
              </span>
            </div>
            <p class="mt-1 text-[0.6875rem] leading-relaxed text-black-700 dark:text-black-600">{c.note}</p>
          </li>
        {/each}
      </ul>
      {#if triples.length}
        <div class="border-t border-white-300 dark:border-navy-600 px-5 py-3">
          <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">
            Embeddings by model
          </p>
          <ul class="mt-1.5 space-y-1">
            {#each triples as t (t.provider + t.model + t.dim)}
              <li class="flex flex-wrap items-baseline justify-between gap-2 font-mono text-xs text-black-900 dark:text-white-100">
                <span class="truncate">{t.provider} · {t.model || "?"} · {t.dim}d</span>
                <span class="tabular-nums text-black-800 dark:text-black-600">{formatCount(t.count)} rows</span>
              </li>
            {/each}
          </ul>
        </div>
      {/if}
    </Section>

    <!-- Storage + compact -->
    <Section title="Storage" scope={STORE_SCOPE_LABEL} note={storage.verdict}>
      {#snippet actions()}
        <!-- Compaction deletes nothing, but it blocks every agent write for
             the length of a full database rewrite — so it is a danger button
             with a confirm that says exactly that (PLAN §13.5 point 2). -->
        {#if canManage}
          <Button variant="danger" size="sm" disabled={busy} onclick={() => (confirmCompact = true)}>Compact</Button>
        {:else}
          <p class="max-w-xs text-[0.6875rem] leading-relaxed text-black-700 dark:text-black-600">{MANAGE_ADMIN_ONLY}</p>
        {/if}
      {/snippet}
      <dl class="grid grid-cols-3 gap-x-6 gap-y-3 px-5 py-4">
        {#each [["Database", storage.database], ["Reclaimable", storage.reclaimable], ["Free on disk", storage.free]] as [k, v] (k)}
          <div class="min-w-0">
            <dt class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{k}</dt>
            <dd class="mt-0.5 font-mono text-sm tabular-nums text-black-900 dark:text-white-100">{v}</dd>
          </div>
        {/each}
      </dl>
    </Section>

    <!-- Providers wired to Agent Memory -->
    <Section title="Providers" scope="wick configuration — not captured counts" note={PROVIDER_SPLIT_NOTE}>
      {#if split.length === 0}
        <p class="px-5 py-6 text-center text-xs leading-relaxed text-black-700 dark:text-black-600">
          No provider instance has Agent Memory switched on, so nothing is being captured — whatever these counters
          say, they stopped moving when the last one was turned off. Turn it on per instance under
          <span class="font-medium text-black-900 dark:text-white-100">Providers</span>.
        </p>
      {:else}
        <ul class="divide-y divide-white-300 dark:divide-navy-600">
          {#each split as s (s.type)}
            <li class="flex flex-wrap items-center justify-between gap-2 px-5 py-2.5">
              <span class="font-mono text-sm text-black-900 dark:text-white-100">{s.type}</span>
              <span class="flex flex-wrap items-center gap-2 text-[0.6875rem] font-medium">
                <span class="rounded-full bg-green-200 dark:bg-green-800 px-2 py-0.5 text-green-700 dark:text-green-300">
                  {s.recording} recording
                </span>
                {#if s.readOnly > 0}
                  <span
                    class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-black-800 dark:text-black-600"
                    title="These instances read the store and write nothing back."
                  >
                    {s.readOnly} read only
                  </span>
                {/if}
              </span>
            </li>
          {/each}
        </ul>
      {/if}
    </Section>
  {/if}
</div>

<ConfirmDialog
  open={confirmCompact}
  title="Compact the memory database?"
  body={compactConfirmBody(storage)}
  confirmLabel="Compact now"
  destructive={true}
  onConfirm={doCompact}
  onCancel={() => (confirmCompact = false)}
/>
