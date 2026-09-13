<script lang="ts">
  /* The `/usage` popover: how much of THIS session's provider account is
     spent, without sending the user to the Providers page — which most of
     them cannot open, since that menu belongs to provider managers.

     Floats above the composer like the /thinking popover and the provider
     switcher, and never sends a message.

     Re-check asks the server's cache for a fresh reading. The cache owns
     the decision and refuses inside a cooldown (its own 10s floor, or a
     Retry-After the upstream asked for), answering with the wait instead
     — which is why the last-check line matters: you can see there is no
     point pressing it again yet. Reconnect is NOT here: acting on the
     account belongs to the Providers menu.

     Two things it is careful about. The numbers come from a shared,
     paced server cache (opening this costs no upstream request), so it
     always says how old the reading is. And a provider type with no usage
     API — codex, gemini, wick — says exactly that instead of rendering
     empty bars that read as "0% used". */
  import type { ComposerUsage } from "../api/usage.js";

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
              <div class="space-y-1">
                <div class="flex items-baseline justify-between gap-4 text-[11px]">
                  <span class="text-black-900 dark:text-white-100">{windowLabel(w.key)}</span>
                  <span class="font-medium text-black-900 dark:text-white-100">{pct}%</span>
                </div>
                <div class="h-1.5 w-full rounded-full bg-white-300 dark:bg-navy-600 overflow-hidden">
                  <div class={`h-full rounded-full ${barColor(pct)}`} style={`width: ${pct}%`}></div>
                </div>
                {#if reset}
                  <p class="text-[10px] text-black-600 dark:text-black-700">Resets in {reset}</p>
                {/if}
              </div>
            {/each}
          </div>
        {/if}

        {#if data.ageS >= 0 && data.fetchedAt}
          <!-- Provenance: this is a shared cached reading, not a fetch made
               when the popover opened. -->
          <p class="flex items-center gap-1 text-[10px] text-black-600 dark:text-black-700">
            <span aria-hidden="true">↻</span>
            last check {data.ageS < 2 ? "just now" : `${shortDuration(data.ageS)} ago`}
            {#if data.nextS > 0}· auto refresh in {shortDuration(data.nextS)}{/if}
          </p>
        {/if}
      {/if}
    </div>
  </div>
{/if}
