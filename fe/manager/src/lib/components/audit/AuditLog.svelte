<script lang="ts">
  /* Admin-only cross-connector run history. Every filter is URL-synced
     (replaceState, no SPA navigation) so links stay shareable, and every
     change resets to page 1. Filters come three ways that always agree:
     the connector → instance → operation pickers plus source/status/user
     and a from/to to the minute; the advanced query box (auditQuery.ts);
     and clicking a row of the summary breakdown. A table row opens the
     call in full (AuditRunDetail). */
  import { Button, Select } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import { push } from "$lib/router.js";
  import { getAuditRuns, AUDIT_FILTER_KEYS } from "$lib/api.js";
  import type { AuditCount, AuditFilter, AuditResult, AuditRun } from "$lib/types.js";
  import AuditRunDetail from "./AuditRunDetail.svelte";
  import { formatAuditQuery, parseAuditQuery, toISOFromLocal, toLocalInput, type QueryKey } from "./auditQuery.js";

  /* embedded: mounted inside the admin Audit page, where there is no SPA
     router, so links leave the page instead of pushing a route. */
  let { embedded = false }: { embedded?: boolean } = $props();

  /* Every entity.ConnectorRunSource value. */
  const sourceOptions = [
    { label: "All sources", value: "" },
    { label: "MCP (agents)", value: "mcp" },
    { label: "Code mode", value: "code_mode" },
    { label: "Schedule / watch", value: "schedule" },
    { label: "Workflow", value: "workflow" },
    { label: "App", value: "app" },
    { label: "Plugin", value: "plugin" },
    { label: "Test panel", value: "test" },
    { label: "Plugin test", value: "plugin_test" },
    { label: "Retry", value: "retry" },
  ];
  const statusOptions = [
    { label: "All statuses", value: "" },
    { label: "Success", value: "success" },
    { label: "Error", value: "error" },
    { label: "Running", value: "running" },
    { label: "Cancelled", value: "cancelled" },
  ];
  const emptyFilter: Omit<AuditFilter, "page"> = {
    connector: "", instance: "", op: "", source: "", status: "", user: "", session: "", error: "", min_ms: "", max_ms: "", from: "", to: "",
  };

  let data = $state<AuditResult | null>(null);
  let loading = $state(true);
  let error = $state("");
  let filter = $state<AuditFilter>(filterFromUrl());
  let query = $state("");
  let queryDirty = $state(false);
  let queryWarn = $state("");
  let detailId = $state<string | null>(null);

  let runs = $derived(data?.runs ?? []);
  let bd = $derived(data?.breakdown);
  let options = $derived(data?.options ?? []);
  let hasFilters = $derived(AUDIT_FILTER_KEYS.some((k) => filter[k]));
  let errorRate = $derived(data && data.summary.total > 0 ? (data.summary.errored / data.summary.total) * 100 : 0);

  /* Instance / user names seen in this response, to show and type names
     instead of ids. */
  let instanceNames = $derived.by(() => {
    const m = new Map<string, string>();
    for (const c of options) for (const i of c.instances ?? []) m.set(i.id, i.label);
    for (const i of bd?.instances ?? []) m.set(i.key, i.label);
    for (const r of runs) if (r.connector_name) m.set(r.connector_id, r.connector_name);
    return m;
  });
  let userNames = $derived.by(() => {
    const m = new Map<string, string>();
    for (const u of data?.users ?? []) m.set(u.id, u.name || u.id);
    return m;
  });

  let connectorOptions = $derived([
    { label: "All connectors", value: "" },
    ...options.map((c) => ({ label: `${c.key} (${c.runs})`, value: c.key })),
  ]);
  let instanceOptions = $derived.by(() => {
    const pool = options.filter((c) => !filter.connector || c.key === filter.connector);
    return [
      { label: "All instances", value: "" },
      ...pool.flatMap((c) => (c.instances ?? []).map((i) => ({ label: `${i.label} (${i.runs})`, value: i.id }))),
    ];
  });
  let opOptions = $derived.by(() => {
    const ops = new Set<string>();
    for (const c of options) {
      if (filter.connector && c.key !== filter.connector) continue;
      for (const i of c.instances ?? []) {
        if (filter.instance && i.id !== filter.instance) continue;
        for (const o of i.ops ?? []) ops.add(o);
      }
    }
    return [{ label: "All operations", value: "" }, ...[...ops].sort().map((o) => ({ label: o, value: o }))];
  });
  let userOptions = $derived([
    { label: "All users", value: "" },
    ...(data?.users ?? []).map((u) => ({ label: u.name || u.id, value: u.id })),
  ]);

  $effect(() => {
    if (!queryDirty) query = formatAuditQuery(filter, display);
  });

  function display(key: QueryKey, v: string): string {
    if (key === "instance") return instanceNames.get(v) ?? v;
    if (key === "user") return userNames.get(v) ?? v;
    if (key === "from" || key === "to") return toLocalInput(v) || v;
    return v;
  }

  /* resolve maps a typed name back to an id (instance, user); unknown
     names pass through so an id typed by hand still works. */
  function resolve(key: QueryKey, v: string): string {
    if (key === "instance") return v.split(",").map((x) => resolveOne(instanceNames, x)).join(",");
    if (key === "user") return v.split(",").map((x) => resolveOne(userNames, x)).join(",");
    if (key === "from" || key === "to") return toISOFromLocal(v) || v;
    return v;
  }

  function resolveOne(m: Map<string, string>, v: string): string {
    const want = v.trim().toLowerCase();
    for (const [id, name] of m) if (name.toLowerCase() === want) return id;
    return v.trim();
  }

  function filterFromUrl(): AuditFilter {
    let q: URLSearchParams;
    try {
      q = new URLSearchParams(window.location.search);
    } catch {
      q = new URLSearchParams();
    }
    const page = Number(q.get("page") ?? "1");
    const f: AuditFilter = { ...emptyFilter, page: Number.isFinite(page) && page > 0 ? page : 1 };
    for (const k of AUDIT_FILTER_KEYS) f[k] = q.get(k) ?? "";
    if (!f.instance && q.get("connector_id")) f.instance = q.get("connector_id") ?? "";
    return f;
  }

  function syncFilterToUrl(f: AuditFilter): void {
    const params = new URLSearchParams();
    if (embedded) params.set("tab", "calls");
    for (const k of AUDIT_FILTER_KEYS) if (f[k]) params.set(k, f[k] as string);
    if (f.page > 1) params.set("page", String(f.page));
    const qs = params.toString();
    const next = qs ? `${window.location.pathname}?${qs}` : window.location.pathname;
    if (window.location.pathname + window.location.search !== next) {
      history.replaceState({}, "", next);
    }
  }

  function setFilter(patch: Partial<AuditFilter>): void {
    filter = { ...filter, ...patch, page: patch.page ?? 1 };
    queryDirty = false;
    queryWarn = "";
    syncFilterToUrl(filter);
    load(true);
  }

  function clearFilters(): void {
    setFilter({ ...emptyFilter });
  }

  function applyQuery(): void {
    const p = parseAuditQuery(query);
    const next: Partial<AuditFilter> = { ...emptyFilter, min_ms: p.minMs, max_ms: p.maxMs };
    for (const [k, v] of Object.entries(p.values) as [QueryKey, string][]) next[k] = resolve(k, v);
    setFilter(next);
    if (p.unknown.length) queryWarn = `Ignored: ${p.unknown.join(" ")}`;
  }

  function gotoPage(page: number): void {
    filter = { ...filter, page };
    syncFilterToUrl(filter);
    load(true);
  }

  async function load(silent = false): Promise<void> {
    if (!silent) loading = true;
    try {
      data = await getAuditRuns(filter);
      error = "";
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (silent) {
        toastError("Refresh failed", msg);
      } else {
        error = msg;
      }
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

  /* clock is the call's local time to the second; the date only when it
     is not today. */
  function clock(iso: string): string {
    const t = new Date(iso);
    if (Number.isNaN(t.getTime())) return iso;
    const p = (n: number) => String(n).padStart(2, "0");
    const hms = `${p(t.getHours())}:${p(t.getMinutes())}:${p(t.getSeconds())}`;
    const today = new Date();
    if (t.toDateString() === today.toDateString()) return hms;
    return `${t.getFullYear()}-${p(t.getMonth() + 1)}-${p(t.getDate())} ${hms}`;
  }

  function connectorLabel(run: AuditRun): string {
    return run.connector_name || run.connector_id;
  }

  function userLabel(run: AuditRun): string {
    if (!run.user_id) return "system";
    return run.user_name || run.user_id;
  }

  function openConnector(key: string, id: string): void {
    const path = `/connectors/${encodeURIComponent(key)}/${encodeURIComponent(id)}`;
    if (embedded) window.location.assign(`/manager${path}`);
    else push(path);
  }

  const statusBadge: Record<string, string> = {
    success: "bg-pos-100 text-pos-400",
    error: "bg-neg-100 text-neg-400",
    running: "bg-prog-100 text-prog-400",
    cancelled: "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600",
  };
  const inputClass = "mt-1 w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 text-sm text-black-900 dark:text-white-100 outline-none focus:border-green-500";
  const labelClass = "block text-xs font-medium text-black-800 dark:text-black-600";
  const thClass = "px-4 py-3 text-left text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600";

  $effect(() => { load(); });
</script>

{#snippet top(title: string, items: AuditCount[] | null | undefined, pick: (c: AuditCount) => Partial<AuditFilter>, label: (c: AuditCount) => string, sub?: (c: AuditCount) => string)}
  <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
    <h3 class="text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">{title}</h3>
    {#if !items || items.length === 0}
      <p class="mt-2 text-xs text-black-700 dark:text-black-600">—</p>
    {:else}
      <ul class="mt-2 space-y-1.5">
        {#each items as c (c.key + (c.op ?? ""))}
          <li class="flex items-center gap-2 text-xs">
            <button type="button" class="min-w-0 flex-1 truncate text-left text-black-900 dark:text-white-100 hover:text-green-600" title={label(c)} onclick={() => setFilter(pick(c))}>
              {label(c)}{#if sub && sub(c)}<span class="text-black-700 dark:text-black-600">&nbsp;· {sub(c)}</span>{/if}
            </button>
            <span class="font-mono text-black-800 dark:text-black-600">{c.runs}</span>
            {#if c.failed > 0}<span class="font-mono text-neg-400">{c.failed} err</span>{/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
{/snippet}

<div class="space-y-6">
  <div>
    {#if !embedded}
      <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Audit Log</h1>
    {/if}
    <p class="mt-1 text-sm text-black-800 dark:text-black-600">Every connector call across every instance: who called what, from which session, when, and how it ended. Click a row for the full call.</p>
  </div>

  <section class="space-y-3 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
    <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
      <div>
        <span class={labelClass}>Connector</span>
        <div class="mt-1"><Select value={filter.connector ?? ""} options={connectorOptions} onChange={(v) => setFilter({ connector: v, instance: "", op: "" })} /></div>
      </div>
      <div>
        <span class={labelClass}>Instance</span>
        <div class="mt-1"><Select value={filter.instance ?? ""} options={instanceOptions} onChange={(v) => setFilter({ instance: v, op: "" })} /></div>
      </div>
      <div>
        <span class={labelClass}>Operation</span>
        <div class="mt-1"><Select value={filter.op ?? ""} options={opOptions} onChange={(v) => setFilter({ op: v })} /></div>
      </div>
    </div>
    <div class="grid grid-cols-1 gap-3 sm:grid-cols-5">
      <div>
        <label for="audit-source" class={labelClass}>Source</label>
        <div class="mt-1"><Select value={filter.source} options={sourceOptions} onChange={(v) => setFilter({ source: v })} /></div>
      </div>
      <div>
        <label for="audit-status" class={labelClass}>Status</label>
        <div class="mt-1"><Select value={filter.status} options={statusOptions} onChange={(v) => setFilter({ status: v })} /></div>
      </div>
      <div>
        <span class={labelClass}>User</span>
        <div class="mt-1"><Select value={filter.user ?? ""} options={userOptions} onChange={(v) => setFilter({ user: v })} /></div>
      </div>
      <div>
        <label for="audit-from" class={labelClass}>From</label>
        <input id="audit-from" type="datetime-local" value={toLocalInput(filter.from)} onchange={(e) => setFilter({ from: toISOFromLocal((e.target as HTMLInputElement).value) })} aria-label="From" class={inputClass} />
      </div>
      <div>
        <label for="audit-to" class={labelClass}>To</label>
        <input id="audit-to" type="datetime-local" value={toLocalInput(filter.to)} onchange={(e) => setFilter({ to: toISOFromLocal((e.target as HTMLInputElement).value) })} aria-label="To" class={inputClass} />
      </div>
    </div>
    <div>
      <label for="audit-query" class={labelClass}>Advanced filter</label>
      <div class="mt-1 flex gap-2">
        <input
          id="audit-query"
          type="text"
          spellcheck="false"
          value={query}
          oninput={(e) => { query = (e.target as HTMLInputElement).value; queryDirty = true; }}
          onkeydown={(e) => { if (e.key === "Enter") applyQuery(); }}
          placeholder={'connector:slack op:send,get_thread status:error user:"Dev One" latency>1000 from:2026-11-04T09:10 timeout'}
          class="w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-3 py-1.5 font-mono text-xs text-black-900 dark:text-white-100 outline-none focus:border-green-500"
        />
        <Button variant="secondary" size="sm" onclick={applyQuery}>Apply</Button>
      </div>
      <p class="mt-1 text-xs text-black-700 dark:text-black-600">
        Keys: connector, instance, op, source, status, user, session, error, from, to (comma = any of), latency&gt;ms / latency&lt;ms; a word without a key searches the error text. Enter applies.
        {#if queryWarn}<span class="text-neg-400"> {queryWarn}</span>{/if}
      </p>
    </div>
    {#if hasFilters}
      <div>
        <button type="button" class="text-xs font-medium text-green-600 hover:underline" onclick={clearFilters}>Reset filters</button>
      </div>
    {/if}
  </section>

  {#if loading}
    <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if data}
    <section class="grid grid-cols-2 gap-3 sm:grid-cols-4">
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <div class="text-xs text-black-800 dark:text-black-600">Calls</div>
        <div class="mt-1 text-2xl font-semibold text-black-900 dark:text-white-100">{data.summary.total}</div>
        <div class="mt-1 text-xs text-black-700 dark:text-black-600">{data.total} run(s) found</div>
      </div>
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <div class="text-xs text-black-800 dark:text-black-600">Errors</div>
        <div class="mt-1 text-2xl font-semibold {data.summary.errored > 0 ? 'text-neg-400' : 'text-black-900 dark:text-white-100'}">{data.summary.errored} <span class="text-sm font-normal text-black-700 dark:text-black-600">({errorRate.toFixed(1)}%)</span></div>
        <div class="mt-1 text-xs text-black-700 dark:text-black-600">· {data.summary.succeeded} ok</div>
      </div>
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <div class="text-xs text-black-800 dark:text-black-600">Avg latency</div>
        <div class="mt-1 text-2xl font-semibold text-black-900 dark:text-white-100">{Math.round(data.summary.avg_latency_ms)} ms</div>
      </div>
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-4">
        <div class="text-xs text-black-800 dark:text-black-600">By source</div>
        <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs">
          {#each bd?.sources ?? [] as s (s.key)}
            <button type="button" class="text-black-900 dark:text-white-100 hover:text-green-600" onclick={() => setFilter({ source: s.key })}>{s.key} <span class="font-mono text-black-700 dark:text-black-600">{s.runs}</span></button>
          {:else}
            <span class="text-black-700 dark:text-black-600">—</span>
          {/each}
        </div>
      </div>
    </section>

    <section class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-3">
      {@render top("Top instances", bd?.instances, (c) => ({ instance: c.key }), (c) => c.label, (c) => c.connector_key ?? "")}
      {@render top("Top operations", bd?.operations, (c) => ({ instance: c.key, op: c.op ?? "" }), (c) => c.op ?? "", (c) => c.label)}
      {@render top("Top users", bd?.users, (c) => ({ user: c.key }), (c) => c.label)}
      {@render top("Top sessions", bd?.sessions, (c) => ({ session: c.key }), (c) => c.label, (c) => c.project ?? "")}
      {@render top("Top errors", bd?.errors, (c) => ({ error: c.key.slice(0, 80) }), (c) => c.label)}
    </section>

    {#if runs.length === 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-8 text-center">
        <p class="text-sm text-black-700 dark:text-black-600">No runs match the current filter.</p>
      </div>
    {:else}
      <section>
        <div class="overflow-x-auto rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
                <th class={thClass}>Time</th>
                <th class={thClass}>Connector</th>
                <th class={thClass}>Operation</th>
                <th class={thClass}>Source</th>
                <th class={thClass}>Status</th>
                <th class={thClass}>User</th>
                <th class={thClass}>Session</th>
                <th class="px-4 py-3 text-right text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Latency</th>
              </tr>
            </thead>
            <tbody>
              {#each runs as run (run.id)}
                <tr class="cursor-pointer border-b border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-800 {detailId === run.id ? 'bg-white-200 dark:bg-navy-800' : ''}" onclick={() => (detailId = run.id)}>
                  <td class="whitespace-nowrap px-4 py-3 font-mono text-xs text-black-900 dark:text-white-100" title={relativeTime(run.started_at)}>{clock(run.started_at)}</td>
                  <td class="px-4 py-3 text-black-900 dark:text-white-100">
                    {#if run.connector_key}
                      <div class="whitespace-nowrap">{connectorLabel(run)}</div>
                      <div class="text-xs text-black-700 dark:text-black-600">{run.connector_key}</div>
                    {:else}
                      <span class="font-mono text-xs text-black-800 dark:text-black-600">{connectorLabel(run)}</span>
                    {/if}
                  </td>
                  <td class="px-4 py-3 font-mono text-xs text-black-900 dark:text-white-100">
                    {run.operation_key}
                    {#if run.error}<div class="max-w-xs truncate font-sans text-neg-400" title={run.error}>{run.error}</div>{/if}
                  </td>
                  <td class="px-4 py-3 text-xs"><span class="inline-flex items-center rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 font-medium text-black-700 dark:text-black-600">{run.source}</span></td>
                  <td class="px-4 py-3 text-xs"><span class="rounded-full px-2 py-0.5 font-medium {statusBadge[run.status] ?? statusBadge.running}">{run.status}</span></td>
                  <td class="whitespace-nowrap px-4 py-3 text-xs text-black-800 dark:text-black-600">{userLabel(run)}</td>
                  <td class="max-w-[14rem] px-4 py-3 text-xs text-black-800 dark:text-black-600" title={run.session_id || ""}>
                    {#if run.session_id}
                      <div class="truncate text-black-900 dark:text-white-100">{run.session_label || run.session_id}</div>
                      <div class="truncate">{run.project_name || run.project_id || "—"}</div>
                    {:else}
                      —
                    {/if}
                  </td>
                  <td class="whitespace-nowrap px-4 py-3 text-right font-mono text-xs text-black-800 dark:text-black-600">{run.latency_ms > 0 ? `${run.latency_ms} ms` : "—"}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>

        {#if data.total_pages > 1}
          <nav class="mt-3 flex items-center justify-center gap-1 text-xs">
            <Button variant="secondary" size="sm" disabled={data.page <= 1} onclick={() => gotoPage(data!.page - 1)}>← Prev</Button>
            <span class="px-3 py-1.5 text-black-800 dark:text-black-600">Page {data.page} of {data.total_pages}</span>
            <Button variant="secondary" size="sm" disabled={data.page >= data.total_pages} onclick={() => gotoPage(data!.page + 1)}>Next →</Button>
          </nav>
        {/if}
      </section>
    {/if}
  {/if}
</div>

{#if detailId}
  <AuditRunDetail
    runId={detailId}
    onClose={() => (detailId = null)}
    onFilter={(patch) => { detailId = null; setFilter({ ...emptyFilter, ...patch }); }}
    onOpenConnector={openConnector}
  />
{/if}
