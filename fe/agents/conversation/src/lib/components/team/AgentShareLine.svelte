<script lang="ts">
  import type { AgentItem } from "../../api/team.js";
  import { shareInfo } from "../../agentSharing.js";

  /* The chat header's sharing part, told once here instead of on every
     roster row: "Shared by <initials> <owner>" for the person it was
     shared with, "Shared with N" for the owner (opens the Sharing
     settings). Nothing when the agent is not shared. */
  let { agent, onOpenSharing }: { agent: Pick<AgentItem, "role" | "shared_by" | "share_count">; onOpenSharing?: () => void } = $props();

  const info = $derived(shareInfo(agent));
  const shareIcon = "M8.6 13.5l6.8 4M15.4 6.5l-6.8 4";
</script>

{#snippet icon()}
  <svg class="h-3 w-3 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" aria-hidden="true"><circle cx="18" cy="5" r="3" /><circle cx="6" cy="12" r="3" /><circle cx="18" cy="19" r="3" /><path d={shareIcon} /></svg>
{/snippet}

{#if info?.kind === "shared-by"}
  <span class="inline-flex min-w-0 items-center gap-1 text-black-900 dark:text-white-100" data-testid="header-shared-by">
    <span class="inline-flex text-black-800 dark:text-black-600">{@render icon()}</span>Shared by
    <span class="inline-flex h-[15px] w-[15px] shrink-0 items-center justify-center rounded-full bg-[#6c8cf5] text-[8.5px] font-bold text-white-100" aria-hidden="true">{info.initials}</span>
    <span class="truncate">{info.owner}</span>
  </span>
{:else if info?.kind === "shared-with"}
  <button
    type="button"
    class="inline-flex items-center gap-1 rounded text-green-600 hover:underline dark:text-green-400"
    data-testid="header-shared-with"
    aria-label="Shared with {info.count} {info.count === 1 ? 'person' : 'people'}: open sharing settings"
    onclick={() => onOpenSharing?.()}
  >{@render icon()}Shared with {info.count}</button>
{/if}
