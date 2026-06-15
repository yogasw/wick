<script lang="ts">
  /* Per-row run audit log. Mirrors the legacy connector_history.templ +
     connector_history.js: filter selects (op, source, status, user), a
     paginated run table, and expandable rows revealing request/response
     JSON. Filters + page are URL-synced via ?op=&source=&status=&user=&page=
     (replaceState, no SPA navigation) so links stay shareable, then trigger
     a re-fetch. Reuses common-ui Select/Button + the JSON history endpoint. */
  import { Button, Select } from "@wick-fe/common-ui";
  import { push } from "$lib/router.js";
  import { getConnectorHistory } from "$lib/api.js";
  import type { HistoryResult, HistoryFilter, HistoryRun } from "$lib/types.js";

  type Props = { connectorKey: string; connectorId: string };
  let { connectorKey, connectorId }: Props = $props();

  const sourceOptions = [
    { label: "All sources", value: "" },
    { label: "MCP", value: "mcp" },
    { label: "Panel test", value: "test" },
    { label: "Retry", value: "retry" },
  ];
  const statusOptions = [
    { label: "All statuses", value: "" },
    { label: "Success", value: "success" },
    { label: "Error", value: "error" },
    { label: "Running", value: "running" },
  ];

  let data = $state<HistoryResult | null>(null);
  let loading = $state(true);
  let error = $state("");
  let expanded = $state<Record<string, boolean>>({});
  let filter = $state<HistoryFilter>(filterFromUrl());

  let runs = $derived(data?.runs ?? []);
  let opOptions = $derived([
    { label: "All operations", value: "" },
    ...(data?.ops ?? []).map((o) => ({ label: o.name, value: o.key })),
  ]);
  let userOptions = $derived([
    { label: "All users", value: "" },
    ...(data?.users ?? []).map((u) => ({ label: u.name, value: u.id })),
  ]);
  let hasFilters = $derived(!!(filter.op || filter.source || filter.status || filter.user));

  function filterFromUrl(): HistoryFilter {
    let q: URLSearchParams;
    try {
      q = new URLSearchParams(window.location.search);
    } catch {
      q = new URLSearchParams();
    }
    const page = Number(q.get("page") ?? "1");
    return {
      op: q.get("op") ?? "",
      source: q.get("source") ?? "",
      status: q.get("status") ?? "",
      user: q.get("user") ?? "",
      page: Number.isFinite(page) && page > 0 ? page : 1,
    };
  }

  function syncFilterToUrl(f: HistoryFilter): void {
    const params = new URLSearchParams();
    if (f.op) params.set("op", f.op);
    if (f.source) params.set("source", f.source);
    if (f.status) params.set("status", f.status);
    if (f.user) params.set("user", f.user);
    if (f.page > 1) params.set("page", String(f.page));
    const qs = params.toString();
    const next = qs ? `${window.location.pathname}?${qs}` : window.location.pathname;
    if (window.location.pathname + window.location.search !== next) {
      history.replaceState({}, "", next);
    }
  }

  function setFilter(patch: Partial<HistoryFilter>): void {
    filter = { ...filter, ...patch, page: patch.page ?? 1 };
    syncFilterToUrl(filter);
    expanded = {};
    load();
  }

  function clearFilters(): void {
    setFilter({ op: "", source: "", status: "", user: "" });
  }

  function gotoPage(page: number): void {
    filter = { ...filter, page };
    syncFilterToUrl(filter);
    expanded = {};
    load();
  }

  function toggle(id: string): void {
    expanded = { ...expanded, [id]: !expanded[id] };
  }

  async function load(): Promise<void> {
    loading = true;
    error = "";
    try {
      data = await getConnectorHistory(connectorKey, connectorId, filter);
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

  function prettyJSON(raw: string): string {
    if (!raw) return "—";
    try {
      return JSON.stringify(JSON.parse(raw), null, 2);
    } catch {
      return raw;
    }
  }

  function userLabel(run: HistoryRun): string {
    if (!run.user_id) return "system";
    return run.user_name || run.user_id;
  }

  const statusBadge: Record<string, string> = {
    success: "bg-pos-100 text-pos-400",
    error: "bg-neg-100 text-neg-400",
    running: "bg-prog-100 text-prog-400",
  };

  $effect(() => { load(); });
</script>

<div class="space-y-6">
  <div class="flex items-start justify-between gap-4">
    <div>
      <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Run history</h1>
      <p class="mt-1 text-sm text-black-800 dark:text-black-600">Recent runs against this row — MCP calls, panel tests, and retries. Older entries are purged on the daily retention cycle.</p>
    </div>
    <Button variant="secondary" size="md" onclick={() => push(`/connectors/${encodeURIComponent(connectorKey)}/${encodeURIComponent(connectorId)}/test`)}>Test runner</Button>
  </div>

  <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
    <div class="grid grid-cols-1 gap-3 sm:grid-cols-4">
      <div>
        <label for="hist-op" class="block text-xs font-medium text-black-800 dark:text-black-600">Operation</label>
        <div class="mt-1"><Select value={filter.op} options={opOptions} onChange={(v) => setFilter({ op: v })} /></div>
      </div>
      <div>
        <label for="hist-source" class="block text-xs font-medium text-black-800 dark:text-black-600">Source</label>
        <div class="mt-1"><Select value={filter.source} options={sourceOptions} onChange={(v) => setFilter({ source: v })} /></div>
      </div>
      <div>
        <label for="hist-status" class="block text-xs font-medium text-black-800 dark:text-black-600">Status</label>
        <div class="mt-1"><Select value={filter.status} options={statusOptions} onChange={(v) => setFilter({ status: v })} /></div>
      </div>
      <div>
        <label for="hist-user" class="block text-xs font-medium text-black-800 dark:text-black-600">User</label>
        <div class="mt-1"><Select value={filter.user} options={userOptions} onChange={(v) => setFilter({ user: v })} /></div>
      </div>
    </div>
    {#if hasFilters}
      <div class="mt-3">
        <button type="button" class="text-xs font-medium text-green-600 hover:underline" onclick={clearFilters}>Clear all filters</button>
      </div>
    {/if}
  </section>

  {#if loading}
    <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if runs.length === 0}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-8 text-center">
      <p class="text-sm text-black-700 dark:text-black-600">No runs match the current filters.</p>
    </div>
  {:else if data}
    <section>
      <div class="overflow-hidden rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
              <th class="w-8 px-2 py-3"></th>
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">When</th>
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Operation</th>
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Source</th>
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">User</th>
              <th class="px-4 py-3 text-left font-medium text-black-800 dark:text-black-600">Status</th>
              <th class="px-4 py-3 text-right font-medium text-black-800 dark:text-black-600">Latency</th>
            </tr>
          </thead>
          <tbody>
            {#each runs as run (run.id)}
              <tr class="border-b border-white-300 dark:border-navy-600 align-top cursor-pointer hover:bg-white-200 dark:hover:bg-navy-800" onclick={() => toggle(run.id)}>
                <td class="px-2 py-3 text-center text-black-700 dark:text-black-600">
                  <svg class="inline h-3 w-3 transition-transform" style={expanded[run.id] ? "transform: rotate(90deg)" : ""} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="9 18 15 12 9 6"/></svg>
                </td>
                <td class="px-4 py-3 text-xs text-black-800 dark:text-black-600"><span title={run.started_at}>{relativeTime(run.started_at)}</span></td>
                <td class="px-4 py-3 font-mono text-xs text-black-900 dark:text-white-100">{run.operation_key}</td>
                <td class="px-4 py-3 text-xs"><span class="inline-flex items-center rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 font-medium text-black-700 dark:text-black-600">{run.source}</span></td>
                <td class="px-4 py-3 text-xs text-black-800 dark:text-black-600">{userLabel(run)}</td>
                <td class="px-4 py-3 text-xs">
                  <span class="rounded-full px-2 py-0.5 font-medium {statusBadge[run.status] ?? statusBadge.running}">{run.status}</span>
                  {#if run.status === "error" && run.error_msg}
                    <p class="mt-1 max-w-md truncate font-mono text-[10px] text-neg-400" title={run.error_msg}>{run.error_msg}</p>
                  {/if}
                </td>
                <td class="px-4 py-3 text-right font-mono text-xs text-black-800 dark:text-black-600">{run.latency_ms > 0 ? `${run.latency_ms} ms` : "—"}</td>
              </tr>
              {#if expanded[run.id]}
                <tr class="border-b border-white-300 dark:border-navy-600">
                  <td colspan="7" class="bg-white-200 dark:bg-navy-800 px-4 py-4">
                    <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
                      <div>
                        <p class="text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">Request</p>
                        <pre class="mt-1 max-h-80 overflow-auto rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-3 font-mono text-[11px] text-black-900 dark:text-white-100">{prettyJSON(run.request_json)}</pre>
                      </div>
                      <div>
                        <p class="text-[11px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">Response</p>
                        {#if run.status === "error" && run.error_msg}
                          <pre class="mt-1 max-h-80 overflow-auto rounded-lg border border-neg-300 bg-neg-100 p-3 font-mono text-[11px] text-neg-400">{run.error_msg}</pre>
                        {:else}
                          <pre class="mt-1 max-h-80 overflow-auto rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-3 font-mono text-[11px] text-black-900 dark:text-white-100">{prettyJSON(run.response_json)}</pre>
                        {/if}
                      </div>
                    </div>
                    <div class="mt-3 flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-black-700 dark:text-black-600">
                      <span>Run ID: <code class="font-mono">{run.id}</code></span>
                      {#if run.ip_address}<span>IP: <code class="font-mono">{run.ip_address}</code></span>{/if}
                      {#if run.http_status > 0}<span>HTTP: <code class="font-mono">{run.http_status}</code></span>{/if}
                    </div>
                  </td>
                </tr>
              {/if}
            {/each}
          </tbody>
        </table>
      </div>

      <div class="mt-3 flex flex-wrap items-center justify-between gap-3">
        <p class="text-[11px] text-black-700 dark:text-black-600">{data.total} run(s)</p>
        {#if data.total_pages > 1}
          <nav class="flex items-center gap-1 text-xs">
            <Button variant="secondary" size="sm" disabled={data.page <= 1} onclick={() => gotoPage(data.page - 1)}>← Prev</Button>
            <span class="px-3 py-1.5 text-black-800 dark:text-black-600">Page {data.page} of {data.total_pages}</span>
            <Button variant="secondary" size="sm" disabled={data.page >= data.total_pages} onclick={() => gotoPage(data.page + 1)}>Next →</Button>
          </nav>
        {/if}
      </div>
    </section>
  {/if}
</div>
