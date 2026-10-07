<script lang="ts">
  /* Settings › Access › History: the agent's latest 20 access changes —
     who, when, a short diff, and how a Captain's proposal was decided.
     Folded by default; read when first opened. */
  import { untrack } from "svelte";
  import { getAccessHistory, runApi, type AccessHistoryItem } from "../api/team.js";
  import { historyLine, historyStatus } from "../captainSettings.js";
  import { timeAgo } from "../timeFormat.js";

  type Props = { base: string; agentId: string; initialOpen?: boolean; initialItems?: AccessHistoryItem[] };
  let { base, agentId, initialOpen = false, initialItems }: Props = $props();
  let items = $state<AccessHistoryItem[] | null>(untrack(() => initialItems ?? null));
  let error = $state("");
  let loading = $state(false);

  async function load() {
    if (loading || items) return;
    loading = true;
    error = "";
    try {
      items = (await runApi(getAccessHistory(base, agentId))).items ?? [];
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }
</script>

<details class="rounded-lg border border-white-300 dark:border-navy-600" open={initialOpen} data-testid="access-history" ontoggle={(e) => { if ((e.currentTarget as HTMLDetailsElement).open) load(); }}>
  <summary class="cursor-pointer select-none px-3 py-2 text-xs font-medium text-black-900 dark:text-white-100">History</summary>
  <div class="border-t border-white-300 px-3 py-2 dark:border-navy-600">
    {#if loading}
      <p class="text-xs text-black-800 dark:text-black-600">Loading…</p>
    {:else if error}
      <p class="text-xs text-neg-400">{error}</p>
    {:else if items && items.length === 0}
      <p class="text-xs text-black-800 dark:text-black-600">No access changes yet.</p>
    {:else if items}
      <ul class="space-y-1.5">
        {#each items as it (it.id)}
          <li class="text-xs" data-testid="access-history-row" data-status={it.status}>
            <span class="font-medium text-black-900 dark:text-white-100">{it.actor || "Someone"}</span>
            <span class="text-black-800 dark:text-black-600"> · {timeAgo(it.at)} · {historyStatus(it)}</span>
            <span class={"block break-words " + (it.status === "declined" ? "text-black-800 line-through dark:text-black-600" : "text-black-900 dark:text-white-100")}>{historyLine(it)}</span>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</details>
