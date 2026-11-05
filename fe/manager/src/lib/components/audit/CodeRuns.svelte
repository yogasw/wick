<script lang="ts">
  /* Admin audit of Code Mode script runs: one row per script, whatever door
     it came through (agent wick_run_code, CLI, watch code step, workflow
     node). The summary answers "is it used, by whom, where, and does it
     fail"; the top lists are clickable filters. Filters are URL-synced like
     AuditLog (?tab=codemode&user=&project=&via=&status=&script=&from=&to=). */
  import { Button, Select } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import { getCodeRuns } from "$lib/api.js";
  import type { CodeRun, CodeRunCount, CodeRunFilter, CodeRunResult } from "$lib/types.js";

  const viaOptions = [
    { label: "All doors", value: "" },
    { label: "Agent (wick_run_code)", value: "mcp" },
    { label: "Watch code step", value: "watch" },
    { label: "Workflow node", value: "workflow" },
    { label: "CLI", value: "cli" },
  ];
  const statusOptions = [
    { label: "All results", value: "" },
    { label: "OK", value: "ok" },
    { label: "Failed (any)", value: "failed" },
    { label: "Script error", value: "error" },
    { label: "Timeout", value: "timeout" },
    { label: "Runner crashed", value: "crashed" },
    { label: "Not started", value: "not_started" },
  ];
  const statusBadge: Record<string, string> = {
    ok: "bg-pos-100 text-pos-400",
    error: "bg-neg-100 text-neg-400",
    timeout: "bg-neg-100 text-neg-400",
    crashed: "bg-neg-100 text-neg-400",
    not_started: "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600",
  };
  const keys = ["user", "project", "via", "status", "script", "from", "to"] as const;

  let data = $state<CodeRunResult | null>(null);
  let loading = $state(true);
  let error = $state("");
  let filter = $state<CodeRunFilter>(filterFromUrl());
  /* Labels of the user/project picked from a top list, so the active-filter
     chips read as names instead of ids. */
  let labels = $state<Record<string, string>>({});
  let expanded = $state<string | null>(null);

  let runs = $derived(data?.runs ?? []);
  let sum = $derived(data?.summary);
  let hasFilters = $derived(keys.some((k) => filter[k]));
  let failRate = $derived(sum && sum.total > 0 ? (sum.failed / sum.total) * 100 : 0);

  function filterFromUrl(): CodeRunFilter {
    const q = new URLSearchParams(window.location.search);
    const page = Number(q.get("page") ?? "1");
    return {
      user: q.get("user") ?? "",
      project: q.get("project") ?? "",
      via: q.get("via") ?? "",
      status: q.get("status") ?? "",
      script: q.get("script") ?? "",
      from: q.get("from") ?? "",
      to: q.get("to") ?? "",
      page: Number.isFinite(page) && page > 0 ? page : 1,
    };
  }

  function syncFilterToUrl(f: CodeRunFilter): void {
    const params = new URLSearchParams();
    params.set("tab", "codemode");
    for (const k of keys) if (f[k]) params.set(k, f[k]);
    if (f.page > 1) params.set("page", String(f.page));
    const next = `${window.location.pathname}?${params}`;
    if (window.location.pathname + window.location.search !== next) history.replaceState({}, "", next);
  }

  function setFilter(patch: Partial<CodeRunFilter>): void {
    filter = { ...filter, ...patch, page: patch.page ?? 1 };
    syncFilterToUrl(filter);
    load(true);
  }

  function pick(key: "user" | "project" | "script", c: CodeRunCount): void {
    labels = { ...labels, [key]: c.label };
    setFilter({ [key]: c.key });
  }

  function clearFilters(): void {
    setFilter({ user: "", project: "", via: "", status: "", script: "", from: "", to: "" });
  }

  async function load(silent = false): Promise<void> {
    if (!silent) loading = true;
    try {
      data = await getCodeRuns(filter);
      error = "";
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (silent) toastError("Refresh failed", msg);
      else error = msg;
    } finally {
      if (!silent) loading = false;
    }
  }

  function relativeTime(iso: string): string {
    const t = Date.parse(iso);
    if (Number.isNaN(t)) return iso;
    const diff = Math.floor((Date.now() - t) / 1000);
    if (diff < 60) return `${diff}s ago`;
    if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
    if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
    return `${Math.floor(diff / 86400)}d ago`;
  }

  function ms(n: number): string {
    return n >= 1000 ? `${(n / 1000).toFixed(1)} s` : `${Math.round(n)} ms`;
  }

  function viaLabel(v: string): string {
    return viaOptions.find((o) => o.value === v)?.label ?? v;
  }

  function chipLabel(k: (typeof keys)[number]): string {
    const v = filter[k];
    if (k === "via") return viaLabel(v);
    if (k === "status") return statusOptions.find((o) => o.value === v)?.label ?? v;
    return labels[k] || v;
  }

  function rowUser(r: CodeRun): string {
    return r.user_id ? r.user_name || r.user_id : "system";
  }

  $effect(() => { load(); });
</script>

{#snippet topList(title: string, items: CodeRunCount[] | null | undefined, key: "user" | "project" | "script" | null)}
  <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
    <h3 class="text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">{title}</h3>
    {#if !items || items.length === 0}
      <p class="mt-2 text-xs text-black-700 dark:text-black-600">—</p>
    {:else}
      <ul class="mt-2 space-y-1.5">
        {#each items as c (c.key)}
          <li class="flex items-center gap-2 text-xs">
            {#if key}
              <button type="button" class="min-w-0 flex-1 truncate text-left text-black-900 dark:text-white-100 hover:text-green-600" title={c.label} onclick={() => pick(key, c)}>{c.label}</button>
            {:else}
              <span class="min-w-0 flex-1 truncate font-mono text-black-900 dark:text-white-100" title={c.label}>{c.label}</span>
            {/if}
            <span class="font-mono text-black-800 dark:text-black-600">{c.runs}</span>
            {#if c.failed > 0 && key}
              <span class="font-mono text-neg-400">{c.failed} failed</span>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
{/snippet}

<div class="space-y-6">
  <p class="text-sm text-black-800 dark:text-black-600">Every Code Mode script run: who ran it, from which project, through which door, and how it ended. The connector calls inside each run are in the Connector calls tab with source Code mode.</p>

  <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
    <div class="grid grid-cols-1 gap-3 sm:grid-cols-4">
      <div>
        <span class="block text-xs font-medium text-black-800 dark:text-black-600">Door</span>
        <div class="mt-1"><Select value={filter.via} options={viaOptions} onChange={(v) => setFilter({ via: v })} /></div>
      </div>
      <div>
        <span class="block text-xs font-medium text-black-800 dark:text-black-600">Result</span>
        <div class="mt-1"><Select value={filter.status} options={statusOptions} onChange={(v) => setFilter({ status: v })} /></div>
      </div>
      <div>
        <label for="cm-from" class="block text-xs font-medium text-black-800 dark:text-black-600">From</label>
        <input id="cm-from" type="date" value={filter.from} onchange={(e) => setFilter({ from: (e.target as HTMLInputElement).value })} class="mt-1 w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm text-black-900 dark:text-white-100 outline-none focus:border-green-500" />
      </div>
      <div>
        <label for="cm-to" class="block text-xs font-medium text-black-800 dark:text-black-600">To</label>
        <input id="cm-to" type="date" value={filter.to} onchange={(e) => setFilter({ to: (e.target as HTMLInputElement).value })} class="mt-1 w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm text-black-900 dark:text-white-100 outline-none focus:border-green-500" />
      </div>
    </div>
    {#if hasFilters}
      <div class="mt-3 flex flex-wrap items-center gap-2">
        {#each keys.filter((k) => filter[k]) as k (k)}
          <button type="button" class="inline-flex items-center gap-1 rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs text-black-900 dark:text-white-100 hover:bg-white-400 dark:hover:bg-navy-500" onclick={() => setFilter({ [k]: "" })} title="Remove filter">
            <span class="text-black-700 dark:text-black-600">{k}:</span> {chipLabel(k)} <span aria-hidden="true">×</span>
          </button>
        {/each}
        <button type="button" class="text-xs font-medium text-green-600 hover:underline" onclick={clearFilters}>Reset filters</button>
      </div>
    {/if}
  </section>

  {#if loading}
    <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if data && sum}
    <section class="grid grid-cols-2 gap-3 sm:grid-cols-4">
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <div class="text-xs text-black-800 dark:text-black-600">Runs</div>
        <div class="mt-1 text-2xl font-semibold text-black-900 dark:text-white-100">{sum.total}</div>
        <div class="mt-1 text-xs text-black-700 dark:text-black-600">{sum.calls} connector calls</div>
      </div>
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <div class="text-xs text-black-800 dark:text-black-600">Failed</div>
        <div class="mt-1 text-2xl font-semibold {sum.failed > 0 ? 'text-neg-400' : 'text-black-900 dark:text-white-100'}">{sum.failed} <span class="text-sm font-normal text-black-700 dark:text-black-600">({failRate.toFixed(1)}%)</span></div>
        <div class="mt-1 text-xs text-black-700 dark:text-black-600">
          {#each Object.entries(sum.by_status ?? {}).filter(([s]) => s !== "ok") as [s, n] (s)}
            <button type="button" class="mr-2 hover:text-green-600" onclick={() => setFilter({ status: s })}>{s} {n}</button>
          {:else}
            no failures
          {/each}
        </div>
      </div>
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <div class="text-xs text-black-800 dark:text-black-600">Avg duration</div>
        <div class="mt-1 text-2xl font-semibold text-black-900 dark:text-white-100">{ms(sum.avg_duration_ms)}</div>
      </div>
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <div class="text-xs text-black-800 dark:text-black-600">Connector call errors</div>
        <div class="mt-1 text-2xl font-semibold {sum.call_errors > 0 ? 'text-neg-400' : 'text-black-900 dark:text-white-100'}">{sum.call_errors}</div>
        <div class="mt-1 text-xs text-black-700 dark:text-black-600">inside scripts</div>
      </div>
    </section>

    <section class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">
      {@render topList("Top users", sum.top_users, "user")}
      {@render topList("Top projects", sum.top_projects, "project")}
      {@render topList("Top scripts", sum.top_scripts, "script")}
      {@render topList("Top errors", sum.top_errors, null)}
    </section>

    {#if runs.length === 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-8 text-center">
        <p class="text-sm text-black-700 dark:text-black-600">No script runs match the current filter.</p>
      </div>
    {:else}
      <section>
        <div class="overflow-x-auto rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Script</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Door</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Result</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">User</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Project</th>
                <th class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Calls</th>
                <th class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Duration</th>
                <th class="px-4 py-3 text-left text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Time</th>
              </tr>
            </thead>
            <tbody>
              {#each runs as run (run.id)}
                <tr class="border-b border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-800 {run.error ? 'cursor-pointer' : ''}" onclick={() => { if (run.error) expanded = expanded === run.id ? null : run.id; }}>
                  <td class="max-w-xs truncate px-4 py-3 font-mono text-xs text-black-900 dark:text-white-100" title={run.script}>{run.script}</td>
                  <td class="px-4 py-3 text-xs"><span class="inline-flex items-center rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 font-medium text-black-700 dark:text-black-600">{run.via}</span></td>
                  <td class="px-4 py-3 text-xs"><span class="rounded-full px-2 py-0.5 font-medium {statusBadge[run.status] ?? statusBadge.error}">{run.status}</span></td>
                  <td class="px-4 py-3 text-xs text-black-800 dark:text-black-600">{rowUser(run)}</td>
                  <td class="px-4 py-3 text-xs text-black-800 dark:text-black-600" title={run.session_id}>{run.project_name || run.project_id || "—"}</td>
                  <td class="px-4 py-3 text-right font-mono text-xs text-black-800 dark:text-black-600">{run.calls}{#if run.call_errors > 0}<span class="text-neg-400">&nbsp;({run.call_errors} err)</span>{/if}</td>
                  <td class="px-4 py-3 text-right font-mono text-xs text-black-800 dark:text-black-600">{ms(run.duration_ms)}</td>
                  <td class="px-4 py-3 text-xs text-black-800 dark:text-black-600"><span title={run.started_at}>{relativeTime(run.started_at)}</span></td>
                </tr>
                {#if run.error && expanded === run.id}
                  <tr class="border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
                    <td colspan="8" class="px-4 py-3">
                      <pre class="whitespace-pre-wrap break-words font-mono text-xs text-neg-400">{run.error}</pre>
                      <div class="mt-2 font-mono text-xs text-black-700 dark:text-black-600">run {run.id} · session {run.session_id || "—"} · {run.lang}{run.runner ? ` · ${run.runner}` : ""}{run.queued_ms > 0 ? ` · queued ${ms(run.queued_ms)}` : ""}</div>
                    </td>
                  </tr>
                {:else if run.error}
                  <tr class="cursor-pointer border-b border-white-300 dark:border-navy-600" onclick={() => (expanded = run.id)}>
                    <td colspan="8" class="max-w-0 truncate px-4 pb-2 pt-0 font-mono text-xs text-neg-400" title="Click the row for the full error">{run.error}</td>
                  </tr>
                {/if}
              {/each}
            </tbody>
          </table>
        </div>

        {#if data.total_pages > 1}
          <nav class="mt-3 flex items-center justify-center gap-1 text-xs">
            <Button variant="secondary" size="sm" disabled={data.page <= 1} onclick={() => setFilter({ page: data!.page - 1 })}>← Prev</Button>
            <span class="px-3 py-1.5 text-black-800 dark:text-black-600">Page {data.page} of {data.total_pages}</span>
            <Button variant="secondary" size="sm" disabled={data.page >= data.total_pages} onclick={() => setFilter({ page: data!.page + 1 })}>Next →</Button>
          </nav>
        {/if}
      </section>
    {/if}
  {/if}
</div>
