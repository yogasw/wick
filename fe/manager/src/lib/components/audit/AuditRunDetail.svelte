<script lang="ts">
  /* Side panel with one connector call in full: exact start/end time,
     who, from which session and project, the connector instance and
     operation, and the request/response wick recorded. Buttons narrow the
     list to this call's user, session, instance or operation. */
  import { getAuditRun } from "$lib/api.js";
  import type { AuditFilter, AuditRunDetail } from "$lib/types.js";

  let {
    runId,
    onClose,
    onFilter,
    onOpenConnector,
  }: {
    runId: string;
    onClose: () => void;
    onFilter: (patch: Partial<AuditFilter>) => void;
    onOpenConnector: (key: string, id: string) => void;
  } = $props();

  let run = $state<AuditRunDetail | null>(null);
  let error = $state("");

  $effect(() => {
    const id = runId;
    run = null;
    error = "";
    getAuditRun(id)
      .then((r) => { if (id === runId) run = r; })
      .catch((e) => { error = e instanceof Error ? e.message : String(e); });
  });

  function exact(iso: string): string {
    if (!iso) return "—";
    const t = new Date(iso);
    if (Number.isNaN(t.getTime())) return iso;
    return t.toLocaleString(undefined, {
      year: "numeric", month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", timeZoneName: "short",
    });
  }

  function pretty(s: string): string {
    if (!s) return "";
    try {
      return JSON.stringify(JSON.parse(s), null, 2);
    } catch {
      return s;
    }
  }

  function onKey(e: KeyboardEvent): void {
    if (e.key === "Escape") onClose();
  }
</script>

<svelte:window onkeydown={onKey} />

<div class="fixed inset-0 z-40 bg-navy-900/40" role="presentation" onclick={onClose}></div>
<aside class="fixed inset-y-0 right-0 z-50 flex w-full max-w-2xl flex-col border-l border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-xl" aria-label="Call detail">
  <header class="flex items-center gap-3 border-b border-white-300 dark:border-navy-600 px-5 py-4">
    <h2 class="flex-1 text-sm font-semibold text-black-900 dark:text-white-100">Connector call</h2>
    <button type="button" class="rounded px-2 py-1 text-sm text-black-800 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-600" onclick={onClose} aria-label="Close">✕</button>
  </header>
  <div class="flex-1 space-y-5 overflow-y-auto px-5 py-4 text-sm">
    {#if error}
      <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-red-700 dark:text-red-400">{error}</div>
    {:else if !run}
      <div class="py-8 text-center text-black-700 dark:text-black-600">Loading…</div>
    {:else}
      <dl class="grid grid-cols-[8rem_1fr] gap-x-4 gap-y-2">
        <dt class="text-black-700 dark:text-black-600">Started</dt>
        <dd class="font-mono text-xs text-black-900 dark:text-white-100">{exact(run.started_at)}</dd>
        <dt class="text-black-700 dark:text-black-600">Ended</dt>
        <dd class="font-mono text-xs text-black-900 dark:text-white-100">{exact(run.ended_at)} · {run.latency_ms} ms</dd>
        <dt class="text-black-700 dark:text-black-600">Status</dt>
        <dd class="text-black-900 dark:text-white-100">{run.status}{run.http_status ? ` · HTTP ${run.http_status}` : ""}</dd>
        <dt class="text-black-700 dark:text-black-600">Connector</dt>
        <dd class="text-black-900 dark:text-white-100">
          {#if run.connector_key}
            <button type="button" class="hover:text-green-600" onclick={() => onOpenConnector(run!.connector_key, run!.connector_id)}>{run.connector_name || run.connector_id}</button>
            <span class="text-xs text-black-700 dark:text-black-600">({run.connector_key})</span>
          {:else}
            <span class="font-mono text-xs">{run.connector_id}</span>
          {/if}
        </dd>
        <dt class="text-black-700 dark:text-black-600">Operation</dt>
        <dd class="font-mono text-xs text-black-900 dark:text-white-100">{run.operation_key}</dd>
        <dt class="text-black-700 dark:text-black-600">Source</dt>
        <dd class="text-black-900 dark:text-white-100">{run.source}</dd>
        <dt class="text-black-700 dark:text-black-600">User</dt>
        <dd class="text-black-900 dark:text-white-100">{run.user_id ? run.user_name || run.user_id : "system"}</dd>
        <dt class="text-black-700 dark:text-black-600">Session</dt>
        <dd class="text-black-900 dark:text-white-100">
          {#if run.session_id}
            <a class="hover:text-green-600" href={`/tools/agents/sessions/${encodeURIComponent(run.session_id)}`} target="_blank" rel="noopener">{run.session_label || run.session_id}</a>
            <div class="font-mono text-xs text-black-700 dark:text-black-600">{run.session_id}</div>
          {:else}
            —
          {/if}
        </dd>
        <dt class="text-black-700 dark:text-black-600">Project</dt>
        <dd class="text-black-900 dark:text-white-100">{run.project_name || run.project_id || "—"}</dd>
        {#if run.account_id}
          <dt class="text-black-700 dark:text-black-600">Account</dt>
          <dd class="font-mono text-xs text-black-900 dark:text-white-100">{run.account_id}</dd>
        {/if}
        {#if run.ip_address || run.user_agent}
          <dt class="text-black-700 dark:text-black-600">Client</dt>
          <dd class="break-all font-mono text-xs text-black-900 dark:text-white-100">{run.ip_address} {run.user_agent}</dd>
        {/if}
        {#if run.parent_run_id}
          <dt class="text-black-700 dark:text-black-600">Retry of</dt>
          <dd class="font-mono text-xs text-black-900 dark:text-white-100">{run.parent_run_id}</dd>
        {/if}
        <dt class="text-black-700 dark:text-black-600">Run id</dt>
        <dd class="font-mono text-xs text-black-900 dark:text-white-100">{run.id}</dd>
      </dl>

      <div class="flex flex-wrap gap-2">
        <span class="text-xs text-black-700 dark:text-black-600">Show all calls with the same:</span>
        {#if run.user_id}<button type="button" class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs text-black-900 dark:text-white-100 hover:text-green-600" onclick={() => onFilter({ user: run!.user_id })}>user</button>{/if}
        {#if run.session_id}<button type="button" class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs text-black-900 dark:text-white-100 hover:text-green-600" onclick={() => onFilter({ session: run!.session_id })}>session</button>{/if}
        <button type="button" class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs text-black-900 dark:text-white-100 hover:text-green-600" onclick={() => onFilter({ instance: run!.connector_id })}>instance</button>
        <button type="button" class="rounded-full bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs text-black-900 dark:text-white-100 hover:text-green-600" onclick={() => onFilter({ instance: run!.connector_id, op: run!.operation_key })}>operation</button>
      </div>

      {#if run.error_full}
        <section>
          <h3 class="mb-1 text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Error</h3>
          <pre class="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-3 font-mono text-xs text-neg-400">{run.error_full}</pre>
        </section>
      {/if}
      <section>
        <h3 class="mb-1 text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Request</h3>
        <pre class="max-h-72 overflow-auto whitespace-pre-wrap break-words rounded border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-3 font-mono text-xs text-black-900 dark:text-white-100">{pretty(run.request) || "—"}</pre>
      </section>
      <section>
        <h3 class="mb-1 text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Response</h3>
        <pre class="max-h-96 overflow-auto whitespace-pre-wrap break-words rounded border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-3 font-mono text-xs text-black-900 dark:text-white-100">{pretty(run.response) || "—"}</pre>
      </section>
    {/if}
  </div>
</aside>
