<!-- The "Recent conversations" block, shared by the project panel and the
     person panel. One component so the two lists cannot drift into
     describing the same conversation two different ways. -->
<script lang="ts">
  import type { AnalyticsSessionRef } from "$lib/types";
  import { ago, channelClass } from "$lib/format";

  type Props = {
    items: AnalyticsSessionRef[];
    /** How many there are in total — the list itself is only the newest few. */
    total: number;
    /** A person's own list already says whose conversations these are. */
    showUser?: boolean;
    /** A project's own list already says which project they landed in. */
    showProject?: boolean;
    onProject?: (id: string) => void;
  };
  let { items, total, showUser = true, showProject = false, onProject }: Props = $props();
</script>

<div class="border-t border-white-300 px-5 py-3 dark:border-navy-600">
  <h3 class="mb-2 text-xs font-semibold text-black-900 dark:text-white-100">
    Recent conversations
    <span class="font-normal text-black-600 dark:text-black-700">newest {items.length} of {total}</span>
  </h3>
  <div class="space-y-1">
    {#each items as s}
      <div class="flex items-baseline gap-2 text-xs">
        <span class="rounded px-1.5 py-0.5 text-[10px] font-medium {channelClass(s.channel)}">{s.channel}</span>
        <a href={`/agents/conversation?session=${encodeURIComponent(s.id)}`} class="truncate text-black-900 hover:underline dark:text-white-100">
          {s.label || s.id}
        </a>
        {#if s.token}
          <span class="rounded bg-amber-100 px-1.5 py-0.5 text-[10px] text-amber-700 dark:bg-amber-900/40 dark:text-amber-300" title="created with this token">
            {s.token}
          </span>
        {/if}
        {#if showProject && s.project}
          {#if onProject && s.project_id}
            <button
              type="button"
              onclick={() => onProject?.(s.project_id ?? "")}
              title={s.project_id}
              class="max-w-[10rem] shrink-0 truncate rounded bg-white-300 px-1.5 py-0.5 text-[10px] text-black-800 hover:text-green-700 dark:bg-navy-600 dark:text-black-600 dark:hover:text-green-300"
            >
              {s.project}
            </button>
          {:else}
            <span class="max-w-[10rem] shrink-0 truncate rounded bg-white-300 px-1.5 py-0.5 text-[10px] text-black-800 dark:bg-navy-600 dark:text-black-600">
              {s.project}
            </span>
          {/if}
        {/if}
        {#each s.providers ?? [] as pv}
          <span
            class="max-w-[10rem] shrink-0 truncate rounded px-1.5 py-0.5 font-mono text-[10px] text-black-700 ring-1 ring-white-300 dark:text-black-600 dark:ring-navy-600"
            title="ran on {pv}"
          >
            {pv}
          </span>
        {/each}
        <span class="ml-auto whitespace-nowrap text-black-600 dark:text-black-700">
          {#if showUser}{s.user || "unattributed"} · {/if}{ago(s.last_active_at)}
        </span>
      </div>
    {:else}
      <p class="text-xs text-black-600 dark:text-black-700">No conversations in this range.</p>
    {/each}
  </div>
</div>
