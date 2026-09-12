<script lang="ts">
  import { onMount } from "svelte";
  import { Button } from "@wick-fe/common-ui";
  import { toastError } from "@wick-fe/common-stores";
  import {
    apiLoginTTYStatus,
    apiLoginTTYUsage,
    apiLoginTTYStart,
    apiLoginTTYUsageRefresh,
    usageLabel,
    fmtResetsIn,
    prettyPlan,
    type LoginTTYStatus,
    type LoginTTYSession,
    type UsageResult,
    type LoginAccount,
  } from "$lib/logintty.js";
  import LoginTerminalModal from "$lib/components/LoginTerminalModal.svelte";
  import UsageCacheChip from "$lib/components/UsageCacheChip.svelte";
  import { fmtSecsShort } from "$lib/usagerings.js";

  type Props = { base: string; type: string; name: string };
  let { base, type, name }: Props = $props();

  let status = $state<LoginTTYStatus | null>(null);
  let usage = $state<UsageResult | null>(null);
  let loading = $state(true);
  let starting = $state(false);
  let session = $state<LoginTTYSession | null>(null);
  let showTerminal = $state(false);
  /* Collapsed by default — the header row is the summary; details
     (account rows + usage) render only when expanded. */
  let expanded = $state(false);

  let connected = $derived(status?.account.connected ?? false);
  /* Reconnect shows while collapsed ONLY when a login is actually
     needed; expanded always offers it (deliberate re-login). */
  let showReconnect = $derived.by(() => {
    if (!status?.supported) return false;
    if (status.session?.state === "running") return false;
    return expanded || !connected;
  });

  async function refresh() {
    try {
      status = await apiLoginTTYStatus(base, type, name);
    } catch {
      status = null;
    }
    try {
      usage = await apiLoginTTYUsage(base, type, name);
    } catch {
      usage = null;
    }
  }

  /* Re-check: ask the server for a fresh reading now. Allowed even when
     numbers are already on screen — the server owns the decision about
     whether a probe may actually go out, and tells us the wait when it
     declines. */
  let rechecking = $state(false);
  let recheckWait = $state(0);

  async function recheckUsage() {
    rechecking = true;
    recheckWait = 0;
    try {
      const r = await apiLoginTTYUsageRefresh(base, type, name);
      if (!r.accepted) recheckWait = r.waitS;
      usage = await apiLoginTTYUsage(base, type, name);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Failed to re-check usage");
    } finally {
      rechecking = false;
    }
  }

  let usageBusy = $derived(rechecking || usage?.checking === true);

  onMount(async () => {
    await refresh();
    loading = false;
  });

  async function reconnect() {
    starting = true;
    try {
      const s = await apiLoginTTYStart(base, type, name);
      if (s) {
        session = s;
        showTerminal = true;
      }
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Failed to start login session");
    } finally {
      starting = false;
    }
  }

  function openRunning() {
    if (status?.session) {
      session = status.session;
      showTerminal = true;
    }
  }

  function onFinished(_account: LoginAccount | null) {
    void refresh();
  }

  function closeTerminal() {
    showTerminal = false;
    void refresh();
  }

  function fmtExpiry(iso: string): string {
    if (!iso) return "";
    const d = new Date(iso);
    if (isNaN(d.getTime())) return "";
    return d.toLocaleString();
  }

  function barColor(pct: number): string {
    if (pct >= 90) return "bg-neg-400";
    if (pct >= 70) return "bg-cau-400";
    return "bg-link-400";
  }

  // Account rows in the CLI's Account & Usage order. Empty values are
  // skipped at render.
  let accountRows = $derived.by(() => {
    if (!status) return [] as { label: string; value: string }[];
    const a = status.account;
    return [
      { label: "Auth method", value: a.authMethod },
      { label: "Email", value: a.email },
      { label: "Organization", value: a.org },
      { label: "Plan", value: prettyPlan(a.plan) },
    ].filter((r) => r.value !== "");
  });
</script>

<!-- Re-check button, shared by the usage header and the error line. The
     wait it prints after a refusal is the point: it says the probe was
     deliberately not sent, rather than leaving a dead-looking button. -->
{#snippet recheck()}
  <span class="inline-flex items-center gap-1">
    <button
      type="button"
      data-testid="panel-usage-recheck"
      class="rounded px-1.5 py-0.5 text-[11px] text-link-400 hover:bg-white-300 dark:hover:bg-navy-600 disabled:opacity-50 disabled:hover:bg-transparent"
      disabled={usageBusy}
      title={usageBusy ? "Checking usage…" : "Check this account's usage now"}
      onclick={(e) => {
        e.stopPropagation();
        void recheckUsage();
      }}
    >
      Re-check
    </button>
    {#if recheckWait > 0}
      <span class="text-[11px] text-black-600 dark:text-black-700">wait {fmtSecsShort(recheckWait)}</span>
    {/if}
  </span>
{/snippet}

{#if showTerminal && session}
  <LoginTerminalModal
    {base}
    {type}
    {name}
    session={session}
    extendS={status?.extendS ?? 300}
    onClose={closeTerminal}
    onFinished={onFinished}
  />
{/if}

<div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
  <!-- Summary row: status badge (+ email when connected); Reconnect
       only when a login is needed. Clicking the row toggles details;
       the action buttons stop propagation so they don't toggle. -->
  <div
    role="button"
    tabindex="0"
    aria-expanded={expanded}
    aria-label="Toggle connection details"
    onclick={() => { expanded = !expanded; }}
    onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); expanded = !expanded; } }}
    class="flex items-center gap-3 px-5 py-3 cursor-pointer select-none hover:bg-white-200 dark:hover:bg-navy-800 transition-colors {expanded ? 'border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800' : ''}"
  >
    <svg class="h-3.5 w-3.5 shrink-0 text-black-600 transition-transform {expanded ? 'rotate-90' : ''}" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path></svg>
    <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Connection</h3>
    {#if loading}
      <span class="text-xs text-black-700 dark:text-black-600">Checking…</span>
    {:else if status}
      {#if connected}
        <span class="rounded bg-pos-100 dark:bg-pos-400/20 px-2 py-0.5 text-xs font-semibold text-pos-400">Connected</span>
        {#if status.account.email}
          <span class="hidden sm:inline min-w-0 truncate font-mono text-xs text-black-800 dark:text-black-600">{status.account.email}</span>
        {/if}
      {:else}
        <span class="rounded bg-neg-100 dark:bg-neg-400/20 px-2 py-0.5 text-xs font-semibold text-neg-400">Not connected</span>
      {/if}
    {:else}
      <span class="text-xs text-black-700 dark:text-black-600">Status unavailable</span>
    {/if}
    <span class="ml-auto"></span>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <span onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()} class="flex items-center gap-2">
      {#if status?.session?.state === "running"}
        <Button variant="secondary" onclick={openRunning}>Open login terminal</Button>
      {:else if showReconnect}
        <Button variant="primary" disabled={starting} onclick={reconnect}>
          {starting ? "Starting…" : "Reconnect"}
        </Button>
      {/if}
    </span>
  </div>
  {#if expanded}
  <div class="p-5 space-y-4">
    {#if loading}
      <p class="text-xs text-black-700 dark:text-black-600">Checking credentials…</p>
    {:else if !status}
      <p class="text-xs text-black-700 dark:text-black-600">Connection status unavailable.</p>
    {:else}
      <!-- ACCOUNT — CLI Account & Usage layout: label left, value right.
           The connect badge lives in the summary row, not repeated here. -->
      <div class="space-y-2">
        <p class="text-[11px] font-semibold tracking-wide text-black-700 dark:text-black-600">ACCOUNT</p>
        {#each accountRows as row (row.label)}
          <div class="flex items-baseline justify-between gap-4 text-xs">
            <span class="shrink-0 text-black-800 dark:text-black-600">{row.label}</span>
            <span class="min-w-0 truncate text-right font-medium text-black-900 dark:text-white-100">{row.value}</span>
          </div>
        {/each}
        {#if status.account.connected && status.account.expiresAt}
          <p class="text-[11px] text-black-700 dark:text-black-600">Token expires {fmtExpiry(status.account.expiresAt)}</p>
        {/if}
        {#if !status.supported}
          <p class="text-[11px] text-black-700 dark:text-black-600">Reconnect via terminal is not available for this provider type yet.</p>
        {/if}
      </div>

      <!-- USAGE — one block per window: name + %, bar, resets-in -->
      {#if usage?.supported}
        {#if usage.windows.length > 0}
          <div class="space-y-3 pt-1">
            <div class="flex items-center justify-between gap-2">
              <p class="text-[11px] font-semibold tracking-wide text-black-700 dark:text-black-600">USAGE</p>
              {@render recheck()}
            </div>
            {#each usage.windows as w (w.key)}
              {@const resetsIn = fmtResetsIn(w.resetsAt, Date.now())}
              <div class="space-y-1">
                <div class="flex items-baseline justify-between gap-4 text-xs">
                  <span class="text-black-900 dark:text-white-100">{usageLabel(w.key)}</span>
                  <span class="font-medium text-black-900 dark:text-white-100">{Math.round(w.utilization)}%</span>
                </div>
                <div class="h-2 w-full rounded-full bg-white-300 dark:bg-navy-600 overflow-hidden">
                  <div class="h-full rounded-full {barColor(w.utilization)}" style={`width: ${Math.min(100, Math.max(0, w.utilization))}%`}></div>
                </div>
                {#if resetsIn}
                  <p class="text-[11px] text-black-700 dark:text-black-600">Resets in {resetsIn}</p>
                {/if}
              </div>
            {/each}
            <!-- Shared, cached reading (see UsageCacheChip): say its age
                 rather than implying this panel fetched it on open. -->
            <p class="flex items-center gap-1 text-[11px] text-black-700 dark:text-black-600">
              {#if usageBusy}
                <span data-testid="panel-usage-checking">Checking usage…</span>
              {:else}
                <UsageCacheChip ageS={usage.ageS} nextS={usage.nextS} fetchedAt={usage.fetchedAt} />
              {/if}
            </p>
          </div>
        {:else if usage.pending || usageBusy}
          <p class="text-[11px] text-black-700 dark:text-black-600">Checking usage… (probes are paced to stay under the endpoint's rate limit)</p>
        {:else if usage.error}
          <p class="text-[11px] text-black-700 dark:text-black-600">
            Usage unavailable: <span class="font-mono">{usage.error}</span>
            {#if usage.nextS > 0}<span class="text-black-600 dark:text-black-700"> — next probe in {fmtSecsShort(usage.nextS)}</span>{/if}
          </p>
          {@render recheck()}
        {/if}
      {/if}
    {/if}
  </div>
  {/if}
</div>
