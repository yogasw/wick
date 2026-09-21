<script lang="ts">
  /* The token ledger panel: what the fleet spent, and who spent it.
   *
   * Three questions, one surface — "what did project A cost", "what is
   * this person costing", "where is provider A actually used". They are
   * the same numbers sliced three ways, so they live in one card with a
   * tab switch rather than three cards competing for the same space.
   *
   * Reading rules this layout follows:
   *   - Cost leads. It is the number people came for; tokens explain it.
   *   - Rows are pre-sorted by the server, so the most expensive row is
   *     always first and the eye never has to scan for it.
   *   - The bar is share-of-total, not a bar per metric. One visual
   *     comparison per row, or the row stops being scannable.
   *   - Nothing auto-refreshes. The ledger is history, not a live feed;
   *     a table that reshuffles while you read it is hostile. */
  import { onMount } from "svelte";
  import {
    fetchUsageReport,
    fetchProviderUsage,
    compactTokens,
    formatCost,
    exact,
    sinceText,
    EMPTY_TOTALS,
    type UsageReport,
    type UsageSlice,
    type SessionUse,
    type WindowOption,
    type ProviderUsageDetail,
  } from "./usageReport.js";

  type Props = {
    /** Where the ledger is served from. The providers page passes its
        tool base; the admin analytics page passes its own mount. */
    base: string;
    /** Scope the whole panel to ONE provider ("claude/enginer"). Same
        component, filtered — the provider page asks a narrower question
        ("what did THIS cost, and where is it used") but it is the same
        ledger, and two components would drift apart. */
    provider?: string;
    /** Resolve an id to something human — project and user slices key by
     *  id, and a bare uuid tells nobody anything. Falls back to the id. */
    labelFor?: (kind: "project" | "user", key: string) => string;
    /** Override the collection the two reads hang off. Defaults to the
        agents tool's shape; the admin page serves the same report from
        its own path. */
    endpoint?: string;
    /** Section heading. The providers page says "Token Usage"; the admin
        page already has a heading above it. */
    title?: string;
    /** Which range to open on. "today" where a fast first paint matters
        more than the lifetime total — all time walks every session. */
    defaultRange?: string;
    /** Drive the range from OUTSIDE instead of from these chips. A page
        that already has a range filter must not grow a second one: two
        controls over one number is how a reader ends up comparing a
        30-day chart with a 7-day figure and trusting the comparison. */
    range?: string;
    since?: string;
    until?: string;
    /** Narrow to certain channels / bots, same filter the page applies
        to everything else below it. */
    channels?: string[];
    instances?: string[];
    /** Already-fetched report. A page that needs these numbers for its
        own drill-downs fetches once and hands the result here, instead
        of the card asking for the same thing a second time. */
    report?: UsageReport | null;
  };
  let {
    base,
    provider,
    labelFor,
    endpoint,
    title = "Token Usage",
    defaultRange = "today",
    range: rangeProp,
    since = "",
    until = "",
    channels,
    instances,
    report: reportProp,
  }: Props = $props();
  /** Fed from outside: render what the page already has. */
  const fed = $derived(reportProp !== undefined);
  /** Controlled when the caller passes a range or explicit dates. */
  const controlled = $derived(rangeProp !== undefined || since !== "" || until !== "");
  const api = $derived(endpoint ?? `${base}/api/providers`);

  type Tab = "provider" | "project" | "user";
  let tab = $state<Tab>("provider");
  let ownReport = $state<UsageReport | null>(null);
  const report = $derived(fed ? (reportProp ?? null) : ownReport);
  let detail = $state<ProviderUsageDetail | null>(null);
  /* The range being read. "What did this cost" is almost never a
     question about all time — it is "today", "this week" — and a figure
     that only ever grows cannot show that anything changed. All time
     stays the default because it is the only exact one. */
  let ownRange = $state(defaultRange);
  const range = $derived(controlled ? (rangeProp ?? "custom") : ownRange);
  /* Offered by the server with every answer, so the chips and the
     windows the backend understands cannot drift apart. */
  const ranges = $derived<WindowOption[]>(
    (provider ? detail?.windows : report?.windows) ?? [{ key: "all", label: "All time" }],
  );
  /* Pagination is not decoration here: a provider used by a busy host
     lists thousands of sessions, and a page that renders all of them is
     a page nobody scrolls to the bottom of. */
  const PAGE = 10;
  let page = $state(0);
  let loading = $state(true);
  let refreshing = $state(false);
  let error = $state("");

  async function load(refresh = false) {
    // Fed from outside and not scoped to a provider: nothing to fetch.
    if (fed && !provider) {
      loading = false;
      return;
    }
    if (refresh) refreshing = true;
    error = "";
    try {
      if (provider) {
        detail = await fetchProviderUsage(api, provider, refresh, range, since, until, { channels, instances });
        page = 0;
      } else {
        ownReport = await fetchUsageReport(api, refresh, range, since, until, { channels, instances });
      }
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
      refreshing = false;
    }
  }
  onMount(() => load(false));

  /* Follow the page's filter. Guarded on first run so this does not fire
     a second request on mount beside onMount's. */
  let lastRange = $state("");
  $effect(() => {
    const key = `${range}|${since}|${until}|${(channels ?? []).join()}|${(instances ?? []).join()}|${fed ? "fed" : ""}`;
    if (lastRange === "") {
      lastRange = key;
      return;
    }
    if (key === lastRange) return;
    lastRange = key;
    loading = true;
    void load(false);
  });

  /* Switching range is a new question, not a refresh: the cached answer
     for the old one is no longer what is on screen. */
  function pickRange(key: string) {
    if (key === range) return;
    ownRange = key;
    loading = true;
    void load(false);
  }

  const totals = $derived(provider ? (detail?.totals ?? EMPTY_TOTALS) : (report?.totals ?? EMPTY_TOTALS));
  const sessions = $derived<SessionUse[]>(detail?.sessions ?? []);
  /* The caveat the server attaches when a windowed figure had to be
     rebuilt from a trail that no longer reaches back far enough. Shown
     verbatim: an "at least" number presented as a total is the one way
     this panel can mislead. */
  const partialNote = $derived((provider ? detail : report)?.partial ? ((provider ? detail : report)?.note ?? "") : "");
  const pageCount = $derived(Math.max(1, Math.ceil(sessions.length / PAGE)));
  const pageRows = $derived(sessions.slice(page * PAGE, page * PAGE + PAGE));
  const rows = $derived<UsageSlice[]>(
    tab === "provider"
      ? (report?.by_provider ?? [])
      : tab === "project"
        ? (report?.by_project ?? [])
        : (report?.by_user ?? []),
  );

  /* A UUID answers nothing to the person asking "where did the money
     go", so a row leads with its name — the project title, the person's
     name — and keeps the id only as the small print that identifies it.
     A row with no name left (deleted project, removed user) still shows,
     shortened: the spend happened, and hiding it would make the totals
     not add up. */
  function rowLabel(r: UsageSlice): string {
    if (r.label) return r.label;
    if (tab === "provider") return r.key;
    return labelFor?.(tab, r.key) ?? shortId(r.key);
  }

  function shortId(key: string): string {
    return key.length > 12 ? key.slice(0, 8) : key;
  }

  /* The id line under a named row. Nothing to add when the row IS its
     id — that would print the same string twice. */
  function rowSubLabel(r: UsageSlice): string {
    if (tab === "provider") return "";
    return rowLabel(r) === shortId(r.key) || rowLabel(r) === r.key ? "" : shortId(r.key);
  }

  /* The empty state differs by tab, and saying why is the whole point:
     "no data" makes people check for a bug, while "sessions here carry
     no project" tells them the tagging is what is missing. */
  const emptyNote = $derived(
    tab === "provider"
      ? "No provider has reported tokens yet. Providers report on the turn they finish."
      : tab === "project"
        ? "No usage is attributed to a project yet — sessions started outside a project have nothing to group by."
        : "No usage is attributed to a user yet — sessions with no owner recorded have nothing to group by.",
  );

  const TABS: { id: Tab; label: string }[] = [
    { id: "provider", label: "By provider" },
    { id: "project", label: "By project" },
    { id: "user", label: "By user" },
  ];
</script>

<div
  class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden"
>
  <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 space-y-2">
    <div class="flex items-center justify-between gap-3">
      <div class="flex items-baseline gap-2 min-w-0">
        <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">{title}</h2>
        {#if provider}
          <span class="text-xs text-black-700 dark:text-black-600 truncate">
            {provider} · {exact(sessions.length)} session{sessions.length === 1 ? "" : "s"}
          </span>
        {:else if report}
          <span class="text-xs text-black-700 dark:text-black-600 truncate">
            {exact(report.turns)} turns · {exact(report.sessions)} sessions
          </span>
        {/if}
      </div>
      <button
        type="button"
        class="rounded-lg border border-white-300 dark:border-navy-600 px-2.5 py-1 text-xs font-medium text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
        onclick={() => load(true)}
        disabled={refreshing || loading}
      >
        {refreshing ? "Refreshing…" : "Refresh"}
      </button>
    </div>

    <!-- The range switch. Narrowest first: the narrow ranges are the
         ones people check repeatedly, all time is the one they check
         once. Hidden when a page drives the range itself. -->
    {#if !controlled}
    <div class="flex items-center gap-1 flex-wrap" role="group" aria-label="Range">
      {#each ranges as r (r.key)}
        <button
          type="button"
          aria-pressed={range === r.key}
          data-testid="usage-range-{r.key}"
          class="rounded-full px-2.5 py-0.5 text-[11px] font-medium border transition-colors {range === r.key
            ? 'border-blue-500 bg-blue-50 text-blue-700 dark:bg-blue-900 dark:text-blue-200'
            : 'border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800'}"
          onclick={() => pickRange(r.key)}
          disabled={loading || refreshing}
        >
          {r.label}
        </button>
      {/each}
    </div>
    {/if}
  </div>

  {#if partialNote}
    <p
      data-testid="usage-partial-note"
      class="px-5 py-2 text-[11px] text-amber-700 dark:text-amber-400 border-b border-white-300 dark:border-navy-600"
    >
      {partialNote}
    </p>
  {/if}

  {#if loading}
    <p class="px-5 py-6 text-xs text-black-700 dark:text-black-600">Reading the ledger…</p>
  {:else if error}
    <p class="px-5 py-6 text-xs text-red-600 dark:text-red-400">Couldn't load usage: {error}</p>
  {:else if provider}
    {@const t = totals}
    <!-- Provider mode: the same headline figures, then WHERE it is used.
         "Per turn" makes no sense here (the slice has no turn count of
         its own), so that tile becomes the session count. -->
    <div class="grid grid-cols-2 md:grid-cols-4 gap-px bg-white-300 dark:bg-navy-600">
      <div class="bg-white-100 dark:bg-navy-700 px-5 py-4">
        <p class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">Cost</p>
        <p class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100">{formatCost(t.cost_usd)}</p>
        <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">as reported by this provider</p>
      </div>
      <div class="bg-white-100 dark:bg-navy-700 px-5 py-4">
        <p class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">Tokens</p>
        <p class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100" title={exact(t.total)}>
          {compactTokens(t.total)}
        </p>
        <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">
          in {compactTokens(t.input + t.cache_read + t.cache_write)} · out {compactTokens(t.output)}
        </p>
      </div>
      <div class="bg-white-100 dark:bg-navy-700 px-5 py-4">
        <p class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">Cache hit</p>
        <p class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100">{t.cache_hit_pct.toFixed(0)}%</p>
        <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">
          {compactTokens(t.cache_read)} read from cache
        </p>
      </div>
      <div class="bg-white-100 dark:bg-navy-700 px-5 py-4">
        <p class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">Sessions</p>
        <p class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100 tabular-nums">
          {exact(sessions.length)}
        </p>
        <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">used this provider</p>
      </div>
    </div>

    {#if sessions.length === 0}
      <p class="px-5 py-6 text-xs text-black-700 dark:text-black-600">
        {range === "all"
          ? "No session has spent tokens on this provider yet."
          : `No session spent tokens on this provider in this range (${detail?.window_label ?? ""}).`}
      </p>
    {:else}
      <div class="px-5 py-2 border-t border-white-300 dark:border-navy-600 flex items-center justify-between gap-2">
        <p class="text-xs font-medium text-black-900 dark:text-white-100">Used in</p>
        {#if pageCount > 1}
          <!-- Pagination, because this list is unbounded: one provider on
               a busy host is thousands of sessions. -->
          <div class="flex items-center gap-1 text-xs">
            <button
              type="button"
              class="rounded px-2 py-0.5 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-40"
              onclick={() => (page = Math.max(0, page - 1))}
              disabled={page === 0}>←</button
            >
            <span class="tabular-nums text-black-700 dark:text-black-600">{page + 1} / {pageCount}</span>
            <button
              type="button"
              class="rounded px-2 py-0.5 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-40"
              onclick={() => (page = Math.min(pageCount - 1, page + 1))}
              disabled={page >= pageCount - 1}>→</button
            >
          </div>
        {/if}
      </div>
      <!-- One row per session: whose conversation it was, under which
           project, and what it spent here. The id alone answered "there
           are 6 of them" and nothing anybody could act on. -->
      <table class="w-full text-xs" data-testid="usage-sessions">
        <thead>
          <tr class="border-t border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600">
            <th class="px-5 py-2 text-left font-medium">Session</th>
            <th class="px-5 py-2 text-left font-medium hidden sm:table-cell">User</th>
            <th class="px-5 py-2 text-left font-medium hidden md:table-cell">Project</th>
            <th class="px-5 py-2 text-right font-medium">Cost</th>
            <th class="px-5 py-2 text-right font-medium hidden sm:table-cell">Tokens</th>
            <th class="px-5 py-2 text-right font-medium hidden md:table-cell">Last used</th>
          </tr>
        </thead>
        <tbody>
          {#each pageRows as row (row.id)}
            <tr class="border-t border-white-300 dark:border-navy-600 hover:bg-white-200 dark:hover:bg-navy-800">
              <td class="px-5 py-1.5 text-black-900 dark:text-white-100 max-w-0">
                <div class="truncate" title={row.id}>
                  {#if row.label}
                    <span class="font-medium">{row.label}</span>
                    <span class="ml-1.5 font-mono text-[11px] text-black-700 dark:text-black-600">
                      {row.id.slice(0, 8)}
                    </span>
                  {:else}
                    <span class="font-mono">{row.id.slice(0, 8)}</span>
                  {/if}
                </div>
              </td>
              <td class="px-5 py-1.5 text-black-700 dark:text-black-600 hidden sm:table-cell max-w-0">
                <div class="truncate" title={row.user_id ?? ""}>{row.user_name || (row.user_id ? row.user_id.slice(0, 8) : "—")}</div>
              </td>
              <td class="px-5 py-1.5 text-black-700 dark:text-black-600 hidden md:table-cell max-w-0">
                <div class="truncate" title={row.project_id ?? ""}>
                  {row.project_name || (row.project_id ? row.project_id.slice(0, 8) : "—")}
                </div>
              </td>
              <td class="px-5 py-1.5 text-right tabular-nums text-black-900 dark:text-white-100">
                {formatCost(row.totals.cost_usd)}
              </td>
              <td
                class="px-5 py-1.5 text-right tabular-nums text-black-700 dark:text-black-600 hidden sm:table-cell"
                title={exact(row.totals.total)}
              >
                {compactTokens(row.totals.total)}
              </td>
              <td class="px-5 py-1.5 text-right text-black-700 dark:text-black-600 hidden md:table-cell whitespace-nowrap">
                {sinceText(row.last_at)}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  {:else if report}
    <!-- Headline figures. Cost first: it is the question, the rest is
         the explanation. -->
    <div class="grid grid-cols-2 md:grid-cols-4 gap-px bg-white-300 dark:bg-navy-600">
      <div class="bg-white-100 dark:bg-navy-700 px-5 py-4">
        <p
          class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide"
        >
          Cost
        </p>
        <p class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100">
          {formatCost(totals.cost_usd)}
        </p>
        <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">
          as reported by providers
        </p>
      </div>
      <div class="bg-white-100 dark:bg-navy-700 px-5 py-4">
        <p
          class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide"
        >
          Tokens
        </p>
        <p
          class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100"
          title={exact(totals.total)}
        >
          {compactTokens(totals.total)}
        </p>
        <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">
          in {compactTokens(totals.input + totals.cache_read + totals.cache_write)} · out {compactTokens(
            totals.output,
          )}
        </p>
      </div>
      <div class="bg-white-100 dark:bg-navy-700 px-5 py-4">
        <p
          class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide"
        >
          Cache hit
        </p>
        <p class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100">
          {totals.cache_hit_pct.toFixed(0)}%
        </p>
        <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">
          {compactTokens(totals.cache_read)} read from cache
        </p>
      </div>
      <div class="bg-white-100 dark:bg-navy-700 px-5 py-4">
        <p
          class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide"
        >
          Per turn
        </p>
        <p class="mt-1 text-2xl font-bold text-black-900 dark:text-white-100">
          {report.turns > 0 ? compactTokens(Math.round(totals.total / report.turns)) : "—"}
        </p>
        <p class="mt-0.5 text-[11px] text-black-700 dark:text-black-600">tokens per turn</p>
      </div>
    </div>

    <div
      class="px-5 pt-3 flex items-center gap-1 border-t border-white-300 dark:border-navy-600"
    >
      {#each TABS as t (t.id)}
        <button
          type="button"
          class="rounded-t-lg px-3 py-1.5 text-xs font-medium -mb-px border-b-2 {tab === t.id
            ? 'border-blue-500 text-black-900 dark:text-white-100'
            : 'border-transparent text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100'}"
          onclick={() => (tab = t.id)}
        >
          {t.label}
        </button>
      {/each}
    </div>

    {#if rows.length === 0}
      <p class="px-5 py-6 text-xs text-black-700 dark:text-black-600">{emptyNote}</p>
    {:else}
      <table class="w-full text-xs">
        <thead>
          <tr
            class="border-b border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600"
          >
            <th class="px-5 py-2 text-left font-medium">
              {tab === "provider" ? "Provider" : tab === "project" ? "Project" : "User"}
            </th>
            <th class="px-5 py-2 text-right font-medium">Cost</th>
            <th class="px-5 py-2 text-right font-medium">Tokens</th>
            <th class="px-5 py-2 text-right font-medium hidden sm:table-cell">Cache</th>
            <th class="px-5 py-2 text-left font-medium w-32">Share</th>
          </tr>
        </thead>
        <tbody>
          {#each rows as r (r.key)}
            <tr
              class="border-b border-white-300 dark:border-navy-600 last:border-0 hover:bg-white-200 dark:hover:bg-navy-800"
            >
              <td class="px-5 py-2 text-black-900 dark:text-white-100">
                <span class="font-medium" title={r.key}>{rowLabel(r)}</span>
                {#if rowSubLabel(r)}
                  <span
                    class="ml-1.5 font-mono text-[11px] text-black-700 dark:text-black-600"
                    title={r.key}>{rowSubLabel(r)}</span
                  >
                {/if}
                {#if r.sessions}
                  <span class="ml-1.5 text-black-700 dark:text-black-600"
                    >· {r.sessions} session{r.sessions === 1 ? "" : "s"}</span
                  >
                {/if}
              </td>
              <td class="px-5 py-2 text-right tabular-nums text-black-900 dark:text-white-100">
                {formatCost(r.totals.cost_usd)}
              </td>
              <td
                class="px-5 py-2 text-right tabular-nums text-black-900 dark:text-white-100"
                title={exact(r.totals.total)}
              >
                {compactTokens(r.totals.total)}
              </td>
              <td
                class="px-5 py-2 text-right tabular-nums text-black-700 dark:text-black-600 hidden sm:table-cell"
              >
                {r.totals.cache_hit_pct.toFixed(0)}%
              </td>
              <td class="px-5 py-2">
                <div
                  class="flex items-center gap-2"
                  title="{r.share.toFixed(1)}% of all tokens"
                >
                  <div
                    class="h-1.5 flex-1 rounded-full bg-white-300 dark:bg-navy-600 overflow-hidden"
                  >
                    <div
                      class="h-full rounded-full bg-blue-500"
                      style="width: {Math.max(2, Math.min(100, r.share))}%"
                    ></div>
                  </div>
                  <span class="tabular-nums text-black-700 dark:text-black-600 w-9 text-right"
                    >{r.share.toFixed(0)}%</span
                  >
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}

    <!-- Says out loud what the numbers are NOT, because the two easy
         misreadings both end in someone quoting a wrong figure. -->
    <p
      class="px-5 py-2.5 text-[11px] text-black-700 dark:text-black-600 border-t border-white-300 dark:border-navy-600"
    >
      Cost is what each provider reported; providers that report none show as —. Rows only
      count sessions that recorded usage.
    </p>
  {/if}
</div>
