<script lang="ts">
  /* Admin-only cross-connector run history, ported from audit_log.templ.
     Source/status/from/to filters + server-side pagination, URL-synced via
     ?source=&status=&from=&to=&page= (replaceState, no SPA navigation) so
     links stay shareable, then re-fetched. Each filter change resets to page
     1. Reuses common-ui Select/Button + the resolved /manager/api/runs JSON
     (connector + user names + summary in one call). */
  import { Button, Select } from "@wick-fe/common-ui";
  import { push } from "$lib/router.js";
  import { getAuditRuns } from "$lib/api.js";
  import type { AuditResult, AuditFilter, AuditRun } from "$lib/types.js";

  const sourceOptions = [
    { label: "All sources", value: "" },
    { label: "MCP", value: "mcp" },
    { label: "Test panel", value: "test" },
    { label: "Retry", value: "retry" },
  ];
  const statusOptions = [
    { label: "All statuses", value: "" },
    { label: "Success", value: "success" },
    { label: "Error", value: "error" },
    { label: "Running", value: "running" },
  ];

  let data = $state<AuditResult | null>(null);
  let loading = $state(true);
  let error = $state("");
  let filter = $state<AuditFilter>(filterFromUrl());

  let runs = $derived(data?.runs ?? []);
  let hasFilters = $derived(!!(filter.source || filter.status || filter.from || filter.to));

  function filterFromUrl(): AuditFilter {
    let q: URLSearchParams;
    try {
      q = new URLSearchParams(window.location.search);
    } catch {
      q = new URLSearchParams();
    }
    const page = Number(q.get("page") ?? "1");
    return {
      source: q.get("source") ?? "",
      status: q.get("status") ?? "",
      from: q.get("from") ?? "",
      to: q.get("to") ?? "",
      page: Number.isFinite(page) && page > 0 ? page : 1,
    };
  }

  function syncFilterToUrl(f: AuditFilter): void {
    const params = new URLSearchParams();
    if (f.source) params.set("source", f.source);
    if (f.status) params.set("status", f.status);
    if (f.from) params.set("from", f.from);
    if (f.to) params.set("to", f.to);
    if (f.page > 1) params.set("page", String(f.page));
    const qs = params.toString();
    const next = qs ? `${window.location.pathname}?${qs}` : window.location.pathname;
    if (window.location.pathname + window.location.search !== next) {
      history.replaceState({}, "", next);
    }
  }

  function setFilter(patch: Partial<AuditFilter>): void {
    filter = { ...filter, ...patch, page: patch.page ?? 1 };
    syncFilterToUrl(filter);
    load();
  }

  function clearFilters(): void {
    setFilter({ source: "", status: "", from: "", to: "" });
  }

  function gotoPage(page: number): void {
    filter = { ...filter, page };
    syncFilterToUrl(filter);
    load();
  }

  async function load(): Promise<void> {
    loading = true;
    error = "";
    try {
      data = await getAuditRuns(filter);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
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

  function connectorLabel(run: AuditRun): string {
    return run.connector_name || run.connector_id;
  }

  function userLabel(run: AuditRun): string {
    if (!run.user_id) return "system";
    return run.user_name || run.user_id;
  }

  function openConnector(run: AuditRun): void {
    if (run.connector_key && run.connector_id) {
      push(`/connectors/${encodeURIComponent(run.connector_key)}/${encodeURIComponent(run.connector_id)}`);
    }
  }

  const statusBadge: Record<string, string> = {
    success: "bg-pos-100 text-pos-400",
    error: "bg-neg-100 text-neg-400",
    running: "bg-prog-100 text-prog-400",
  };

  $effect(() => { load(); });
</script>

<div class="space-y-6">
  <div>
    <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Audit Log</h1>
    <p class="mt-1 text-sm text-black-800 dark:text-black-600">Cross-connector run history — MCP calls, panel tests, and retries across every connector instance.</p>
  </div>

  <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
    <div class="grid grid-cols-1 gap-3 sm:grid-cols-4">
      <div>
        <label for="audit-source" class="block text-xs font-medium text-black-800 dark:text-black-600">Source</label>
        <div class="mt-1"><Select value={filter.source} options={sourceOptions} onChange={(v) => setFilter({ source: v })} /></div>
      </div>
      <div>
        <label for="audit-status" class="block text-xs font-medium text-black-800 dark:text-black-600">Status</label>
        <div class="mt-1"><Select value={filter.status} options={statusOptions} onChange={(v) => setFilter({ status: v })} /></div>
      </div>
      <div>
        <label for="audit-from" class="block text-xs font-medium text-black-800 dark:text-black-600">From</label>
        <input id="audit-from" type="date" value={filter.from} onchange={(e) => setFilter({ from: (e.target as HTMLInputElement).value })} aria-label="From" class="mt-1 w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm text-black-900 dark:text-white-100 outline-none focus:border-green-500" />
      </div>
      <div>
        <label for="audit-to" class="block text-xs font-medium text-black-800 dark:text-black-600">To</label>
        <input id="audit-to" type="date" value={filter.to} onchange={(e) => setFilter({ to: (e.target as HTMLInputElement).value })} aria-label="To" class="mt-1 w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm text-black-900 dark:text-white-100 outline-none focus:border-green-500" />
      </div>
    </div>
    {#if hasFilters}
      <div class="mt-3">
        <button type="button" class="text-xs font-medium text-green-600 hover:underline" onclick={clearFilters}>Reset filters</button>
      </div>
    {/if}
  </section>

  {#if loading}
    <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if data}
    <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-black-700 dark:text-black-600">
      <span>{data.total} run(s) found</span>
      <span>· {data.summary.succeeded} ok</span>
      <span>· {data.summary.errored} error</span>
      <span>· avg {Math.round(data.summary.avg_latency_ms)} ms</span>
    </div>

    {#if runs.length === 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-8 text-center">
        <p class="text-sm text-black-700 dark:text-black-600">No runs match the current filter.</p>
      </div>
    {:else}
      <section>
        <div class="overflow-hidden rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
                <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Connector</th>
                <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Operation</th>
                <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Source</th>
                <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Status</th>
                <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">User</th>
                <th class="px-4 py-3 text-right font-medium text-black-800 dark:text-black-600">Latency</th>
                <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Time</th>
              </tr>
            </thead>
            <tbody>
              {#each runs as run (run.id)}
                <tr class="border-b border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-800">
                  <td class="px-4 py-3 text-black-900 dark:text-white-100">
                    {#if run.connector_key}
                      <button type="button" class="hover:text-green-600" onclick={() => openConnector(run)}>{connectorLabel(run)}</button>
                    {:else}
                      <span class="font-mono text-xs text-black-800 dark:text-black-600">{connectorLabel(run)}</span>
                    {/if}
                  </td>
                  <td class="px-4 py-3 font-mono text-xs text-black-900 dark:text-white-100">{run.operation_key}</td>
                  <td class="px-4 py-3 text-xs"><span class="inline-flex items-center rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 font-medium text-black-700 dark:text-black-600">{run.source}</span></td>
                  <td class="px-4 py-3 text-xs"><span class="rounded-full px-2 py-0.5 font-medium {statusBadge[run.status] ?? statusBadge.running}">{run.status}</span></td>
                  <td class="px-4 py-3 text-xs text-black-800 dark:text-black-600">{userLabel(run)}</td>
                  <td class="px-4 py-3 text-right font-mono text-xs text-black-800 dark:text-black-600">{run.latency_ms > 0 ? `${run.latency_ms} ms` : "—"}</td>
                  <td class="px-4 py-3 text-xs text-black-800 dark:text-black-600"><span title={run.started_at}>{relativeTime(run.started_at)}</span></td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>

        {#if data.total_pages > 1}
          <nav class="mt-3 flex items-center justify-center gap-1 text-xs">
            <Button variant="secondary" size="sm" disabled={data.page <= 1} onclick={() => gotoPage(data.page - 1)}>← Prev</Button>
            <span class="px-3 py-1.5 text-black-800 dark:text-black-600">Page {data.page} of {data.total_pages}</span>
            <Button variant="secondary" size="sm" disabled={data.page >= data.total_pages} onclick={() => gotoPage(data.page + 1)}>Next →</Button>
          </nav>
        {/if}
      </section>
    {/if}
  {/if}
</div>
