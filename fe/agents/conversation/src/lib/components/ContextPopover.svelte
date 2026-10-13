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
  import { contextTone } from "@wick-fe/common-ui";

  import type { ContextQuery, SessionContext, SessionContextProvider, UsageTotals } from "../api/context.js";
  import type { ComposerUsage } from "../api/usage.js";
  import ContextTrend from "./ContextTrend.svelte";

  /* What the turn on screen is doing right now. Everything else in this
     panel is written when a turn FINISHES — which is exactly the moment
     it stops being interesting on a turn that has been going for four
     minutes and has said nothing. */
  export type LiveTurnState = {
    /** A turn is running. */
    active: boolean;
    /** Epoch ms the turn started; 0 when nothing is running. */
    startedAt: number;
    /** Steps taken so far — thinking blocks, tool calls, replies. The
        same count the trace shows. */
    steps: number;
    /** wick's own lifecycle substate ("spawning", "thinking", …). */
    substate?: string;
    /** The tool being run, when one is. */
    toolName?: string;
    /** The newest window reading off the stream, in tokens; 0 when the
        provider has not reported one this turn. Same number the ledger
        will record when the turn ends — it just arrives sooner. */
    used?: number;
  };

  type Props = {
    open: boolean;
    data: SessionContext | null;
    /** The running turn, if any. Provider-agnostic: it comes from wick's
        event stream, so it reads the same on claude, codex and wick's
        own provider. */
    live?: LiveTurnState | null;
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
    /** The provider account's quota, from the same cached reading /usage
        shows. The window above is how full THIS conversation is; this is
        how much of the account is left — the two answer different
        questions, so the line is small and links to the full panel. */
    usage?: ComposerUsage | null;
    /** Ask the usage cache for a fresh reading (it may decline). */
    onUsageRefresh?: () => void;
    usageRefreshing?: boolean;
    /** Seconds the server said to wait when it declined the last click. */
    usageRecheckWait?: number;
    /** Close this panel and open /usage for the per-account detail. */
    onOpenUsage?: () => void;
    /** Fetch the reading with every stored point and/or every
        provider's trail, for the chart's All / All providers. */
    loadSeries?: (q: ContextQuery) => Promise<SessionContext>;
  };

  let {
    open, data, live = null, loading, error, onRefresh, onCompact, compacting, onClose,
    usage = null, onUsageRefresh, usageRefreshing = false, usageRecheckWait = 0, onOpenUsage,
    loadSeries,
  }: Props = $props();

  /* The windows the usage line summarises: the account this session runs
     on when the instance holds several, else the instance headline. */
  const usageWindows = $derived.by(() => {
    if (!usage?.supported) return [];
    const cur = usage.accounts.find((a) => a.current);
    return cur ? cur.windows : usage.windows;
  });
  const usageLabel = (key: string) =>
    key === "five_hour" ? "5h" : key === "seven_day" ? "7d" : key.replace(/^seven_day_/, "7d ");
  const usageTone = (pct: number) =>
    pct >= 90 ? "text-red-600 dark:text-red-400" : pct >= 70 ? "text-amber-600 dark:text-amber-400" : "text-black-900 dark:text-white-100";
  const usageReset = $derived.by(() => {
    let best = Infinity;
    for (const w of usageWindows) {
      const t = Date.parse(w.resetsAt);
      if (Number.isFinite(t) && t > Date.now() && t < best) best = t;
    }
    if (best === Infinity) return "";
    const s = Math.round((best - Date.now()) / 1000);
    return s < 3600 ? `${Math.max(1, Math.round(s / 60))}m` : s < 86400 ? `${Math.round(s / 3600)}h` : `${Math.round(s / 86400)}d`;
  });

  /* A clock that ticks while a turn is running and the panel is open.

     One second, not the app-wide 30s `now` store: the whole complaint is
     that a long turn looks identical to a stuck one, and a readout that
     only moves twice a minute does not answer that. It runs only while
     this panel is open, so a closed panel costs nothing. */
  let tick = $state(Date.now());
  $effect(() => {
    if (!open || !live?.active) return;
    tick = Date.now();
    const id = setInterval(() => { tick = Date.now(); }, 1000);
    return () => clearInterval(id);
  });

  const elapsedMs = $derived(
    live?.active && live.startedAt > 0 ? Math.max(0, tick - live.startedAt) : 0,
  );

  /** m:ss up to an hour, then h:mm:ss. Seconds stay visible throughout —
      "3:01" moving every second is what says the turn is alive. */
  function clock(ms: number): string {
    const total = Math.floor(ms / 1000);
    const s = total % 60;
    const m = Math.floor(total / 60) % 60;
    const h = Math.floor(total / 3600);
    const two = (n: number) => String(n).padStart(2, "0");
    return h > 0 ? `${h}:${two(m)}:${two(s)}` : `${m}:${two(s)}`;
  }

  /* What it is doing, in the words the rest of the UI already uses. The
     tool name wins when there is one: "Bash" says more than "working". */
  const liveWhat = $derived(
    live?.toolName
      ? live.toolName
      : live?.substate === "spawning"
        ? "starting up"
        : live?.substate
          ? live.substate
          : "thinking",
  );

  let el: HTMLDivElement | undefined = $state();
  let confirming = $state(false);
  let trendEl: ContextTrend | undefined = $state();

  $effect(() => {
    if (!open) {
      confirming = false;
      return;
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        // The chart takes Esc first: it clears a selection, closes its
        // options or its big view, and the panel stays open. Consumed
        // here, it must not also close the side panel or the drawer
        // behind — those listen on the window too, below this capture
        // listener. The big view's own listener sits on the window as
        // well, so stopPropagation (not Immediate) still lets it close.
        if (trendEl?.escape()) {
          e.preventDefault();
          e.stopPropagation();
          return;
        }
        e.preventDefault();
        e.stopPropagation();
        onClose();
      }
    }
    function onDown(e: MouseEvent) {
      const t = e.target as Node;
      // The big chart is mounted on <body>, outside this box, but it is
      // still part of the panel: a click in it is not a click outside.
      if (t instanceof Element && t.closest("[data-context-modal]")) return;
      if (el && !el.contains(t)) onClose();
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
    if (n < 1_000_000_000) return `${(n / 1_000_000).toFixed(2)}M`;
    return `${(n / 1_000_000_000).toFixed(2)}B`;
  }
  function exact(n: number): string {
    return Number.isFinite(n) ? n.toLocaleString() : "0";
  }
  function cost(usd: number): string {
    if (!Number.isFinite(usd) || usd <= 0) return "—";
    if (usd < 0.01) return "<$0.01";
    return `$${usd.toFixed(2)}`;
  }

  /* The level, preferring whichever source is further along. They are
     the same reading from the same place — the last request's level —
     so a difference only ever means one of them has not caught up, and
     during a long turn that is always the stored one. */
  const usedNow = $derived(Math.max(live?.used ?? 0, data?.used ?? 0));
  const hasWindow = $derived((data?.window ?? 0) > 0);
  /* True when the number on screen came from the running turn rather
     than from the ledger. Worth saying out loud: the spend figures below
     it are still the last finished turn's, and a panel where one number
     is live and the rest are not should say which is which. */
  const levelIsLive = $derived((live?.used ?? 0) > (data?.used ?? 0));
  /* The server computes the stored percentage, and stays the source of
     truth for it; recomputing only happens when the live reading is
     ahead of the stored one, which is the one case the server could not
     have known about. */
  const pct = $derived(
    levelIsLive && hasWindow
      ? Math.max(0, Math.min(100, (usedNow / (data?.window ?? 1)) * 100))
      : Math.max(0, Math.min(100, data?.pct ?? 0)),
  );
  /* Thresholds live in @wick-fe/common-ui, shared with the sub-agent
     inspector: two panels that disagreed about what "nearly full" means
     would be two different warnings about one number. */
  const tone = $derived(contextTone(pct));
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
  const cacheHit = $derived(Math.round(data?.totals.cache_hit_pct ?? 0));

  /* The headline row is the ACTIVE provider's: its turns and its spend.
     The session's own totals (every provider) are the Session total row,
     shown once a second provider exists. An older server without the
     row falls back to the session totals, which are then the same. */
  const activeRow = $derived(
    (data?.providers ?? []).find((p) => p.provider === data?.provider) ?? null,
  );
  const headTurns = $derived(activeRow?.turns ?? data?.turns ?? 0);
  const headTotals = $derived<UsageTotals | null>(activeRow?.totals ?? data?.totals ?? null);
  /* Fresh is what was not served from cache: input, cache writes and
     output. Cache read is split out because on a long session it is
     most of the traffic. */
  const fresh = (t: UsageTotals) => t.input + t.cache_write + t.output;
  const spentTokens = $derived(headTotals?.total ?? 0);

  const others = $derived<SessionContextProvider[]>(
    (data?.providers ?? []).filter((p) => p.provider !== data?.provider),
  );

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

    <!-- The running turn, above everything the ledger knows: those
         figures are written when a turn ENDS, so during a long one this
         is the only part of the panel that is about now. -->
    {#if live?.active}
      <div
        data-testid="context-live"
        class="flex items-center gap-2 border-b border-white-300 bg-white-200 px-4 py-2 dark:border-navy-600 dark:bg-navy-800"
      >
        <span class="relative inline-flex h-2 w-2 shrink-0">
          <span class="absolute inline-flex h-full w-full animate-ping rounded-full bg-green-500 opacity-60"></span>
          <span class="relative inline-flex h-2 w-2 rounded-full bg-green-500"></span>
        </span>
        <span class="min-w-0 flex-1 truncate text-xs text-black-900 dark:text-white-100" title={liveWhat}>
          {liveWhat}
        </span>
        {#if live.steps > 0}
          <span class="shrink-0 text-xs text-black-700 tabular-nums dark:text-black-600">
            {live.steps} step{live.steps === 1 ? "" : "s"}
          </span>
        {/if}
        <span
          data-testid="context-live-elapsed"
          class="shrink-0 font-mono text-xs text-black-900 tabular-nums dark:text-white-100"
          title="How long this turn has been running"
        >{clock(elapsedMs)}</span>
      </div>
    {/if}

    {#if usage && onOpenUsage}
      <!-- Account quota, one line. Details jumps to /usage (per account). -->
      <div
        data-testid="context-usage"
        class="flex items-center gap-2 border-b border-white-300 px-4 py-1.5 text-[11px] tabular-nums dark:border-navy-600"
      >
        <span class="shrink-0 text-black-700 dark:text-black-600">Usage</span>
        <span class="min-w-0 flex-1 truncate">
          {#if !usage.supported}
            <span class="text-black-600 dark:text-black-700">not reported by this provider</span>
          {:else if usage.pending || usage.checking}
            <span class="text-black-600 dark:text-black-700">checking…</span>
          {:else if usage.error && usageWindows.length === 0}
            <span class="text-black-600 dark:text-black-700">unavailable</span>
          {:else if usageWindows.length === 0}
            <span class="text-black-600 dark:text-black-700">no reading yet</span>
          {:else}
            {#each usageWindows as w, i (w.key)}
              {@const pct = Math.min(100, Math.max(0, Math.round(w.utilization)))}
              {#if i > 0}<span class="text-black-600 dark:text-black-700"> · </span>{/if}
              <span class="text-black-700 dark:text-black-600">{usageLabel(w.key)}</span>
              <span class="font-medium {usageTone(pct)}">{pct}%</span>
            {/each}
            {#if usageReset}<span class="text-black-600 dark:text-black-700"> · reset {usageReset}</span>{/if}
            {#if usage.accounts.length > 1}<span class="text-black-600 dark:text-black-700"> · {usage.accounts.length} accounts</span>{/if}
          {/if}
        </span>
        {#if usageRecheckWait > 0}
          <span data-testid="context-usage-wait" class="shrink-0 text-[10px] text-black-600 dark:text-black-700" title="A probe now would land inside a cooldown, so it was not sent">wait {usageRecheckWait}s</span>
        {/if}
        {#if usage.supported && onUsageRefresh}
          <button
            type="button"
            data-testid="context-usage-refresh"
            aria-label="Re-check usage"
            title="Re-check usage"
            class="shrink-0 rounded px-1 text-black-700 hover:bg-white-200 disabled:opacity-50 dark:text-black-600 dark:hover:bg-navy-800"
            disabled={usageRefreshing || usage.checking}
            onclick={onUsageRefresh}
          ><span class={usageRefreshing || usage.checking ? "inline-block animate-spin" : ""}>↻</span></button>
        {/if}
        <button
          type="button"
          data-testid="context-usage-open"
          class="shrink-0 rounded px-1.5 py-0.5 font-medium text-link-400 hover:bg-white-200 dark:hover:bg-navy-800"
          onclick={onOpenUsage}
        >Details →</button>
      </div>
    {/if}

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
            {#if hasWindow}{Math.round(pct)}%{:else}{short(usedNow)}{/if}
          </p>
          <p
            class="text-xs text-black-700 dark:text-black-600 tabular-nums"
            title="{exact(usedNow)} of {exact(data.window)} tokens"
          >
            {short(usedNow)}{#if hasWindow}
              / {short(data.window)}{/if} tokens
          </p>
        </div>

        {#if hasWindow}
          <div class="mt-2 h-2 rounded-full bg-white-300 dark:bg-navy-600 overflow-hidden">
            <div class="h-full rounded-full {barClass}" style="width: {pct}%"></div>
          </div>
        {/if}

        <p class="mt-2 text-xs text-black-700 dark:text-black-600">
          {advice}
          {#if levelIsLive}
            <span data-testid="context-level-live" class="text-green-600 dark:text-green-400"
              >· live, this turn</span
            >
          {/if}
        </p>

        {#if (data.trend?.length ?? 0) > 0}
          <ContextTrend bind:this={trendEl} {data} {loadSeries} />
        {/if}
      </div>

      <!-- Everything below is the SPEND, and the spend is only known when
           a turn ends: the vendor reports cost and per-model totals in the
           frame that closes the turn, never before it. So while one is
           running these read one turn behind, and say so rather than
           looking stale next to a level that is moving. -->
      {#if levelIsLive}
        <p
          data-testid="context-spend-lag"
          class="border-t border-white-300 px-4 pt-2 text-[11px] text-black-700 dark:border-navy-600 dark:text-black-600"
        >
          Spend below is up to the last finished turn — the provider only reports it when a turn ends.
        </p>
      {/if}
      <div
        class="ctx-grid px-4 py-2.5 border-t border-white-300 dark:border-navy-600 grid gap-2 text-xs"
        class:border-t-0={levelIsLive}
      >
        <div class="min-w-0">
          <p class="text-black-700 dark:text-black-600">Provider</p>
          <p class="font-medium text-black-900 dark:text-white-100 truncate" title={data.model}>
            {data.provider || "—"}
          </p>
        </div>
        <div class="min-w-0">
          <p class="text-black-700 dark:text-black-600">Turns</p>
          <p class="font-medium text-black-900 dark:text-white-100 tabular-nums">{headTurns}</p>
        </div>
        <div class="min-w-0">
          <p class="text-black-700 dark:text-black-600">Spent</p>
          <p class="text-sm font-bold text-black-900 dark:text-white-100 tabular-nums" data-testid="session-spent">
            {cost(headTotals?.cost_usd ?? 0)}
          </p>
        </div>
        <div class="min-w-0">
          <!-- Every token this provider sent and received, split into
               what was new and what was re-read from cache — not the
               fill level above. -->
          <p class="text-black-700 dark:text-black-600">Fresh</p>
          <p
            class="font-medium text-black-900 dark:text-white-100 tabular-nums"
            data-testid="session-tokens"
            title="{exact(spentTokens)} tokens in total — in {exact(headTotals?.input ?? 0)} · cache write {exact(
              headTotals?.cache_write ?? 0,
            )} · out {exact(headTotals?.output ?? 0)}"
          >
            {short(headTotals ? fresh(headTotals) : 0)}
          </p>
        </div>
        <div class="min-w-0">
          <p class="text-black-700 dark:text-black-600">Cache read</p>
          <p class="font-medium text-black-900 dark:text-white-100 tabular-nums" data-testid="session-cache-read">
            {short(headTotals?.cache_read ?? 0)}
          </p>
        </div>
      </div>

      {#if spentTokens > 0 && headTotals}
        <div
          class="px-4 pb-2.5 -mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-black-700 dark:text-black-600 tabular-nums"
        >
          <span title="Fresh input tokens — the part that missed the cache">in {short(headTotals.input)}</span>
          <span title="Input written to the prompt cache">cache write {short(headTotals.cache_write)}</span>
          <span title="Tokens the model generated, reasoning included">out {short(headTotals.output)}</span>
          {#if cacheHit > 0}<span>{cacheHit}% cache hit</span>{/if}
        </div>
      {/if}

      {#if others.length > 0}
        <div class="px-4 py-2 border-t border-white-300 dark:border-navy-600" data-testid="context-others">
          <p class="text-[11px] text-black-700 dark:text-black-600 mb-1">
            Also used in this session
          </p>
          {#each others as p (p.provider)}
            <div class="ctx-grid grid gap-2 py-0.5 text-xs tabular-nums">
              <span class="truncate text-black-900 dark:text-white-100">{p.provider}</span>
              <span class="text-black-700 dark:text-black-600">{p.turns}</span>
              <span class="font-medium text-black-900 dark:text-white-100">{cost(p.totals.cost_usd)}</span>
              <span class="text-black-700 dark:text-black-600">{short(fresh(p.totals))}</span>
              <span class="text-black-700 dark:text-black-600">{short(p.totals.cache_read)}</span>
            </div>
          {/each}
          <div
            class="ctx-grid grid gap-2 py-0.5 text-xs tabular-nums mt-1 border-t border-white-300 dark:border-navy-600 pt-1.5 font-medium"
            data-testid="context-session-total"
          >
            <span class="truncate text-black-900 dark:text-white-100">Session total</span>
            <span class="text-black-700 dark:text-black-600">{data.turns}</span>
            <span class="font-medium text-black-900 dark:text-white-100">{cost(data.totals.cost_usd)}</span>
            <span class="text-black-700 dark:text-black-600">{short(fresh(data.totals))}</span>
            <span class="text-black-700 dark:text-black-600">{short(data.totals.cache_read)}</span>
          </div>
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
          <!-- auto_compact is what the provider itself reported (omp
               get_state, opencode config); absent = it never said, so the
               general sentence stays. -->
          <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600 text-center" data-testid="auto-compact-note">
            {#if data.auto_compact === true}
              wick never presses this for you — auto-compact is on at {data.provider || "the provider"}, so
              it compacts on its own once the window fills.
            {:else if data.auto_compact === false}
              Auto-compact is off at {data.provider || "the provider"} — nothing compacts unless you press
              this.
            {:else}
              wick never presses this for you — but the provider compacts on its own once the window
              fills.
            {/if}
          </p>
        {/if}
      </div>
    {/if}
  </div>
{/if}

<style>
  .ctx-grid {
    grid-template-columns: 1.7fr 0.5fr 1fr 1fr 1fr;
  }
</style>
