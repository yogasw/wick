<script lang="ts">
  /* Admin → Plugins → Host access. Every plugin that declares (or was seen
     using) the Host API: its scopes with their status, an ON/OFF toggle per
     scope (the default-OFF ones included), the declared items per scope
     with ✕ block / ↺ allow, Block all host access, Revoke host tokens, and
     the last 7 days of calls. Destructive scopes and items are red,
     sensitive ones yellow. filter narrows the plugins by key (the shared
     search box; the detail card links here with it set). */
  import { Button, ConfirmDialog } from "@wick-fe/common-ui";
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import {
    listHostAccess, setHostScope, setHostItem, setHostBlockAll, setHostRevoked, listHostCalls,
    type PluginHostAccess, type HostAccessScope, type HostCall,
  } from "$lib/api.js";
  import { STATUS_LABEL, statusClass, scopeOn, canToggle, scopeTone } from "./hostAccess.js";

  let { filter = "" }: { filter?: string } = $props();

  const PAGE = 50;
  let plugins = $state<PluginHostAccess[]>([]);
  let loading = $state(true);
  let loadError = $state("");
  let busy = $state("");
  let revoking = $state<string | null>(null);
  let calls = $state<HostCall[]>([]);
  let callsTotal = $state(0);
  let callsPage = $state(1);
  let callsPlugin = $state("");
  let failedOnly = $state(false);

  let shown = $derived(plugins.filter((p) => !filter.trim() || p.key.toLowerCase().includes(filter.trim().toLowerCase())));
  let pages = $derived(Math.max(1, Math.ceil(callsTotal / PAGE)));

  async function load(): Promise<void> {
    try {
      plugins = await listHostAccess();
    } catch (e) {
      // e.g. 503 "host API not started": say it on the page, not only in a toast.
      loadError = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }
  async function loadCalls(): Promise<void> {
    try {
      const r = await listHostCalls({ plugin: callsPlugin, failed: failedOnly, page: callsPage, limit: PAGE });
      calls = r.calls ?? [];
      callsTotal = r.total;
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    }
  }
  $effect(() => {
    load();
  });
  $effect(() => {
    void [callsPlugin, failedOnly, callsPage];
    loadCalls();
  });

  /* Runs one change and swaps the plugin's row for the server's answer. */
  async function change(id: string, fn: () => Promise<PluginHostAccess>, ok?: string): Promise<void> {
    busy = id;
    try {
      const v = await fn();
      plugins = plugins.map((p) => (p.key === v.key ? v : p));
      if (ok) toastOk(ok);
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }
  const toggle = (p: PluginHostAccess, s: HostAccessScope) =>
    change(`${p.key}/${s.scope}`, () => setHostScope(p.key, s.scope, !scopeOn(s)));
  const reset = (p: PluginHostAccess, s: HostAccessScope) =>
    change(`${p.key}/${s.scope}`, () => setHostScope(p.key, s.scope, null));
  const item = (p: PluginHostAccess, s: HostAccessScope, it: string, blocked: boolean) =>
    change(`${p.key}/${s.scope}/${it}`, () => setHostItem(p.key, s.scope, it, blocked));
  const blockAll = (p: PluginHostAccess) =>
    change(`${p.key}/*`, () => setHostBlockAll(p.key, !p.blocked_all), p.blocked_all ? "Host access unblocked" : "All host access blocked");
  /* Revoke / restore, with a toast that says what actually happened. */
  async function setRevoked(key: string, revoked: boolean): Promise<void> {
    busy = `${key}/revoke`;
    try {
      const r = await setHostRevoked(key, revoked);
      plugins = plugins.map((p) => (p.key === r.access.key ? r.access : p));
      if (!r.changed) toastOk(revoked ? `Host access of ${key} was already revoked` : `Host access of ${key} was not revoked`);
      else if (revoked) toastOk(`Host access of ${key} revoked` + (r.dropped ? ` — ${r.dropped} live token${r.dropped === 1 ? "" : "s"} dropped` : " — no live token to drop; later calls are refused"));
      else toastOk(`Host access of ${key} restored; its next process gets a token`);
    } catch (e) {
      toastError(e instanceof Error ? e.message : String(e));
    } finally {
      busy = "";
    }
  }
  async function revoke(): Promise<void> {
    const key = revoking;
    revoking = null;
    if (key) await setRevoked(key, true);
  }

  const fmt = (t?: string) => (t ? new Date(t).toLocaleString() : "never");
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if loadError}
  <div class="px-5 py-12 text-center text-sm text-red-600" role="alert" data-testid="host-access-error">Host access is unavailable: {loadError}</div>
{:else}
  <div class="space-y-4" data-testid="host-access">
    <p class="text-sm text-black-800 dark:text-black-600">What each plugin may call back into wick. Changes apply to the next call, without restarting the plugin.</p>
    {#each shown as p (p.key)}
      <section class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700" data-testid={`host-plugin-${p.key}`}>
        <header class="flex flex-wrap items-center gap-3 border-b border-white-300 dark:border-navy-600 px-4 py-3">
          <div class="min-w-0 flex-1">
            <p class="font-medium text-black-900 dark:text-white-100">{p.key} <span class="text-xs font-normal text-black-700 dark:text-black-600">{p.kind} · v{p.version}</span></p>
            <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">{p.calls_24h} calls in 24h · {p.failed_24h} failed ({p.denied_24h} denied) · last {fmt(p.last_call)}</p>
          </div>
          {#if p.blocked_all}<span class={statusClass("blocked")} data-testid="blocked-all">all host access blocked</span>{/if}
          <Button size="sm" variant={p.blocked_all ? "secondary" : "danger"} disabled={!!busy} onclick={() => blockAll(p)}>{p.blocked_all ? "Unblock" : "Block all host access"}</Button>
          {#if p.revoked}
            <span class={statusClass("blocked")} data-testid="revoked">host access revoked</span>
            <Button size="sm" disabled={!!busy} onclick={() => setRevoked(p.key, false)}>Restore host access</Button>
          {:else}
            <Button size="sm" variant="secondary" disabled={!!busy} onclick={() => (revoking = p.key)}>Revoke host tokens</Button>
          {/if}
        </header>
        {#each p.scopes as s (s.scope)}
          <div class="border-b border-white-300 dark:border-navy-600 px-4 py-3 last:border-b-0" data-testid={`host-scope-${s.scope}`}>
            <div class="flex flex-wrap items-center gap-3">
              <div class="min-w-0 flex-1">
                <p class={`text-sm ${scopeTone(s)}`}>
                  {s.label || s.scope}
                  <code class="ml-1 font-mono text-xs text-black-700 dark:text-black-600">{s.scope}</code>
                  {#if s.destructive}<span class="ml-1 text-xs text-red-600">destructive</span>{/if}
                  {#if s.sensitive}<span class="ml-1 text-xs text-cau-400">sensitive</span>{/if}
                  {#if !s.declared}<span class="ml-1 text-xs text-black-700 dark:text-black-600">not declared</span>{/if}
                </p>
                {#if s.known}
                  <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">{s.description}{s.calls_24h ? ` · ${s.calls_24h} calls in 24h, ${s.failed_24h} failed (${s.denied_24h} denied)` : ""}{s.declarative ? " · Declared for information; there is nothing to switch." : ""}{s.known && s.resource && !s.declared ? " · Not declared: the plugin must declare it before it can be allowed." : ""}</p>
                {:else}
                  <p class="mt-0.5 text-xs text-black-700 dark:text-black-600" data-testid="unknown-note">{s.reason}</p>
                {/if}
              </div>
              <span class={statusClass(s.status)} data-testid="scope-status">{STATUS_LABEL[s.status] ?? s.status}</span>
              {#if canToggle(s)}
                {#if s.overlay}
                  <button class="text-xs text-black-700 underline dark:text-black-600" disabled={!!busy} onclick={() => reset(p, s)}>Use default</button>
                {/if}
                <button
                  type="button"
                  role="switch"
                  aria-checked={scopeOn(s)}
                  aria-label={`${s.scope} ${scopeOn(s) ? "on" : "off"}`}
                  disabled={!!busy || p.blocked_all}
                  class={`relative h-5 w-9 rounded-full transition-colors ${scopeOn(s) ? "bg-green-500" : "bg-white-300 dark:bg-navy-600"}`}
                  onclick={() => toggle(p, s)}
                >
                  <span class={`absolute top-0.5 h-4 w-4 rounded-full bg-white-100 transition-all ${scopeOn(s) ? "left-4" : "left-0.5"}`}></span>
                </button>
              {/if}
            </div>
            {#if s.items?.length}
              <ul class="mt-2 space-y-1">
                {#each s.items as it (it.item)}
                  <li class="flex items-center gap-2 text-xs" data-testid="host-item">
                    <code class={`flex-1 truncate font-mono ${it.blocked ? "text-black-700 line-through dark:text-black-600" : it.destructive ? "text-red-600" : s.sensitive ? "text-cau-400" : "text-black-900 dark:text-white-100"}`}>{it.item}</code>
                    {#if it.blocked}
                      <span class={statusClass("blocked")}>blocked</span>
                      <button class="rounded px-1.5 text-sm text-black-800 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600" aria-label={`Allow ${it.item}`} title="Allow again" disabled={!!busy} onclick={() => item(p, s, it.item, false)}>↺</button>
                    {:else}
                      <button class="rounded px-1.5 text-sm text-red-600 hover:bg-white-300 dark:hover:bg-navy-600" aria-label={`Block ${it.item}`} title="Block this item" disabled={!!busy} onclick={() => item(p, s, it.item, true)}>✕</button>
                    {/if}
                  </li>
                {/each}
              </ul>
            {/if}
          </div>
        {:else}
          <p class="px-4 py-3 text-sm text-black-700 dark:text-black-600">No host access declared.</p>
        {/each}
      </section>
    {:else}
      <p class="px-4 py-8 text-center text-sm text-black-700 dark:text-black-600">No installed plugin uses the Host API.</p>
    {/each}

    <section data-testid="host-calls">
      <div class="flex flex-wrap items-center gap-3">
        <h2 class="flex-1 text-base font-semibold text-black-900 dark:text-white-100">Recent calls <span class="text-xs font-normal text-black-700 dark:text-black-600">last 7 days</span></h2>
        <select class="rounded-md border border-white-300 bg-white-100 px-2 py-1 text-sm dark:border-navy-600 dark:bg-navy-700" aria-label="Plugin" bind:value={callsPlugin} onchange={() => (callsPage = 1)}>
          <option value="">All plugins</option>
          {#each plugins as p (p.key)}<option value={p.key}>{p.key}</option>{/each}
        </select>
        <label class="flex items-center gap-1.5 text-sm text-black-800 dark:text-black-600">
          <input type="checkbox" bind:checked={failedOnly} onchange={() => (callsPage = 1)} /> Failed only
        </label>
      </div>
      <div class="mt-3 overflow-x-auto rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700">
        <table class="w-full text-left text-xs">
          <thead class="text-black-700 dark:text-black-600">
            <tr>{#each ["Time", "Plugin", "RPC", "Scope", "Item", "User", "Source", "Result", "Duration"] as h}<th class="px-3 py-2 font-medium">{h}</th>{/each}</tr>
          </thead>
          <tbody>
            {#each calls as c, i (i)}
              <tr class="border-t border-white-300 dark:border-navy-600 text-black-900 dark:text-white-100" data-testid="host-call">
                <td class="whitespace-nowrap px-3 py-1.5">{fmt(c.ts)}</td>
                <td class="px-3 py-1.5">{c.plugin}</td>
                <td class="px-3 py-1.5 font-mono">{c.rpc}</td>
                <td class="px-3 py-1.5 font-mono">{c.scope}</td>
                <td class="max-w-xs truncate px-3 py-1.5 font-mono">{c.item ?? ""}</td>
                <td class="px-3 py-1.5 font-mono">{c.user_id ?? ""}</td>
                <td class="px-3 py-1.5">{c.source ?? ""}</td>
                <td class={`px-3 py-1.5 ${c.ok ? "text-green-600" : "text-red-600"}`}>{c.ok ? "ok" : c.code || "failed"}</td>
                <td class="px-3 py-1.5">{c.duration_ms} ms</td>
              </tr>
            {:else}
              <tr><td colspan="9" class="px-3 py-4 text-center text-black-700 dark:text-black-600">No calls in the last 7 days.</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if callsTotal > PAGE}
        <div class="mt-2 flex items-center justify-end gap-2 text-xs text-black-700 dark:text-black-600" data-testid="calls-pager">
          <span>Page {callsPage} of {pages} · {callsTotal} calls</span>
          <Button size="sm" variant="secondary" disabled={callsPage <= 1} onclick={() => (callsPage -= 1)}>Previous</Button>
          <Button size="sm" variant="secondary" disabled={callsPage >= pages} onclick={() => (callsPage += 1)}>Next</Button>
        </div>
      {/if}
    </section>
  </div>
{/if}

<ConfirmDialog
  open={!!revoking}
  title="Revoke host tokens?"
  body={`Every Host API call of ${revoking ?? ""} is refused from now on, over both the broker and its tokens, and its next processes get no token — until you restore host access. The plugin itself keeps running.`}
  confirmLabel="Revoke"
  destructive
  onConfirm={revoke}
  onCancel={() => (revoking = null)}
/>
