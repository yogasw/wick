<script lang="ts">
  import { onMount, onDestroy } from "svelte";
  import { Effect } from "effect";
  import { ToastHost } from "@wick-fe/common-ui";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { fetchReportE, fetchSeriesE, applySuggestedE } from "$lib/api.js";
  import Sparkline from "$lib/Sparkline.svelte";
  import TopTable from "$lib/TopTable.svelte";
  import ProcessExplorer from "$lib/ProcessExplorer.svelte";
  import WrapperPanel from "$lib/WrapperPanel.svelte";
  import { humanBytes, humanBps, humanPct, humanDuration, clockTime, pctOf } from "$lib/format.js";
  import type { MemoryReport, SeriesResponse } from "$lib/types.js";

  const base: string = (document.getElementById("app")?.dataset.base ?? "").replace(/\/$/, "");

  let report = $state<MemoryReport | null>(null);
  let series = $state<SeriesResponse | null>(null);
  let loadError = $state("");
  let applying = $state(false);
  let windowMinutes = $state(30);

  // Live refresh. The sampler writes on its own interval server-side, so
  // polling faster than that only redraws the same points; 10s keeps the
  // "now" row current without hammering /proc.
  let timer: ReturnType<typeof setInterval> | null = null;

  // The HttpClient layer is provided here, at the edge — the api module
  // stays layer-agnostic so its tests can swap in a mock.
  async function load(): Promise<void> {
    try {
      const [r, s] = await Promise.all([
        Effect.runPromise(fetchReportE(base).pipe(Effect.provide(WickClientLayer))),
        Effect.runPromise(fetchSeriesE(base, windowMinutes).pipe(Effect.provide(WickClientLayer))),
      ]);
      report = r;
      series = s;
      loadError = "";
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  }

  onMount(() => {
    void load();
    timer = setInterval(() => void load(), 10_000);
  });
  onDestroy(() => {
    if (timer) clearInterval(timer);
  });

  async function onWindowChange(m: number): Promise<void> {
    windowMinutes = m;
    await load();
  }

  async function onApply(): Promise<void> {
    applying = true;
    try {
      await Effect.runPromise(applySuggestedE(base).pipe(Effect.provide(WickClientLayer)));
      toastOk("Suggested limits applied. The guard mode was left unchanged.");
      await load();
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      applying = false;
    }
  }

  // Machine-wide series, split per metric for the charts.
  const machineMem = $derived(series?.machine.map((m) => m.agent_bytes) ?? []);
  const machineCPU = $derived(series?.machine.map((m) => m.agent_cpu_pct) ?? []);
  const machineProcs = $derived(series?.machine.map((m) => m.agent_procs) ?? []);
  // Parallel to the series above, so the hover readout can name the moment
  // a spike happened rather than just its height.
  const machineTimes = $derived(series?.machine.map((m) => m.at) ?? []);

  // Machine-wide series. The agent charts are blank whenever no agent is
  // running — which is exactly when someone is asking why the box is
  // slow — so these are recorded and drawn alongside them.
  const boxMem = $derived(series?.machine.map((m) => m.machine_used_bytes) ?? []);
  const boxCPU = $derived(series?.machine.map((m) => m.machine_cpu_pct) ?? []);
  const boxProcs = $derived(series?.machine.map((m) => m.machine_procs) ?? []);
  // Any agent activity in the window at all? Used to decide whether the
  // agent chart is worth the vertical space on a machine that has none.
  const hasAgentSamples = $derived(
    (series?.machine ?? []).some((m) => m.agent_procs > 0 || m.agent_bytes > 0),
  );

  // CPU is percent of ONE core, so a busy 16-core box legitimately reads
  // 444%. Naming the ceiling in the label stops that looking like a bug.
  const cpuLabel = $derived(
    (report?.cpu_cores ?? 0) > 1 ? `CPU · max ${report!.cpu_cores * 100}%` : "CPU",
  );
  // Same shape for memory: state the ceiling so the plotted figure has a
  // stated denominator rather than an assumed one.
  const memLabel = $derived(
    (report?.total_bytes ?? 0) > 0 ? `Memory · of ${humanBytes(report!.total_bytes!)}` : "Memory",
  );

  // The window buttons look inert when the buffer holds less than the
  // selected span — every option renders the same few minutes. Naming the
  // span actually held makes the difference between "not working" and
  // "nothing older recorded yet" visible.
  const spanLabel = $derived.by(() => {
    const st = report?.history;
    if (!st || st.machine_points === 0) return "";
    return humanDuration(st.span_sec);
  });

  // Which agent rows have their process list expanded. Keyed by pid so a
  // refresh that reorders rows keeps the right one open.
  let expanded = $state<Set<number>>(new Set());

  function toggleProcesses(pid: number): void {
    const next = new Set(expanded);
    if (next.has(pid)) {
      next.delete(pid);
    } else {
      next.add(pid);
    }
    expanded = next;
  }

  // Graded server-side (percentage AND absolute free together), because
  // percentage alone cries wolf: a 328 GB disk at 93% still has 22 GB
  // free and nothing is about to fail.
  const diskTone = $derived.by(() => {
    switch (report?.disk?.pressure) {
      case "full":
        return "bg-red-600";
      case "warn":
        return "bg-yellow-500";
      default:
        return "bg-blue-600";
    }
  });

  // Per-agent series keyed by pid, so each row can draw its own history.
  const perAgent = $derived.by(() => {
    const out = new Map<number, number[]>();
    for (const s of series?.agents ?? []) {
      const cur = out.get(s.pid) ?? [];
      cur.push(s.rss_bytes);
      out.set(s.pid, cur);
    }
    return out;
  });

  const perAgentCPU = $derived.by(() => {
    const out = new Map<number, number[]>();
    for (const s of series?.agents ?? []) {
      const cur = out.get(s.pid) ?? [];
      cur.push(s.cpu_pct);
      out.set(s.pid, cur);
    }
    return out;
  });

  const usedBytes = $derived(
    report && report.total_bytes && report.available_bytes
      ? report.total_bytes - report.available_bytes
      : 0,
  );

  const agentTotal = $derived(
    (report?.agents ?? []).reduce((sum, a) => sum + a.tree_bytes, 0),
  );

  const modeBadge = $derived.by(() => {
    switch (report?.mode) {
      case "enforce":
        return { text: "enforcing", cls: "bg-green-100 text-green-700 dark:bg-green-900 dark:text-green-300" };
      case "measure":
        return { text: "measuring only", cls: "bg-blue-100 text-blue-700 dark:bg-blue-900 dark:text-blue-300" };
      default:
        return { text: "off", cls: "bg-white-300 text-black-700 dark:bg-navy-600 dark:text-black-600" };
    }
  });

  const historyLabel = $derived.by(() => {
    const st = report?.history;
    if (!st || st.machine_points === 0) return "no samples recorded yet";
    return `${humanDuration(st.span_sec)} of history · ${st.machine_points} points · kept ${humanDuration(st.retention_sec)}`;
  });
</script>

<ToastHost />

<div class="min-h-screen space-y-6 p-6">
  <div class="flex flex-wrap items-center justify-between gap-3">
    <div>
      <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Resources</h1>
      <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">{historyLabel}</p>
    </div>

    <div class="flex items-center gap-2">
      <span class="rounded-full px-2.5 py-1 text-xs font-medium {modeBadge.cls}">
        Guard: {modeBadge.text}
      </span>
      <div class="flex overflow-hidden rounded-lg border border-white-300 dark:border-navy-600">
        {#each [15, 30, 120, 360] as m (m)}
          {@const beyondBuffer = (report?.history?.span_sec ?? 0) < m * 60}
          <button
            type="button"
            class="px-2.5 py-1 text-xs transition-colors {windowMinutes === m
              ? 'bg-blue-600 text-white-100'
              : 'bg-white-100 text-black-700 hover:bg-white-200 dark:bg-navy-700 dark:text-black-600 dark:hover:bg-navy-600'} {beyondBuffer &&
            windowMinutes !== m
              ? 'opacity-50'
              : ''}"
            title={beyondBuffer
              ? `Only ${spanLabel} recorded so far — this window shows everything there is`
              : ""}
            onclick={() => void onWindowChange(m)}
          >
            {m < 60 ? `${m}m` : `${m / 60}h`}
          </button>
        {/each}
      </div>
    </div>
  </div>

  {#if loadError}
    <div
      class="rounded-xl border border-red-300 bg-red-50 p-4 text-sm text-red-700 dark:border-red-800 dark:bg-red-950 dark:text-red-300"
    >
      Could not load usage data: {loadError}
    </div>
  {/if}

  {#if report?.notice}
    <!-- Degrading silently is how an operator ends up believing they are
         protected when nothing is enforcing anything. -->
    <div
      class="rounded-xl border border-yellow-300 bg-yellow-50 p-4 text-sm text-yellow-800 dark:border-yellow-800 dark:bg-yellow-950 dark:text-yellow-300"
    >
      {report.notice}
    </div>
  {/if}

  {#if report && !report.processes_readable}
    <div
      class="rounded-xl border border-white-300 bg-white-100 p-4 text-sm text-black-700 dark:border-navy-600 dark:bg-navy-700 dark:text-black-600"
    >
      Process listing is unavailable on this platform. The limits below can still be
      configured, and they apply wherever this wick instance runs its agents.
    </div>
  {/if}

  <!-- Coverage. Above the agent tables on purpose: the number that
       matters is how many processes have NO ceiling, and reading that
       after scrolling past a list of healthy rows is reading it too
       late. -->
  <WrapperPanel {base} suggestedMB={report?.suggested?.AgentMaxMB ?? 0} />

  <!-- Suggested limits -->
  {#if report?.machine_known}
    <div class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Suggested limits</h2>
          <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
            Derived from this machine's memory and the peaks recorded above, with headroom.
            Applying them fills in the numbers only — it does not switch enforcement on.
          </p>
        </div>
        <button
          type="button"
          class="rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-medium text-white-100 transition-colors hover:bg-blue-700 disabled:opacity-50"
          disabled={applying}
          onclick={() => void onApply()}
        >
          {applying ? "Applying…" : "Apply suggested values"}
        </button>
      </div>

      <div class="mt-4 grid gap-3 sm:grid-cols-4">
        {#each [{ label: "Per agent", now: report.current.agent_memory_max_mb, next: report.suggested.AgentMaxMB }, { label: "All agents", now: report.current.agents_total_memory_mb, next: report.suggested.AgentsTotalMB }, { label: "Tool commands", now: report.current.tool_memory_max_mb, next: report.suggested.ToolMaxMB }, { label: "Keep free", now: report.current.min_free_memory_mb, next: report.suggested.MinFreeMB }] as row (row.label)}
          <div class="rounded-lg border border-white-300 p-3 dark:border-navy-600">
            <p class="text-xs text-black-700 dark:text-black-600">{row.label}</p>
            <p class="mt-1 text-sm tabular-nums text-black-900 dark:text-white-100">
              {row.now ? `${row.now} MB` : "none"}
              <span class="text-black-700 dark:text-black-600">→</span>
              <span class="font-semibold">{row.next} MB</span>
            </p>
          </div>
        {/each}
      </div>
    </div>
  {/if}

  <!-- Per-agent table -->
  <div class="overflow-hidden rounded-xl border border-white-300 bg-white-100 shadow-sm dark:border-navy-600 dark:bg-navy-700">
    <div class="border-b border-white-300 px-5 py-4 dark:border-navy-600">
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Running agents</h2>
    </div>

    {#if (report?.agents.length ?? 0) === 0}
      <p class="px-5 py-8 text-center text-sm text-black-700 dark:text-black-600">
        No agent processes running.
      </p>
    {:else}
      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-white-300 text-left text-xs uppercase tracking-wide text-black-700 dark:border-navy-600 dark:text-black-600">
              <th class="px-5 py-2 font-medium">Agent</th>
              <th class="px-5 py-2 font-medium">Memory</th>
              <th class="px-5 py-2 font-medium">Trend</th>
              <th class="px-5 py-2 font-medium">CPU</th>
              <th class="px-5 py-2 font-medium">Disk</th>
              <th class="px-5 py-2 font-medium">Heaviest child</th>
            </tr>
          </thead>
          <tbody>
            {#each report?.agents ?? [] as a (a.pid)}
              <tr class="border-b border-white-300 last:border-0 dark:border-navy-600">
                <td class="px-5 py-3">
                  <div class="flex items-center gap-1.5">
                    <span class="font-medium text-black-900 dark:text-white-100">{a.name}</span>
                    <!-- Read from the cgroup, not from the configured
                         mode: an agent can be uncovered while the guard
                         reads "enforce", and without this the row looks
                         identical either way. -->
                    {#if a.isolated}
                      <span
                        class="rounded border border-green-600/30 px-1 py-px text-[10px] text-green-700 dark:text-green-500"
                        title="A memory ceiling applies to this agent."
                      >
                        limited
                      </span>
                    {:else}
                      <span
                        class="rounded border border-amber-600/30 px-1 py-px text-[10px] text-amber-700 dark:text-amber-500"
                        title="No memory ceiling applies to this agent. It can take the machine down with it."
                      >
                        no limit
                      </span>
                    {/if}
                  </div>
                  {#if (a.processes?.length ?? 0) > 0}
                    <button
                      type="button"
                      class="mt-0.5 flex items-center gap-1 text-xs text-blue-600 hover:underline dark:text-blue-400"
                      onclick={() => toggleProcesses(a.pid)}
                      aria-expanded={expanded.has(a.pid)}
                    >
                      <span class="inline-block transition-transform {expanded.has(a.pid) ? 'rotate-90' : ''}">›</span>
                      pid {a.pid} · {a.procs} proc{a.procs === 1 ? "" : "s"}
                    </button>
                  {:else}
                    <div class="text-xs text-black-700 dark:text-black-600">
                      pid {a.pid} · {a.procs} proc{a.procs === 1 ? "" : "s"}
                    </div>
                  {/if}
                </td>
                <td class="px-5 py-3">
                  <div class="flex items-baseline gap-2">
                    <span class="tabular-nums text-black-900 dark:text-white-100">
                      {humanBytes(a.tree_bytes)}
                    </span>
                    {#if (report?.total_bytes ?? 0) > 0}
                      <!-- Share of the machine, same as the explorer: a
                           byte count alone cannot say whether it is a lot. -->
                      <span class="text-xs tabular-nums text-black-700 dark:text-black-600">
                        {((a.tree_bytes / report!.total_bytes!) * 100).toFixed(1)}%
                      </span>
                    {/if}
                  </div>
                  {#if a.peak_bytes}
                    <div class="text-xs text-black-700 dark:text-black-600">
                      peak {humanBytes(a.peak_bytes)}
                    </div>
                  {/if}
                </td>
                <td class="w-40 px-5 py-3">
                  <Sparkline
                    points={perAgent.get(a.pid) ?? []}
                    height={28}
                    color="#3b82f6"
                    format={humanBytes}
                  />
                </td>
                <td class="px-5 py-3">
                  <div class="tabular-nums text-black-900 dark:text-white-100">
                    {humanPct(a.cpu_pct)}
                  </div>
                  {#if (perAgentCPU.get(a.pid)?.length ?? 0) > 1}
                    <div class="w-24">
                      <Sparkline
                        points={perAgentCPU.get(a.pid) ?? []}
                        height={20}
                        color="#f59e0b"
                        format={(v) => `${v.toFixed(0)}%`}
                      />
                    </div>
                  {/if}
                </td>
                <td class="px-5 py-3 text-xs tabular-nums text-black-700 dark:text-black-600">
                  <div>↓ {humanBps(a.io_read_bps)}</div>
                  <div>↑ {humanBps(a.io_write_bps)}</div>
                </td>
                <td class="px-5 py-3 text-xs text-black-700 dark:text-black-600">
                  {#if a.largest_name}
                    <span class="font-medium text-black-900 dark:text-white-100">{a.largest_name}</span>
                    {humanBytes(a.largest_bytes ?? 0)}
                  {:else}
                    —
                  {/if}
                </td>
              </tr>

              {#if expanded.has(a.pid)}
                <!-- The task-manager view: the row above says how much this
                     agent uses, this says which process is using it.
                     Member rows share the parent table's columns rather
                     than nesting a second table, so the numbers line up
                     with the row they belong to. -->
                {#each a.processes ?? [] as p (p.pid)}
                  <tr class="border-b border-white-300 bg-white-200/50 text-xs dark:border-navy-600 dark:bg-navy-800/40">
                    <td class="py-1 pl-9 pr-5">
                      <span class="text-black-900 dark:text-white-100">{p.name}</span>
                      <span class="ml-1 tabular-nums text-black-700 dark:text-black-600">
                        pid {p.pid}
                      </span>
                    </td>
                    <td class="px-5 py-1 tabular-nums text-black-900 dark:text-white-100">
                      {humanBytes(p.rss_bytes)}
                    </td>
                    <td class="px-5 py-1"></td>
                    <td class="px-5 py-1"></td>
                    <td class="px-5 py-1"></td>
                    <td class="px-5 py-1"></td>
                  </tr>
                {/each}
                {#if a.procs > (a.processes?.length ?? 0)}
                  <tr class="border-b border-white-300 bg-white-200/50 dark:border-navy-600 dark:bg-navy-800/40">
                    <td colspan="6" class="py-1 pl-9 pr-5 text-xs text-black-700 dark:text-black-600">
                      Showing the {a.processes?.length} largest of {a.procs} processes.
                    </td>
                  </tr>
                {/if}
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>

  <!-- Machine summary -->
  <div class="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
    <div class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700">
      <p class="text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">
        Machine memory
      </p>
      {#if report?.machine_known}
        <p class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100">
          {humanBytes(usedBytes)}
          <span class="text-sm font-normal text-black-700 dark:text-black-600">
            / {humanBytes(report.total_bytes ?? 0)}
          </span>
        </p>
        <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-white-300 dark:bg-navy-600">
          <div
            class="h-full rounded-full bg-blue-600"
            style="width: {pctOf(usedBytes, report.total_bytes ?? 0)}%"
          ></div>
        </div>
        <p class="mt-1 text-xs text-black-700 dark:text-black-600">
          {humanBytes(report.available_bytes ?? 0)} available
        </p>
      {:else}
        <p class="mt-1 text-sm text-black-700 dark:text-black-600">unknown on this platform</p>
      {/if}
    </div>

    <div class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700">
      <p class="text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">
        Agents now
      </p>
      <p class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100">
        {humanBytes(agentTotal)}
      </p>
      <p class="mt-1 text-xs text-black-700 dark:text-black-600">
        {report?.agents.length ?? 0} running ·
        {(report?.agents ?? []).reduce((n, a) => n + a.procs, 0)} processes
      </p>
    </div>

    <!-- Disk capacity. Distinct from the per-agent IO rates below: a busy
         disk is slow, a FULL disk fails writes outright, and wick writes
         continuously (transcripts, spawn logs, trace events). -->
    <div class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700">
      <p class="text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">
        Disk
      </p>
      {#if report?.disk?.known}
        <p class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100">
          {humanBytes(report.disk.used_bytes)}
          <span class="text-sm font-normal text-black-700 dark:text-black-600">
            / {humanBytes(report.disk.total_bytes)}
          </span>
        </p>
        <div class="mt-2 h-1.5 overflow-hidden rounded-full bg-white-300 dark:bg-navy-600">
          <div class="h-full rounded-full {diskTone}" style="width: {report.disk.used_pct}%"></div>
        </div>
        <p class="mt-1 truncate text-xs text-black-700 dark:text-black-600" title={report.disk.path}>
          {humanBytes(report.disk.avail_bytes)} free · {report.disk.path}
        </p>
      {:else}
        <p class="mt-1 text-sm text-black-700 dark:text-black-600">unknown on this platform</p>
      {/if}
    </div>

    <div class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700">
      <p class="text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">
        Per-agent limit
      </p>
      <p class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100">
        {report?.current.agent_memory_max_mb ? `${report.current.agent_memory_max_mb} MB` : "none"}
      </p>
      <p class="mt-1 text-xs text-black-700 dark:text-black-600">
        combined {report?.current.agents_total_memory_mb
          ? `${report.current.agents_total_memory_mb} MB`
          : "unlimited"}
      </p>
    </div>
  </div>

  <!-- Trends -->
  <div class="rounded-xl border border-white-300 bg-white-100 p-5 shadow-sm dark:border-navy-600 dark:bg-navy-700">
    <h2 class="mb-4 text-sm font-semibold text-black-900 dark:text-white-100">
      Over time
      {#if spanLabel && (report?.history?.span_sec ?? 0) < windowMinutes * 60}
        <!-- Without this the window buttons look broken: every option
             renders the same few minutes because that is all there is. -->
        <span class="ml-1 text-xs font-normal text-black-700 dark:text-black-600">
          — only {spanLabel} recorded so far
        </span>
      {/if}
    </h2>
    {#if series && !series.enabled}
      <p class="text-sm text-black-700 dark:text-black-600">
        Usage history is switched off, so only the live snapshot above is available. Turn on
        <span class="font-medium">Usage History</span> in Agents settings to record trends.
      </p>
    {:else}
      <!-- Whole machine first: this is the row that always has data, and
           it gives the agent row below it a denominator. -->
      <p class="mb-2 text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">
        Whole machine
      </p>
      <div class="grid gap-5 sm:grid-cols-3">
        <Sparkline
          points={boxMem}
          times={machineTimes}
          label={memLabel}
          color="#3b82f6"
          format={humanBytes}
          max={report?.total_bytes}
        />
        <Sparkline
          points={boxCPU}
          times={machineTimes}
          label={cpuLabel}
          color="#f59e0b"
          format={(v) => `${v.toFixed(0)}%`}
        />
        <Sparkline
          points={boxProcs}
          times={machineTimes}
          label="Processes"
          color="#10b981"
          format={(v) => String(Math.round(v))}
        />
      </div>

      <p class="mb-2 mt-5 text-xs font-medium uppercase tracking-wide text-black-700 dark:text-black-600">
        Agents only
        {#if !hasAgentSamples}
          <span class="ml-1 normal-case tracking-normal text-black-600 dark:text-black-700">
            — none ran in this window
          </span>
        {/if}
      </p>
      <div class="grid gap-5 sm:grid-cols-3">
        <Sparkline
          points={machineMem}
          times={machineTimes}
          label="Memory"
          color="#3b82f6"
          format={humanBytes}
          max={report?.total_bytes}
        />
        <Sparkline
          points={machineCPU}
          times={machineTimes}
          label="CPU"
          color="#f59e0b"
          format={(v) => `${v.toFixed(0)}%`}
        />
        <Sparkline
          points={machineProcs}
          times={machineTimes}
          label="Processes"
          color="#10b981"
          format={(v) => String(Math.round(v))}
        />
      </div>
      {#if series && series.machine.length > 1}
        <div class="mt-2 flex justify-between text-xs text-black-700 dark:text-black-600">
          <span>{clockTime(series.machine[0].at)}</span>
          <span>{clockTime(series.machine[series.machine.length - 1].at)}</span>
        </div>
      {/if}
    {/if}
  </div>

  <!-- Machine-wide top processes. Deliberately not scoped to wick: when
       the box is slow the cause is frequently not an agent, and a
       dashboard that can only see its own processes cannot say so. -->
  {#if report?.top?.available}
    <div>
      <div class="mb-3 flex items-baseline justify-between">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">
          Top processes on this machine
        </h2>
        <span class="text-xs text-black-700 dark:text-black-600">
          {report.top.total} processes
        </span>
      </div>
      <div class="grid gap-4 lg:grid-cols-3">
        <TopTable
          title="By memory"
          rows={report.top.by_memory}
          metric="memory"
          barColor="#3b82f6"
          machineTotal={report.total_bytes ?? 0}
        />
        <TopTable
          title="By CPU"
          rows={report.top.by_cpu}
          metric="cpu"
          barColor="#f59e0b"
          emptyText="no CPU activity yet — rates need a second sample"
        />
        <TopTable
          title="By disk"
          rows={report.top.by_io}
          metric="io"
          barColor="#8b5cf6"
          emptyText="no disk activity yet — rates need a second sample"
        />
      </div>
    </div>
  {/if}

  <!-- The full, searchable list. Fetched on its own, not on the 10s poll:
       ~350 processes on every refresh would be most of the payload for a
       table the operator is usually not reading. -->
  {#if report?.top?.available}
    <ProcessExplorer {base} />
  {/if}

</div>
