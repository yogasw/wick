<script lang="ts">
  import type { ApprovedItem } from "../types/agents.js";

  type Props = {
    sessionApproved: ApprovedItem[];
    alwaysApproved: ApprovedItem[];
    onRevoke: (matchKey: string, scope: "session" | "always") => void;
  };

  let { sessionApproved, alwaysApproved, onRevoke }: Props = $props();

  const total = $derived(sessionApproved.length + alwaysApproved.length);
  const isEmpty = $derived(total === 0);

  function truncateKey(key: string): string {
    return key.slice(0, 12) + "…";
  }
</script>

<details
  class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm"
>
  <summary
    class="cursor-pointer px-5 py-3 text-sm font-medium text-black-900 dark:text-white-100 select-none flex items-center justify-between"
  >
    <span>Approved commands</span>
    <span class="text-xs text-black-700 dark:text-black-600 font-normal">{total}</span>
  </summary>
  <div class="border-t border-white-300 dark:border-navy-600 px-5 py-3 text-xs space-y-3">
    {#if isEmpty}
      <div class="text-black-700 dark:text-black-600 italic">
        No commands have been approved yet. The list updates after the next gate decision.
      </div>
    {:else}
      <ul class="space-y-2">
        {#each sessionApproved as item (item.match_key)}
          <li
            class="flex items-center justify-between gap-3 rounded-lg bg-white-200 dark:bg-navy-800 px-3 py-2"
          >
            <div class="flex items-center gap-2 min-w-0">
              <span
                class="inline-block rounded border border-green-500 dark:border-green-600 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-400"
              >session</span>
              <code
                class="font-mono text-xs text-black-900 dark:text-white-100 truncate"
                title={item.match_key}
              >{truncateKey(item.match_key)}</code>
            </div>
            <button
              type="button"
              class="shrink-0 rounded-md border border-red-300 dark:border-red-800 px-2 py-1 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors"
              onclick={() => onRevoke(item.match_key, "session")}
            >Revoke</button>
          </li>
        {/each}
        {#each alwaysApproved as item (item.match_key)}
          <li
            class="flex items-center justify-between gap-3 rounded-lg bg-white-200 dark:bg-navy-800 px-3 py-2"
          >
            <div class="flex items-center gap-2 min-w-0">
              <span
                class="inline-block rounded bg-green-500 px-2 py-0.5 text-xs font-medium text-white-100"
              >always</span>
              <code
                class="font-mono text-xs text-black-900 dark:text-white-100 truncate"
                title={item.match_key}
              >{truncateKey(item.match_key)}</code>
            </div>
            <button
              type="button"
              class="shrink-0 rounded-md border border-red-300 dark:border-red-800 px-2 py-1 text-xs font-medium text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors"
              onclick={() => onRevoke(item.match_key, "always")}
            >Revoke</button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</details>
