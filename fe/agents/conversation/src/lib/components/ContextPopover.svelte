<script lang="ts">
  /* The Context window panel — what `/context` opens, and what the ring
     in the composer opens when clicked.

     Floats above the composer like /usage and /thinking, and like them it
     never sends a message on its own. The one action it offers, Compact,
     DOES send one, so it is behind a confirm: compaction spends tokens
     and permanently replaces older turns with a summary. Nothing here
     ever compacts automatically — the whole point of showing the number
     is that a person decides.

     Two different numbers share this panel and must not be confused:
     the fill level (how full the window is NOW, replaced each turn) and
     the spend (what the session has burned in total, which only grows).
     They are labelled and grouped separately for that reason. */
  import type { SessionContext, SessionContextProvider } from "../api/context.js";

  type Props = {
    open: boolean;
    data: SessionContext | null;
    loading: boolean;
    error: string;
    /** Ask the server for a fresh reading. */
    onRefresh: () => void;
    /** Send /compact. The caller owns the send; this panel only asks. */
    onCompact: () => void;
    /** True while a compaction is in flight, so the button can't be
        double-fired — compacting twice in a row is pure waste. */
    compacting: boolean;
    onClose: () => void;
  };

  let { open, data, loading, error, onRefresh, onCompact, compacting, onClose }: Props = $props();

  let el: HTMLDivElement | undefined = $state();
  let confirming = $state(false);

  $effect(() => {
    if (!open) {
      confirming = false;
      return;
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        onClose();
      }
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

  function short(n: number): string {
    if (!Number.isFinite(n) || n <= 0) return "0";
    if (n < 1000) return String(n);
    if (n < 1_000_000) return `${(n / 1000).toFixed(n < 10_000 ? 1 : 0)}k`;
    return `${(n / 1_000_000).toFixed(2)}M`;
  }
  function exact(n: number): string {
    return Number.isFinite(n) ? n.toLocaleString() : "0";
  }
  function cost(usd: number): string {
    if (!Number.isFinite(usd) || usd <= 0) return "—";
    if (usd < 0.01) return "<$0.01";
    return `$${usd.toFixed(2)}`;
  }

  const pct = $derived(Math.max(0, Math.min(100, data?.pct ?? 0)));
  const hasWindow = $derived((data?.window ?? 0) > 0);
  const tone = $derived(pct >= 90 ? "red" : pct >= 75 ? "amber" : "green");
  const barClass = $derived(
    tone === "red" ? "bg-red-500" : tone === "amber" ? "bg-amber-500" : "bg-green-500",
  );

  /* The advice line is the reason this panel exists: a percentage alone
     does not tell anyone what to do about it. */
  const advice = $derived(
    !hasWindow
      ? "This provider doesn't report a window size, so there's no fill to show — only what's been spent."
      : pct >= 90
        ? "Nearly full. The provider will compact on its own during one of the next turns — compact now if you'd rather choose when."
        : pct >= 75
          ? "Filling up. Still fine, but a few more tool-heavy turns will reach the limit."
          : "Plenty of room.",
  );

  /* Whether the Compact action can do anything here. The server
     decides (it knows the provider); undefined means an older server
     that never said, and the old behaviour — offering it — is the safe
     reading of silence. */
  const canCompact = $derived(data?.can_compact !== false);
  const compactNote = $derived(data?.compact_note ?? "");

  /* What the session has actually put on the wire. The fill level above
     is one request's worth; this is every request added up, which is
     the number people mean by "how much has this session used". They
     differ by a lot once a conversation is long — the same prefix is
     re-sent (and mostly re-read from cache) every turn. */
  const spentTokens = $derived(data?.totals.total ?? 0);
  const cacheHit = $derived(Math.round(data?.totals.cache_hit_pct ?? 0));

  const others = $derived<SessionContextProvider[]>(
    (data?.providers ?? []).filter((p) => p.provider !== data?.provider),
  );

  /* Sparkline over the recent per-turn levels. Drawn from the same
     numbers as the ring, so a rise the ring cannot express ("we jumped
     30% in one turn") is still visible. */
  const spark = $derived.by(() => {
    const t = data?.trend ?? [];
    if (t.length < 2) return "";
    const max = Math.max(...t, 1);
    const w = 100 / (t.length - 1);
    return t.map((v, i) => `${(i * w).toFixed(1)},${(20 - (v / max) * 18).toFixed(1)}`).join(" ");
  });
</script>

{#if open}
  <div
    bind:this={el}
    class="absolute bottom-full left-0 right-0 mb-2 z-30 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-lg overflow-hidden"
    role="dialog"
    aria-label="Context window"
  >
    <div
      class="px-4 py-2.5 flex items-center justify-between border-b border-white-300 dark:border-navy-600"
    >
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Context window</h2>
      <div class="flex items-center gap-1">
        <button
          type="button"
          class="rounded-lg px-2 py-1 text-xs font-medium text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
          onclick={onRefresh}
          disabled={loading}
        >
          {loading ? "…" : "Refresh"}
        </button>
        <button
          type="button"
          aria-label="Close"
          class="rounded-lg px-2 py-1 text-xs text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
          onclick={onClose}>✕</button
        >
      </div>
    </div>

    {#if error}
      <p class="px-4 py-5 text-xs text-red-600 dark:text-red-400">{error}</p>
    {:else if !data || (data.turns === 0 && !data.provider)}
      <p class="px-4 py-5 text-xs text-black-700 dark:text-black-600">
        No reading yet — the window is measured when a turn finishes.
      </p>
    {:else}
      <div class="px-4 py-3">
        <div class="flex items-baseline justify-between gap-2">
          <p class="text-2xl font-bold text-black-900 dark:text-white-100 tabular-nums">
            {#if hasWindow}{Math.round(pct)}%{:else}{short(data.used)}{/if}
          </p>
          <p
            class="text-xs text-black-700 dark:text-black-600 tabular-nums"
            title="{exact(data.used)} of {exact(data.window)} tokens"
          >
            {short(data.used)}{#if hasWindow}
              / {short(data.window)}{/if} tokens
          </p>
        </div>

        {#if hasWindow}
          <div class="mt-2 h-2 rounded-full bg-white-300 dark:bg-navy-600 overflow-hidden">
            <div class="h-full rounded-full {barClass}" style="width: {pct}%"></div>
          </div>
        {/if}

        <p class="mt-2 text-xs text-black-700 dark:text-black-600">{advice}</p>

        {#if spark}
          <svg
            viewBox="0 0 100 20"
            preserveAspectRatio="none"
            class="mt-2 h-8 w-full text-black-700 dark:text-black-600"
            aria-label="Recent context level per turn"
          >
            <polyline
              points={spark}
              fill="none"
              stroke="currentColor"
              stroke-width="1"
              vector-effect="non-scaling-stroke"
            />
          </svg>
          <p class="text-[11px] text-black-700 dark:text-black-600">
            last {data.trend?.length ?? 0} turns
          </p>
        {/if}
      </div>

      <div
        class="px-4 py-2.5 border-t border-white-300 dark:border-navy-600 grid grid-cols-4 gap-2 text-xs"
      >
        <div>
          <p class="text-black-700 dark:text-black-600">Provider</p>
          <p class="font-medium text-black-900 dark:text-white-100 truncate" title={data.model}>
            {data.provider || "—"}
          </p>
        </div>
        <div>
          <p class="text-black-700 dark:text-black-600">Turns</p>
          <p class="font-medium text-black-900 dark:text-white-100 tabular-nums">{data.turns}</p>
        </div>
        <div>
          <!-- Session total, not the fill level: every token this
               session sent and received, which is what the bill is made
               of. Labelled "Tokens" rather than "Used" precisely so it
               is not read as the meter above. -->
          <p class="text-black-700 dark:text-black-600">Tokens</p>
          <p
            class="font-medium text-black-900 dark:text-white-100 tabular-nums"
            data-testid="session-tokens"
            title="{exact(spentTokens)} tokens this session — in {exact(data.totals.input)} · cache {exact(
              data.totals.cache_read,
            )} · out {exact(data.totals.output)}"
          >
            {short(spentTokens)}
          </p>
        </div>
        <div>
          <p class="text-black-700 dark:text-black-600">Spent</p>
          <p class="font-medium text-black-900 dark:text-white-100 tabular-nums">
            {cost(data.totals.cost_usd)}
          </p>
        </div>
      </div>

      {#if spentTokens > 0}
        <!-- The split behind that one number. Cache read is called out
             because on a long session it is most of the traffic, and
             seeing it is how someone understands why the total dwarfs
             the window. -->
        <div
          class="px-4 pb-2.5 -mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-black-700 dark:text-black-600 tabular-nums"
        >
          <span title="Fresh input tokens — the part that missed the cache">
            in {short(data.totals.input)}
          </span>
          <span title="Input served from the prompt cache">
            cache {short(data.totals.cache_read)}{#if cacheHit > 0}
              · {cacheHit}% hit{/if}
          </span>
          <span title="Tokens the model generated, reasoning included">
            out {short(data.totals.output)}
          </span>
        </div>
      {/if}

      {#if others.length > 0}
        <div class="px-4 py-2 border-t border-white-300 dark:border-navy-600">
          <p class="text-[11px] text-black-700 dark:text-black-600 mb-1">
            Also used in this session
          </p>
          {#each others as p (p.provider)}
            <div class="flex items-center justify-between text-xs py-0.5">
              <span class="text-black-900 dark:text-white-100 truncate">{p.provider}</span>
              <span class="text-black-700 dark:text-black-600 tabular-nums shrink-0 ml-2">
                {p.turns} turns · {short(p.totals.total)}
              </span>
            </div>
          {/each}
        </div>
      {/if}

      <div class="px-4 py-2.5 border-t border-white-300 dark:border-navy-600">
        {#if !canCompact}
          <!-- A provider that cannot compact gets no button. Offering
               one that only makes the model SAY it compacted is worse
               than offering nothing: the meter would keep climbing
               while the transcript claimed otherwise. So the panel
               states the situation and stops. -->
          <div
            class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-3 py-2"
            data-testid="compact-unavailable"
          >
            <p class="text-xs font-medium text-black-900 dark:text-white-100">
              No manual compaction here
            </p>
            <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">
              {compactNote ||
                "This provider has no /compact — it compacts on its own once it reaches its limit."}
            </p>
          </div>
        {:else if confirming}
          <!-- The confirm states the two costs in the order they bite:
               what is lost (older turns), then what is spent (the
               summarization). Both are consequences people have been
               surprised by. -->
          <div class="rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2">
            <p class="text-xs font-medium text-black-900 dark:text-white-100">
              Compact this conversation?
            </p>
            <ul class="mt-1 space-y-0.5 text-[11px] text-black-700 dark:text-black-600">
              <li>· Older turns are replaced by a summary — not reversible.</li>
              <li>· The summary is written by the model, so it costs tokens.</li>
              {#if data.used > 0}
                <li>· Frees roughly {short(Math.round(data.used * 0.8))} tokens of room.</li>
              {/if}
            </ul>
            <div class="mt-2 flex items-center gap-2">
              <button
                type="button"
                class="rounded-lg bg-amber-500 px-3 py-1.5 text-xs font-semibold text-white-100 hover:bg-amber-600 disabled:opacity-50"
                onclick={() => {
                  confirming = false;
                  onCompact();
                }}
                disabled={compacting}>Yes, compact</button
              >
              <button
                type="button"
                class="rounded-lg px-3 py-1.5 text-xs font-medium text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
                onclick={() => (confirming = false)}>Cancel</button
              >
            </div>
          </div>
        {:else}
          <!-- Not a primary action: compacting is something you do when
               the meter says so, not the point of opening this panel. So
               it reads as a quiet control that grows a warning tint on
               hover — and turns amber on its own once the window is
               filling, which is the moment it stops being optional. -->
          <button
            type="button"
            class="group w-full inline-flex items-center justify-center gap-2 rounded-lg border px-3 py-2 text-xs font-medium transition-colors disabled:opacity-50 {pct >=
            75
              ? 'border-amber-500/50 bg-amber-500/10 text-amber-700 dark:text-amber-300 hover:bg-amber-500/20'
              : 'border-white-300 dark:border-navy-600 text-black-900 dark:text-white-100 hover:border-amber-500/40 hover:bg-amber-500/10'}"
            onclick={() => (confirming = true)}
            disabled={compacting || (data.turns ?? 0) === 0}
          >
            {#if compacting}
              <svg class="h-3.5 w-3.5 animate-spin" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <circle cx="8" cy="8" r="6" stroke="currentColor" stroke-width="2" class="opacity-25" />
                <path d="M14 8a6 6 0 00-6-6" stroke="currentColor" stroke-width="2" stroke-linecap="round" />
              </svg>
              <span>Compacting…</span>
            {:else}
              <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                <path d="M3 4h10M4.5 8h7M6 12h4" stroke-linecap="round"></path>
                <path d="M8 6.5v3" stroke-linecap="round" class="opacity-0 group-hover:opacity-100"></path>
              </svg>
              <span>Compact conversation</span>
              {#if pct >= 75}
                <span class="rounded bg-amber-500/20 px-1.5 py-0.5 text-[10px] font-semibold">recommended</span>
              {/if}
            {/if}
          </button>
          <!-- Not "nothing compacts on its own": the providers do. Claude
               Code auto-compacts unless DISABLE_AUTO_COMPACT is set, and
               the wick engine folds the oldest history once the estimate
               passes 80% of the budget. What is manual is THIS button —
               wick never fires it for you, so pressing it is how you pick
               the moment instead of being surprised by it. -->
          <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600 text-center">
            wick never presses this for you — but the provider compacts on its own once the window
            fills.
          </p>
        {/if}
      </div>
    {/if}
  </div>
{/if}
