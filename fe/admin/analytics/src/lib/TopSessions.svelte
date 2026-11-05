<!-- "Which conversations spent it": the ledger's per-session list under the
     page's range and filters, sortable by tokens, cost, turns or recency.
     Used for the whole page and inside a person's panel, with the scope
     narrowed to them. -->
<script lang="ts">
  import { compactTokens, fetchUsageSessions, formatCost } from "@wick-fe/common-ui";
  import type { LedgerScope, SessionSort, SessionsUsage } from "@wick-fe/common-ui";
  import { ago, channelClass, sessionHref } from "$lib/format";

  type Props = {
    endpoint: string;
    window: string;
    since?: string;
    until?: string;
    scope?: LedgerScope;
    title?: string;
    /** A person's own list already says whose conversations these are. */
    showUser?: boolean;
    onProject?: (id: string) => void;
  };
  let {
    endpoint,
    window,
    since = "",
    until = "",
    scope = {},
    title = "Conversations by usage",
    showUser = true,
    onProject,
  }: Props = $props();

  const sorts: { key: SessionSort; label: string }[] = [
    { key: "tokens", label: "Tokens" },
    { key: "cost", label: "Cost" },
    { key: "turns", label: "Turns" },
    { key: "recent", label: "Recent" },
  ];
  let sort = $state<SessionSort>("tokens");
  let limit = $state(20);
  let data = $state<SessionsUsage | null>(null);
  let error = $state("");
  let loading = $state(false);

  // One request per change of range, scope, sort or page size; a late
  // answer to an older question is dropped.
  let seq = 0;
  $effect(() => {
    const mine = ++seq;
    loading = true;
    fetchUsageSessions(endpoint, window, since, until, scope, sort, limit)
      .then((r) => {
        if (mine !== seq) return;
        data = r;
        error = "";
      })
      .catch((e: unknown) => {
        if (mine !== seq) return;
        error = e instanceof Error ? e.message : String(e);
      })
      .finally(() => {
        if (mine === seq) loading = false;
      });
  });
</script>

<div class="border-t border-white-300 px-5 py-3 dark:border-navy-600">
  <div class="mb-2 flex flex-wrap items-center gap-2">
    <h3 class="text-xs font-semibold text-black-900 dark:text-white-100">
      {title}
      {#if data}
        <span class="font-normal text-black-600 dark:text-black-700">
          top {data.sessions.length} of {data.total} · {compactTokens(data.totals.total)} tokens
        </span>
      {/if}
    </h3>
    <div class="ml-auto flex items-center gap-1" role="group" aria-label="Sort conversations">
      <span class="text-[10px] text-black-600 dark:text-black-700">Sort</span>
      {#each sorts as s}
        <button
          type="button"
          onclick={() => (sort = s.key)}
          aria-pressed={sort === s.key}
          class="rounded px-1.5 py-0.5 text-[10px] font-medium {sort === s.key
            ? 'bg-green-600 text-white-100 dark:bg-green-500 dark:text-navy-900'
            : 'bg-white-300 text-black-800 hover:text-green-700 dark:bg-navy-600 dark:text-black-600 dark:hover:text-green-300'}"
        >
          {s.label}
        </button>
      {/each}
    </div>
  </div>

  {#if error}
    <p class="text-xs text-red-600 dark:text-red-400">Could not load conversations: {error}</p>
  {:else if !data && loading}
    <p class="text-xs text-black-600 dark:text-black-700">Loading…</p>
  {:else if data}
    <div class="space-y-1 {loading ? 'opacity-60' : ''}">
      {#each data.sessions as s (s.id)}
        <div class="flex items-center gap-2 text-xs">
          {#if s.channel}
            <span class="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium {channelClass(s.channel)}">{s.channel}</span>
          {/if}
          <a href={sessionHref(s.id)} class="min-w-0 truncate text-black-900 hover:underline dark:text-white-100" title={s.id}>
            {s.label || s.id}
          </a>
          {#if s.project_name || s.project_id}
            {#if onProject && s.project_id}
              <button
                type="button"
                onclick={() => onProject?.(s.project_id ?? "")}
                title={s.project_id}
                class="max-w-[10rem] shrink-0 truncate rounded bg-white-300 px-1.5 py-0.5 text-[10px] text-black-800 hover:text-green-700 dark:bg-navy-600 dark:text-black-600 dark:hover:text-green-300"
              >
                {s.project_name || s.project_id}
              </button>
            {:else}
              <span class="max-w-[10rem] shrink-0 truncate rounded bg-white-300 px-1.5 py-0.5 text-[10px] text-black-800 dark:bg-navy-600 dark:text-black-600">
                {s.project_name || s.project_id}
              </span>
            {/if}
          {/if}
          {#each s.providers ?? [] as pv}
            <span class="max-w-[10rem] shrink-0 truncate rounded px-1.5 py-0.5 font-mono text-[10px] text-black-700 ring-1 ring-white-300 dark:text-black-600 dark:ring-navy-600">
              {pv}
            </span>
          {/each}
          <span class="ml-auto flex shrink-0 items-center gap-2 whitespace-nowrap text-black-600 dark:text-black-700">
            {#if showUser}<span class="max-w-[8rem] truncate">{s.user_name || "unattributed"}</span>{/if}
            <span class="w-14 text-right">{s.turns ?? 0} turns</span>
            <span
              class="inline-block h-1.5 w-12 overflow-hidden rounded bg-white-300 dark:bg-navy-600"
              title="{s.share.toFixed(1)}% of the tokens in this range"
            >
              <span class="block h-full bg-green-600 dark:bg-green-400" style="width: {Math.min(100, s.share)}%"></span>
            </span>
            <span class="w-14 text-right font-mono text-black-900 dark:text-white-100" title="{s.totals.total.toLocaleString()} tokens">
              {compactTokens(s.totals.total)}
            </span>
            {#if s.totals.cost_usd > 0}<span class="w-14 text-right font-mono">{formatCost(s.totals.cost_usd)}</span>{/if}
            <span class="w-16 text-right">{ago(s.last_at)}</span>
          </span>
        </div>
      {:else}
        <p class="text-xs text-black-600 dark:text-black-700">No token usage in this range.</p>
      {/each}
    </div>
    {#if data.total > data.sessions.length}
      <button
        type="button"
        onclick={() => (limit += 30)}
        class="mt-2 text-[11px] text-green-700 hover:underline dark:text-green-300"
      >
        Show more ({data.total - data.sessions.length} left)
      </button>
    {/if}
  {/if}
</div>
