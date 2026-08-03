<script lang="ts">
  import type { AgentMessageItem, SubAgentItem } from "../types/agents.js";
  import MessageThread from "./MessageThread.svelte";
  import {
    subAgentStatusCls,
    subAgentStatusLabel,
    isSubAgentLive,
    isSubAgentWorking,
  } from "../lifecycleCls.js";
  import { timeAgo, exactTime, shortDuration, parseEventTime } from "../timeFormat.js";
  import { now } from "../stores/now.js";

  type Props = {
    subAgents: SubAgentItem[];
    selectedId: string | null;
    onSelect: (childSessionId: string) => void;
    onInterrupt: (delegationId: string) => void;
    onInterruptAll: () => void;
    messages: AgentMessageItem[];
    hopsLeft: number;
    onBumpHops: () => void;
  };

  let {
    subAgents,
    selectedId,
    onSelect,
    onInterrupt,
    onInterruptAll,
    messages,
    hopsLeft,
    onBumpHops,
  }: Props = $props();

  const anyLive = $derived(subAgents.some((s) => isSubAgentLive(s.status)));

  // Depth indent: 14px per level, matching the design doc. Capped so a
  // deep chain cannot push a row's text off a narrow rail.
  function indentStyle(depth: number): string {
    return `margin-left:${Math.min(depth, 3) * 14}px`;
  }

  function turnsLabel(s: SubAgentItem): string {
    return s.max_turns > 0 ? `${s.turns_used}/${s.max_turns} turns` : `${s.turns_used} turns`;
  }

  /* When it happened, phrased by what it is doing.

     A finished row is dated by when it FINISHED — that is what you want
     to know when scanning results — while a live one is dated by when it
     started, because the only interesting question about a running
     sub-agent is how long it has been at it. */
  function stamp(s: SubAgentItem): { text: string; exact: string } {
    const live = isSubAgentLive(s.status);
    const raw = live ? s.started_at : (s.ended_at ?? s.started_at);
    const exact = exactTime(raw);
    if (!exact) return { text: "", exact: "" };
    if (live) {
      const d = shortDuration($now - (parseEventTime(raw) ?? $now));
      return { text: d === "just now" ? "just started" : `running ${d}`, exact };
    }
    return { text: timeAgo(raw, $now), exact };
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
        {@const ts = stamp(sub)}
        <div style={indentStyle(sub.depth)}>
          <!--
            The whole card opens the sub-agent, not just its title. The most
            informative part of a row is the result preview at the bottom,
            and that is exactly the part a reader reaches for when they want
            to see more — a card where it is the one dead zone teaches the
            wrong thing about what is clickable.

            A div with role="button" rather than a real <button> because the
            card holds its own Stop button, and a button inside a button is
            invalid HTML that browsers resolve by dropping one of them.
          -->
          <div
            role="button"
            tabindex="0"
            aria-label={`Open sub-agent ${sub.handle || sub.profile_key}`}
            onclick={() => onSelect(sub.child_session_id)}
            onkeydown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onSelect(sub.child_session_id);
              }
            }}
            class={"cursor-pointer rounded-xl border p-3 space-y-2 text-left transition-colors " +
              (selectedId === sub.child_session_id
                ? "border-green-500 bg-white-200 dark:bg-navy-800"
                : "border-white-300 hover:border-green-500 dark:border-navy-600 dark:hover:border-green-500 bg-white-200 dark:bg-navy-800")}
          >
            <div class="flex items-center justify-between gap-2">
              <div class="flex items-center gap-2 min-w-0">
                <span class="text-xs font-semibold text-black-900 dark:text-white-100 truncate"
                  >{sub.profile_key}</span
                >
                <!-- A spinner, not just a "Running" chip: the chip says what
                     the row is, the spinner says it is happening right now,
                     and in a list of finished rows only the second one is
                     visible at a glance. -->
                {#if isSubAgentWorking(sub.status, sub.lifecycle)}
                  <span
                    class="h-3 w-3 shrink-0 rounded-full border-2 border-green-500 border-t-transparent animate-spin"
                    aria-label="Working"
                  ></span>
                {/if}
                <span class={"rounded px-1.5 py-0.5 text-[10px] font-medium shrink-0 " + subAgentStatusCls(sub.status)}
                  >{subAgentStatusLabel(sub.status)}</span
                >
              </div>
              <!--
                Stop is offered for queued rows too, not just running ones.
                A queued sub-agent is cancelled by dropping it from the
                queue; hiding the button there leaves work the user cannot
                call off before it starts.

                stopPropagation because the card behind it opens the
                sub-agent: a Stop that also opened the transcript would put
                a panel in front of the thing you just asked to end.
              -->
              {#if isSubAgentLive(sub.status)}
                <button
                  type="button"
                  onclick={(e) => { e.stopPropagation(); onInterrupt(sub.delegation_id); }}
                  class="shrink-0 rounded px-2 py-1 text-[10px] font-medium bg-neg-100 text-neg-400 hover:bg-neg-200 transition-colors"
                >Stop</button>
              {/if}
            </div>

            <p class="text-[11px] text-black-800 dark:text-black-600 line-clamp-2">{sub.label}</p>

            <div class="flex items-center gap-2 text-[10px] text-black-700 dark:text-black-600">
              <span>{turnsLabel(sub)}</span>
              {#if sub.depth > 0}
                <span>· depth {sub.depth}</span>
              {/if}
              <!-- The rounded label is what you scan; the tooltip is what
                   you check when a row's age actually matters. -->
              {#if ts.text}
                <span title={ts.exact}>· {ts.text}</span>
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

    {#if messages.length > 0 || hopsLeft <= 0}
      <div class="border-t border-white-300 dark:border-navy-600">
        <div class="px-4 pt-3 text-xs font-semibold text-black-900 dark:text-white-100">
          Between agents
        </div>
        <MessageThread {messages} {hopsLeft} {onBumpHops} />
      </div>
    {/if}
  </div>
</div>
