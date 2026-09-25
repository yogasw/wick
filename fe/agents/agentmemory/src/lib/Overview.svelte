<script lang="ts">
  /* Overview tab — "how is my memory doing?" on one screen (PLAN §13.3).

     The daemon card and the store card are separate on purpose: they answer
     different questions and can disagree (a daemon up with an empty store, a
     store readable while the daemon is down), so neither blanks the other.
     Every number is labelled with its scope — store-wide here, never mixed
     with a per-project count (PLAN §13.5 point 1). */
  import { Button } from "@wick-fe/common-ui";
  import type { Overview, TestResult } from "./types.js";
  import {
    badgeFor,
    captureChip,
    formatCount,
    instanceLabel,
    MANAGE_ADMIN_ONLY,
    measured,
    portLabel,
    uptimeOf,
    warningsFor,
  } from "./format.js";

  type Props = {
    ov: Overview | null;
    loading: boolean;
    busy: boolean;
    test: TestResult | null;
    // canManage false = a viewer: the daemon controls are not rendered at
    // all, and one line says who may drive them (PLAN §23.3).
    canManage: boolean;
    onStart: () => void;
    onStop: () => void;
    onRestart: () => void;
    onTest: () => void;
  };
  let { ov, loading, busy, test, canManage, onStart, onStop, onRestart, onTest }: Props = $props();

  // now ticks once a second so the uptime reads as a clock rather than as a
  // value frozen at the last poll.
  let now = $state(Date.now());
  $effect(() => {
    const t = setInterval(() => (now = Date.now()), 1000);
    return () => clearInterval(t);
  });

  const daemon = $derived(ov?.daemon);
  const badge = $derived(badgeFor(daemon));
  const warnings = $derived(warningsFor(ov));
  const usedBy = $derived(ov?.used_by ?? []);
  const counts = $derived(ov?.store?.counts);

  // The gap between every stored version and the latest one is what a compact
  // would reclaim — and what a forced backfill inflates (PLAN §11.1).
  const pageVersionGap = $derived(
    counts ? Math.max(0, counts.pages_all - counts.pages_latest) : 0,
  );
</script>

<div class="mx-auto w-full max-w-4xl space-y-5 px-4 py-5 sm:px-6 sm:py-6">
  <!-- Warnings: derived, never dismissible, each stating the real consequence -->
  {#each warnings as w (w.id)}
    <div
      class={`rounded-xl border px-4 py-3 ${
        w.level === "warn"
          ? "border-rose-300 bg-rose-100 dark:border-rose-700 dark:bg-navy-800"
          : "border-white-400 bg-white-200 dark:border-navy-600 dark:bg-navy-800"
      }`}
    >
      <p
        class={`text-sm font-medium ${
          w.level === "warn"
            ? "text-rose-700 dark:text-rose-300"
            : "text-black-900 dark:text-white-100"
        }`}
      >
        {w.title}
      </p>
      <p class="mt-1 text-xs leading-relaxed text-black-800 dark:text-black-600">{w.body}</p>
    </div>
  {/each}

  <!-- Daemon -->
  <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
    <div class="flex flex-wrap items-center justify-between gap-3 border-b border-white-300 dark:border-navy-600 px-5 py-3">
      <div class="flex items-center gap-2">
        <h2 class="text-sm font-medium text-black-900 dark:text-white-100">Daemon</h2>
        <span
          class={`inline-flex items-center gap-1.5 rounded-full bg-white-300 dark:bg-navy-600 px-2.5 py-1 text-xs font-medium ${badge.cls}`}
        >
          <span class="h-1.5 w-1.5 rounded-full bg-current"></span>
          {badge.text}
        </span>
        {#if daemon?.running && !daemon?.managed}
          <span
            class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[0.6875rem] font-medium text-black-800 dark:text-black-600"
            title="This daemon was not started by wick — it was started by hand or outlived a wick restart. Stop and Restart still act on it only if wick owns the process."
          >
            not managed by wick
          </span>
        {/if}
      </div>
      <div class="flex flex-wrap items-center gap-2">
        {#if canManage}
          {#if daemon?.state === "running" || daemon?.state === "starting"}
            <!-- Stopping is the destructive one here: every instance wired to
                 this daemon loses recall AND stops being captured while it is
                 down, so it gets the danger variant + a confirm that names
                 that consequence (PLAN §13.5 point 2). -->
            <Button variant="danger" size="lg" disabled={busy} onclick={onStop}>Stop</Button>
            <Button variant="secondary" size="lg" disabled={busy} onclick={onRestart}>Restart</Button>
          {:else}
            <Button variant="primary" size="lg" disabled={busy || daemon?.state === "not-installed"} onclick={onStart}>
              Start
            </Button>
          {/if}
          <Button variant="secondary" size="lg" disabled={busy} onclick={onTest}>Test connection</Button>
        {:else}
          <!-- A viewer gets the sentence instead of the buttons, not the
               buttons greyed out (PLAN §23.3). -->
          <p class="max-w-md text-xs leading-relaxed text-black-700 dark:text-black-600">{MANAGE_ADMIN_ONLY}</p>
        {/if}
      </div>
    </div>

    <dl class="grid grid-cols-2 gap-x-6 gap-y-3 px-5 py-4 sm:grid-cols-3">
      {#each [["Version", daemon?.version || "—"], ["Uptime", uptimeOf(daemon, now)], ["Port", portLabel(daemon)], ["Base URL", daemon?.base_url || "—"], ["Process RSS", ov ? measured(ov.resources.rss_bytes, ov.resources.rss_known) : "—"], ["Store on disk", ov ? measured(ov.resources.data_dir_bytes, ov.resources.data_dir_known) : "—"]] as [k, v] (k)}
        <div class="min-w-0">
          <dt class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{k}</dt>
          <dd class="mt-0.5 truncate font-mono text-sm text-black-900 dark:text-white-100" title={String(v)}>{v}</dd>
        </div>
      {/each}
    </dl>

    {#if daemon?.state === "not-installed"}
      <!-- Same fact, addressed to whoever can act on it: an admin is told
           what to do, a viewer is told who does it. Telling a viewer to
           "press Start" points at a button that is not on their screen. -->
      <p class="border-t border-white-300 dark:border-navy-600 px-5 py-3 text-xs text-black-800 dark:text-black-600">
        The backend binary is not on PATH. wick does not install it yet —
        {#if canManage}put{:else}an admin has to put{/if}
        <code class="font-mono text-black-900 dark:text-white-100">{ov?.backend.name ?? "the backend"}</code>
        on PATH{#if canManage} yourself{/if}{#if ov?.backend.github_url}&nbsp;(<a
            href={ov.backend.github_url}
            target="_blank"
            rel="noreferrer"
            class="text-green-600 hover:text-green-500 dark:text-green-300">upstream repo</a
          >){/if}{#if canManage}, then press Start.{:else} and start it.{/if}
      </p>
    {/if}

    {#if test}
      <p
        class={`border-t border-white-300 dark:border-navy-600 px-5 py-3 text-xs ${
          test.ok ? "text-black-800 dark:text-black-600" : "text-rose-700 dark:text-rose-300"
        }`}
      >
        {#if test.ok}
          Answered at <span class="font-mono">{test.base_url}{test.health_path}</span>
          {#if test.version}&nbsp;· v{test.version}{/if}
          {#if test.counts}&nbsp;· {formatCount(test.counts.sessions)} sessions, {formatCount(test.counts.observations)} observations{/if}
          {#if test.store_error}&nbsp;· store unreadable: {test.store_error}{/if}
        {:else}
          {test.error ?? "No answer from the daemon."}
        {/if}
      </p>
    {/if}
  </section>

  <!-- Store counters (store-wide, NOT per project) -->
  <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
    <div class="flex flex-wrap items-baseline justify-between gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3">
      <h2 class="text-sm font-medium text-black-900 dark:text-white-100">Store</h2>
      <span class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">
        whole store — every workspace and project
      </span>
    </div>
    {#if ov?.store}
      <div class="grid grid-cols-2 gap-4 px-5 py-4 sm:grid-cols-4">
        {#each [["Pages", formatCount(ov.store.counts.pages_latest), "latest version of each"], ["Page versions", formatCount(ov.store.counts.pages_all), pageVersionGap ? `${formatCount(pageVersionGap)} older versions kept` : "no older versions"], ["Sessions", formatCount(ov.store.counts.sessions), "captured end to end"], ["Observations", formatCount(ov.store.counts.observations), "raw facts behind the pages"]] as [k, v, note] (k)}
          <div class="min-w-0">
            <p class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{k}</p>
            <p class="mt-0.5 text-xl font-semibold tabular-nums text-black-900 dark:text-white-100">{v}</p>
            <p class="mt-0.5 text-[0.6875rem] leading-tight text-black-700 dark:text-black-600">{note}</p>
          </div>
        {/each}
      </div>
      <dl class="grid grid-cols-2 gap-x-6 gap-y-3 border-t border-white-300 dark:border-navy-600 px-5 py-3 sm:grid-cols-3">
        {#each [["Backend version", ov.store.version || "—"], ["Capture mode", ov.store.capture_mode || "—"], ["Data dir", ov.store.data_dir || "—"]] as [k, v] (k)}
          <div class="min-w-0">
            <dt class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">{k}</dt>
            <dd class="mt-0.5 truncate font-mono text-xs text-black-900 dark:text-white-100" title={String(v)}>{v}</dd>
          </div>
        {/each}
      </dl>
    {:else}
      <p class="px-5 py-6 text-center text-xs text-black-700 dark:text-black-600">
        {#if loading}
          Reading the store…
        {:else if ov?.store_error}
          The store could not be read — see the banner above.
        {:else}
          This backend exposes no store numbers.
        {/if}
      </p>
    {/if}
  </section>

  <!-- Who uses it -->
  <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
    <div class="flex flex-wrap items-baseline justify-between gap-2 border-b border-white-300 dark:border-navy-600 px-5 py-3">
      <h2 class="text-sm font-medium text-black-900 dark:text-white-100">Provider instances using Agent Memory</h2>
      <span class="text-[0.6875rem] uppercase tracking-wider text-black-700 dark:text-black-600">
        {usedBy.length}
        {usedBy.length === 1 ? "instance" : "instances"}
      </span>
    </div>
    {#if usedBy.length === 0}
      <p class="px-5 py-6 text-center text-xs leading-relaxed text-black-700 dark:text-black-600">
        No provider instance has Agent Memory switched on, so nothing is being captured and nothing recalls.
        Turn it on per instance under <span class="font-medium text-black-900 dark:text-white-100">Providers</span>.
      </p>
    {:else}
      <ul class="divide-y divide-white-300 dark:divide-navy-600">
        {#each usedBy as ins (ins.type + "/" + ins.name)}
          {@const chip = captureChip(ins)}
          <li class="flex flex-wrap items-center gap-2 px-5 py-2.5">
            <span class="font-mono text-sm text-black-900 dark:text-white-100">{instanceLabel(ins)}</span>
            <span class={`rounded-full px-2 py-0.5 text-[0.6875rem] font-medium ${chip.cls}`} title={chip.title}>
              {chip.text}
            </span>
            {#if ins.server_url}
              <span
                class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-[0.6875rem] font-medium text-black-800 dark:text-black-600"
                title={`This instance talks to ${ins.server_url}, not the daemon shown above — the numbers on this page are not its numbers.`}
              >
                points elsewhere
              </span>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </section>
</div>
