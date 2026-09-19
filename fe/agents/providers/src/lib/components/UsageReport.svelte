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
    compactTokens,
    formatCost,
    exact,
    EMPTY_TOTALS,
    type UsageReport,
    type UsageSlice,
  } from "../usageReport.js";

  type Props = {
    base: string;
    /** Resolve an id to something human — project and user slices key by
     *  id, and a bare uuid tells nobody anything. Falls back to the id. */
    labelFor?: (kind: "project" | "user", key: string) => string;
  };
  let { base, labelFor }: Props = $props();

  type Tab = "provider" | "project" | "user";
  let tab = $state<Tab>("provider");
  let report = $state<UsageReport | null>(null);
  let loading = $state(true);
  let refreshing = $state(false);
  let error = $state("");

  async function load(refresh = false) {
    if (refresh) refreshing = true;
    error = "";
    try {
      report = await fetchUsageReport(base, refresh);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
      refreshing = false;
    }
  }
  onMount(() => load(false));

  const totals = $derived(report?.totals ?? EMPTY_TOTALS);
  const rows = $derived<UsageSlice[]>(
    tab === "provider"
      ? (report?.by_provider ?? [])
      : tab === "project"
        ? (report?.by_project ?? [])
        : (report?.by_user ?? []),
  );

  function rowLabel(r: UsageSlice): string {
    if (r.label) return r.label;
    if (tab === "provider") return r.key;
    return labelFor?.(tab, r.key) ?? r.key;
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
  <div
    class="px-5 py-3 flex items-center justify-between gap-3 border-b border-white-300 dark:border-navy-600"
  >
    <div class="flex items-baseline gap-2 min-w-0">
      <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Token Usage</h2>
      {#if report}
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

  {#if loading}
    <p class="px-5 py-6 text-xs text-black-700 dark:text-black-600">Reading the ledger…</p>
  {:else if error}
    <p class="px-5 py-6 text-xs text-red-600 dark:text-red-400">Couldn't load usage: {error}</p>
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
                <span class="font-medium">{rowLabel(r)}</span>
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
