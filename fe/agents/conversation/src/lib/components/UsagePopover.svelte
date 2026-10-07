<script lang="ts">
  /* The `/usage` popover: how much of THIS session's provider account is
     spent, without sending the user to the Providers page — which most of
     them cannot open, since that menu belongs to provider managers.

     Floats above the composer like the /thinking popover and the provider
     switcher, and never sends a message.

     Re-check is the ONLY thing that fetches: nothing probes the provider
     on a timer, so what you see is whatever was last read, and the
     footer says when that was. The cache owns the decision and refuses
     inside a cooldown (its own 10s floor, or a Retry-After the upstream
     asked for) — the button is disabled for that window rather than
     letting anyone press a rate limit into existence.

     Reconnect is NOT here: acting on the account belongs to the
     Providers menu.

     Two things it is careful about. The numbers come from a shared,
     paced server cache (opening this costs no upstream request), so it
     always says how old the reading is. And a provider type wick cannot
     read limits for — gemini, wick — says exactly that instead of
     rendering empty bars that read as "0% used".

     Codex is a third case: it has no usage endpoint, but it writes its
     rate limits into every turn's journal, so its numbers are real and
     free to read — and possibly hours old. Those windows carry their
     own observation time and are dated per window, because "last check
     just now" describes when WE looked, not when codex last knew. */
  import type { ComposerUsage, ComposerUsageAccount, ComposerUsageWindow } from "../api/usage.js";

  type Props = {
    open: boolean;
    data: ComposerUsage | null;
    loading: boolean;
    error: string;
    /** Ask for a fresh reading; the parent calls the endpoint + reloads. */
    onRecheck: () => void;
    rechecking: boolean;
    /** Seconds the server said to wait, when it declined the last click. */
    recheckWait: number;
    onClose: () => void;
  };

  let { open, data, loading, error, onRecheck, rechecking, recheckWait, onClose }: Props = $props();

  let el: HTMLDivElement | undefined = $state();

  /* Multi-account instances (omp's pool, opencode's account folders):
     Summary is one line per account so every account's remaining quota
     is visible at once; Detail is the full bars per account. Both read
     the same cached probe — switching costs nothing. */
  let view = $state<"summary" | "detail">("summary");
  const accounts = $derived<ComposerUsageAccount[]>(
    [...(data?.accounts ?? [])].sort((a, b) => Number(b.current) - Number(a.current)),
  );

  function shortLabel(key: string): string {
    if (key === "five_hour") return "5h";
    if (key === "seven_day") return "7d";
    if (key === "seven_day_opus") return "7d Opus";
    if (key === "seven_day_fable") return "7d Fable";
    return key;
  }

  function textTone(pct: number): string {
    if (pct >= 90) return "text-red-600 dark:text-red-400";
    if (pct >= 70) return "text-amber-600 dark:text-amber-400";
    return "text-black-900 dark:text-white-100";
  }

  /* The soonest reset among an account's windows that are nearly spent —
     the one moment that matters when an account is the bottleneck. */
  function nextReset(ws: ComposerUsageWindow[]): string {
    let best = Infinity;
    for (const w of ws) {
      const t = Date.parse(w.resetsAt);
      if (Number.isFinite(t) && t > Date.now() && t < best) best = t;
    }
    return best === Infinity ? "" : shortDuration((best - Date.now()) / 1000);
  }

  function accountName(a: ComposerUsageAccount): string {
    return a.email || a.label || a.id;
  }

  $effect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); onClose(); }
    }
    function onDown(e: MouseEvent) {
      if (el && !el.contains(e.target as Node)) onClose();
    }
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("mousedown", onDown, true);
    return () => {
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("mousedown", onDown, true);
    };
  });

  /* Window labels match the provider page so the same number is called the
     same thing in both places; an unknown key prints as-is rather than
     being dropped, since the upstream adds windows over time. */
  function windowLabel(key: string): string {
    if (key === "five_hour") return "Session (5hr)";
    if (key === "seven_day") return "Weekly (7 day)";
    if (key === "seven_day_opus") return "Weekly (Opus)";
    if (key === "seven_day_fable") return "Weekly (Fable)";
    return key;
  }

  function barColor(pct: number): string {
    if (pct >= 90) return "bg-red-500";
    if (pct >= 70) return "bg-amber-500";
    return "bg-blue-500";
  }

  /* How old the provider's own figure is, for providers that publish
     limits only while they run (codex writes them into each turn's
     journal). Empty when the number is live — claude answers a request
     we just made, and dating that would be noise. Under a minute is
     also empty: "as of 12s ago" says nothing "last check" does not. */
  function observedAgo(iso: string | undefined): string {
    if (!iso) return "";
    const at = Date.parse(iso);
    if (Number.isNaN(at)) return "";
    const s = Math.round((Date.now() - at) / 1000);
    if (s < 60) return "";
    return `${shortDuration(s)} ago`;
  }

  function shortDuration(seconds: number): string {
    const s = Math.max(0, Math.round(seconds));
    if (s < 60) return `${s}s`;
    if (s < 3600) return `${Math.round(s / 60)}m`;
    if (s < 86400) return `${Math.round(s / 3600)}h`;
    return `${Math.round(s / 86400)}d`;
  }

  function resetsIn(resetsAt: string): string {
    if (!resetsAt) return "";
    const t = new Date(resetsAt).getTime();
    if (Number.isNaN(t)) return "";
    const left = (t - Date.now()) / 1000;
    return left > 0 ? shortDuration(left) : "";
  }
</script>

{#snippet windowBars(ws: ComposerUsageWindow[])}
  <div class="space-y-2">
    {#each ws as w (w.key)}
      {@const pct = Math.min(100, Math.max(0, Math.round(w.utilization)))}
      {@const reset = resetsIn(w.resetsAt)}
      {@const observed = observedAgo(w.observedAt)}
      <div class="space-y-1">
        <div class="flex items-baseline justify-between gap-4 text-[11px]">
          <span class="text-black-900 dark:text-white-100">{windowLabel(w.key)}</span>
          <span class="font-medium text-black-900 dark:text-white-100">{pct}%</span>
        </div>
        <div class="h-1.5 w-full rounded-full bg-white-300 dark:bg-navy-600 overflow-hidden">
          <div class={`h-full rounded-full ${barColor(pct)}`} style={`width: ${pct}%`}></div>
        </div>
        {#if reset || observed}
          <p class="text-[10px] text-black-600 dark:text-black-700">
            {#if reset}Resets in {reset}{/if}{#if reset && observed} ·
            {/if}{#if observed}<span data-testid="observed-at">as of {observed}</span>{/if}
          </p>
        {/if}
      </div>
    {/each}
  </div>
{/snippet}

{#snippet accountBadges(a: ComposerUsageAccount)}
  {#if a.current}
    <span data-testid="usage-account-current" class="shrink-0 rounded bg-link-400/15 px-1.5 py-0.5 text-[10px] font-semibold text-link-400">this session</span>
  {/if}
  {#if a.status === "disabled"}
    <span class="shrink-0 rounded bg-neg-100 dark:bg-neg-400/20 px-1.5 py-0.5 text-[10px] font-semibold text-neg-400">disabled</span>
  {/if}
  {#if a.plan}
    <span class="shrink-0 rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[10px] text-black-800 dark:text-black-600">{a.plan}</span>
  {/if}
{/snippet}

{#snippet accountsBlock()}
  <div class="space-y-2" data-testid="usage-accounts">
    <div class="flex items-center justify-between gap-2">
      <p class="text-[11px] font-semibold tracking-wide text-black-700 dark:text-black-600">ACCOUNTS ({accounts.length})</p>
      <div class="inline-flex rounded-md border border-white-300 dark:border-navy-600 overflow-hidden text-[10px]">
        {#each [["summary", "Summary"], ["detail", "Detail"]] as [v, label] (v)}
          <button
            type="button"
            data-testid="usage-view-{v}"
            class="px-2 py-0.5 {view === v ? 'bg-white-300 dark:bg-navy-600 font-semibold text-black-900 dark:text-white-100' : 'text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700'}"
            onclick={() => (view = v as "summary" | "detail")}
          >{label}</button>
        {/each}
      </div>
    </div>
    {#if data?.rotation}
      <p data-testid="usage-rotation" class="text-[10px] text-black-600 dark:text-black-700">
        This session is on Auto — {data.rotation === "omp" ? "omp picks the account and rotates when one hits its limit" : "wick moves to the next account when one hits its limit"}.
      </p>
    {/if}
    {#if data?.pending || data?.checking}
      <p class="text-xs text-black-700 dark:text-black-600">Checking usage…</p>
    {:else if data?.error}
      <p class="font-mono text-[11px] text-black-700 dark:text-black-600">usage unavailable: {data.error}</p>
    {/if}
    {#if view === "summary"}
      <div class="rounded-lg border border-white-300 dark:border-navy-600 divide-y divide-white-300 dark:divide-navy-600">
        {#each accounts as a (a.id)}
          {@const reset = nextReset(a.windows)}
          <div data-testid="usage-account-row" class="px-2.5 py-1.5 text-[11px] {a.current ? 'bg-link-400/10' : ''}">
            <div class="flex items-center gap-1.5 min-w-0">
              <span class="min-w-0 truncate text-black-900 dark:text-white-100" title={accountName(a)}>{accountName(a)}</span>
              <span class="shrink-0 font-mono text-[10px] text-black-600 dark:text-black-700">{a.provider}</span>
              <span class="ml-auto"></span>
              {@render accountBadges(a)}
            </div>
            <div class="mt-0.5 flex flex-wrap items-center gap-x-2.5 gap-y-0.5 tabular-nums">
              {#if a.error}
                <span class="text-black-700 dark:text-black-600">{a.error}</span>
              {:else if a.noUsage}
                <span class="text-black-600 dark:text-black-700">no usage reported</span>
              {:else if a.windows.length === 0}
                <span class="text-black-600 dark:text-black-700">{data?.pending || data?.checking ? "…" : "no reading yet"}</span>
              {:else}
                {#each a.windows as w (w.key)}
                  {@const pct = Math.min(100, Math.max(0, Math.round(w.utilization)))}
                  <span class="inline-flex items-center gap-1">
                    <span class="text-black-700 dark:text-black-600">{shortLabel(w.key)}</span>
                    <span class="inline-block h-1 w-8 rounded-full bg-white-300 dark:bg-navy-600 overflow-hidden"><span class={`block h-full ${barColor(pct)}`} style={`width: ${pct}%`}></span></span>
                    <span class="font-medium {textTone(pct)}">{pct}%</span>
                  </span>
                {/each}
                {#if reset}<span class="text-[10px] text-black-600 dark:text-black-700">reset {reset}</span>{/if}
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {:else}
      <div class="space-y-3">
        {#each accounts as a (a.id)}
          <div data-testid="usage-account-detail" class="rounded-lg border px-2.5 py-2 space-y-2 {a.current ? 'border-link-400/60' : 'border-white-300 dark:border-navy-600'}">
            <div class="flex items-center gap-1.5 min-w-0 text-[11px]">
              <span class="min-w-0 truncate font-medium text-black-900 dark:text-white-100">{accountName(a)}</span>
              <span class="shrink-0 font-mono text-[10px] text-black-600 dark:text-black-700">{a.provider}</span>
              <span class="ml-auto"></span>
              {@render accountBadges(a)}
            </div>
            {#if a.error}
              <p class="font-mono text-[11px] text-black-700 dark:text-black-600">usage unavailable: {a.error}</p>
            {:else if a.noUsage}
              <p class="text-[11px] text-black-600 dark:text-black-700">This login's provider does not report usage limits.</p>
            {:else if a.windows.length === 0}
              <p class="text-[11px] text-black-600 dark:text-black-700">No usage windows reported.</p>
            {:else}
              {@render windowBars(a.windows)}
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </div>
{/snippet}

{#if open}
  <div
    bind:this={el}
    data-usage-popup
    class="absolute bottom-full left-0 right-0 mb-2 z-30 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg overflow-hidden"
  >
    <div class="flex items-center gap-2 px-3 py-2 border-b border-white-300 dark:border-navy-600">
      <span class="text-xs font-semibold text-black-900 dark:text-white-100">Usage</span>
      {#if data}
        <span class="font-mono text-[11px] text-black-700 dark:text-black-600">{data.provider}</span>
        {#if data.account?.connected}
          <span class="rounded bg-pos-100 dark:bg-pos-400/20 px-1.5 py-0.5 text-[10px] font-medium text-pos-400">Connected</span>
        {:else}
          <span class="rounded bg-neg-100 dark:bg-neg-400/20 px-1.5 py-0.5 text-[10px] font-medium text-neg-400">Not connected</span>
        {/if}
      {/if}
      {#if data?.supported}
        <span class="ml-auto inline-flex items-center gap-1">
          {#if recheckWait > 0}
            <span class="text-[10px] text-black-600 dark:text-black-700" title="A probe now would land inside a cooldown, so it was not sent">wait {shortDuration(recheckWait)}</span>
          {/if}
          <button
            type="button"
            data-testid="usage-recheck"
            class="rounded px-1.5 py-0.5 text-[11px] text-link-400 hover:bg-white-300 dark:hover:bg-navy-600 disabled:opacity-50"
            disabled={rechecking || data.checking}
            title="Check this account's usage now"
            onclick={onRecheck}
          >{rechecking || data.checking ? "Checking…" : "Re-check"}</button>
        </span>
      {/if}
    </div>

    <div class="p-3 space-y-3">
      {#if loading}
        <p class="text-xs text-black-600 dark:text-black-700">Loading…</p>
      {:else if error}
        <p class="text-xs text-black-700 dark:text-black-600">{error}</p>
      {:else if data}
        {#if accounts.length > 0}
          {@render accountsBlock()}
        {:else}
        {#if data.account?.email}
          <div class="space-y-1 text-[11px]">
            <p class="font-semibold tracking-wide text-black-700 dark:text-black-600">ACCOUNT</p>
            <div class="flex justify-between gap-4">
              <span class="text-black-700 dark:text-black-600">Email</span>
              <span class="font-mono text-black-900 dark:text-white-100 truncate">{data.account.email}</span>
            </div>
            {#if data.account.plan}
              <div class="flex justify-between gap-4">
                <span class="text-black-700 dark:text-black-600">Plan</span>
                <span class="text-black-900 dark:text-white-100">{data.account.plan}</span>
              </div>
            {/if}
            {#if data.account.org}
              <div class="flex justify-between gap-4">
                <span class="text-black-700 dark:text-black-600">Organization</span>
                <span class="text-black-900 dark:text-white-100">{data.account.org}</span>
              </div>
            {/if}
          </div>
        {/if}

        {#if !data.supported}
          <!-- Not a failure: this provider type has no usage API at all. -->
          <p data-testid="usage-unsupported" class="text-xs text-black-700 dark:text-black-600">
            {data.reason || "This provider does not report usage limits."}
          </p>
        {:else if data.error}
          <p class="font-mono text-[11px] text-black-700 dark:text-black-600">usage unavailable: {data.error}</p>
          {#if data.nextS > 0}
            <p class="text-[11px] text-black-600 dark:text-black-700">next probe in {shortDuration(data.nextS)}</p>
          {/if}
        {:else if data.pending || data.checking}
          <p class="text-xs text-black-700 dark:text-black-600">Checking usage…</p>
        {:else if data.windows.length === 0}
          <p class="text-xs text-black-700 dark:text-black-600">No usage windows reported.</p>
        {:else}
          <div class="space-y-2">
            <p class="text-[11px] font-semibold tracking-wide text-black-700 dark:text-black-600">USAGE</p>
            {#each data.windows as w (w.key)}
              {@const pct = Math.min(100, Math.max(0, Math.round(w.utilization)))}
              {@const reset = resetsIn(w.resetsAt)}
              {@const observed = observedAgo(w.observedAt)}
              <div class="space-y-1">
                <div class="flex items-baseline justify-between gap-4 text-[11px]">
                  <span class="text-black-900 dark:text-white-100">{windowLabel(w.key)}</span>
                  <span class="font-medium text-black-900 dark:text-white-100">{pct}%</span>
                </div>
                <div class="h-1.5 w-full rounded-full bg-white-300 dark:bg-navy-600 overflow-hidden">
                  <div class={`h-full rounded-full ${barColor(pct)}`} style={`width: ${pct}%`}></div>
                </div>
                {#if reset || observed}
                  <p class="text-[10px] text-black-600 dark:text-black-700">
                    {#if reset}Resets in {reset}{/if}{#if reset && observed} ·
                    {/if}{#if observed}<span data-testid="observed-at">as of {observed}</span>{/if}
                  </p>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
        {/if}

        {#if data.ageS >= 0 && data.fetchedAt}
          <!-- Provenance: this is a shared cached reading, not a fetch made
               when the popover opened. -->
          <p class="flex items-center gap-1 text-[10px] text-black-600 dark:text-black-700">
            <span aria-hidden="true">↻</span>
            last check {data.ageS < 2 ? "just now" : `${shortDuration(data.ageS)} ago`}
            {#if data.nextS > 0}· re-check in {shortDuration(data.nextS)}{/if}
          </p>
        {/if}
      {/if}
    </div>
  </div>
{/if}
