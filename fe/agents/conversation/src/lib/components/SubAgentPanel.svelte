<script lang="ts">
  import type { SubAgentItem } from "../types/agents.js";
  import {
    subAgentStatusCls,
    subAgentStatusLabel,
    isSubAgentLive,
  } from "../lifecycleCls.js";

  type Props = {
    subAgents: SubAgentItem[];
    selectedId: string | null;
    onSelect: (childSessionId: string) => void;
    onInterrupt: (delegationId: string) => void;
    onInterruptAll: () => void;
  };

  let { subAgents, selectedId, onSelect, onInterrupt, onInterruptAll }: Props = $props();

  const anyLive = $derived(subAgents.some((s) => isSubAgentLive(s.status)));

  // Depth indent: 14px per level, matching the design doc. Capped so a
  // deep chain cannot push a row's text off a narrow rail.
  function indentStyle(depth: number): string {
    return `margin-left:${Math.min(depth, 3) * 14}px`;
  }

  function turnsLabel(s: SubAgentItem): string {
    return s.max_turns > 0 ? `${s.turns_used}/${s.max_turns} turns` : `${s.turns_used} turns`;
  }
</script>

<div class="flex-1 overflow-y-auto">
  <div
    class="flex items-center justify-between gap-2 border-b border-white-300 dark:border-navy-600 px-4 py-3"
  >
    <span class="text-xs font-semibold text-black-900 dark:text-white-100">Sub-agents</span>
    {#if anyLive}
      <button
        type="button"
        onclick={onInterruptAll}
        class="shrink-0 rounded px-2 py-1 text-[10px] font-medium bg-neg-100 text-neg-400 hover:bg-neg-200 transition-colors"
      >Stop all</button>
    {/if}
  </div>

  <div class="p-4 space-y-3">
    {#if subAgents.length === 0}
      <p class="text-xs text-black-700 dark:text-black-600 py-4 px-2">
        No sub-agents for this session.
      </p>
    {:else}
      {#each subAgents as sub (sub.delegation_id)}
        <div style={indentStyle(sub.depth)}>
          <div
            class={"rounded-xl border p-3 space-y-2 transition-colors " +
              (selectedId === sub.child_session_id
                ? "border-green-500 bg-white-200 dark:bg-navy-800"
                : "border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800")}
          >
            <div class="flex items-center justify-between gap-2">
              <button
                type="button"
                onclick={() => onSelect(sub.child_session_id)}
                class="flex items-center gap-2 min-w-0 text-left"
              >
                <span class="text-xs font-semibold text-black-900 dark:text-white-100 truncate"
                  >{sub.profile_key}</span
                >
                <span class={"rounded px-1.5 py-0.5 text-[10px] font-medium shrink-0 " + subAgentStatusCls(sub.status)}
                  >{subAgentStatusLabel(sub.status)}</span
                >
              </button>
              <!--
                Stop is offered for queued rows too, not just running ones.
                A queued sub-agent is cancelled by dropping it from the
                queue; hiding the button there leaves work the user cannot
                call off before it starts.
              -->
              {#if isSubAgentLive(sub.status)}
                <button
                  type="button"
                  onclick={() => onInterrupt(sub.delegation_id)}
                  class="shrink-0 rounded px-2 py-1 text-[10px] font-medium bg-neg-100 text-neg-400 hover:bg-neg-200 transition-colors"
                >Stop</button>
              {/if}
            </div>

            <button
              type="button"
              onclick={() => onSelect(sub.child_session_id)}
              class="block w-full text-left text-[11px] text-black-800 dark:text-black-600 line-clamp-2"
            >{sub.label}</button>

            <div class="flex items-center gap-2 text-[10px] text-black-700 dark:text-black-600">
              <span>{turnsLabel(sub)}</span>
              {#if sub.depth > 0}
                <span>· depth {sub.depth}</span>
              {/if}
            </div>

            {#if sub.result}
              <p class="text-[11px] text-black-800 dark:text-black-600 line-clamp-3 border-l-2 border-white-400 dark:border-navy-500 pl-2">
                {sub.result}
              </p>
            {/if}
          </div>
        </div>
      {/each}
    {/if}
  </div>
</div>
