<script lang="ts">
  import type { AgentMessageItem, IncidentSummary, SubAgentItem } from "../types/agents.js";
  import MessageThread from "./MessageThread.svelte";
  import {
    subAgentStatusCls,
    subAgentStatusLabel,
    isSubAgentLive,
    isSubAgentWorking,
    confidenceCls,
    confidenceLabel,
    incidentStatusCls,
    incidentStatusLabel,
  } from "../lifecycleCls.js";
  import { timeAgo, exactTime, shortDuration, parseEventTime } from "../timeFormat.js";
  import { now } from "../stores/now.js";

  type Props = {
    incident?: IncidentSummary | null;
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
    incident = null,
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

  /* A room runs one sub-agent at a time, so the rail's job is to answer
     "what is happening, what is next, what is done" in that order.
     Grouping does it; a flat list sorted by time makes the reader
     cross-reference status chips row by row to work out the same thing.

     Queued rows are ordered by the server-computed position rather than
     by timestamp: the panel must not re-derive an ordering it can be
     told, especially from times it may have received out of order. */
  const groups = $derived(
    [
      {
        title: "Working",
        rows: subAgents.filter((s) => s.status === "running"),
      },
      {
        title: "Queued",
        rows: subAgents
          .filter((s) => s.status === "queued")
          .sort((a, b) => (a.queue_position ?? 0) - (b.queue_position ?? 0)),
      },
      {
        title: "Finished",
        rows: subAgents.filter((s) => !isSubAgentLive(s.status)),
      },
    ].filter((g) => g.rows.length > 0),
  );

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

  <!-- The investigation is the frame every row below sits in, so it goes
       above the list rather than inside one card. Absent for an ordinary
       conversation: a header on all of them would be noise that teaches
       people to stop reading it. -->
  {#if incident}
    <div
      data-testid="incident-header"
      class="border-b border-white-300 dark:border-navy-600 px-4 py-3 space-y-1"
    >
      <div class="flex items-center gap-2">
        <span class={"rounded px-1.5 py-0.5 text-[10px] font-medium " + incidentStatusCls(incident.status)}
          >{incidentStatusLabel(incident.status)}</span>
        <span class="text-[10px] text-black-700 dark:text-black-600">round {incident.iteration}</span>
        <span class="text-[10px] text-black-700 dark:text-black-600"
          >{incident.evidence_count} evidence</span>
      </div>
      {#if incident.summary}
        <p class="text-[11px] text-black-800 dark:text-black-600 line-clamp-3">{incident.summary}</p>
      {/if}
      <!-- An investigation that stopped without saying why looks
           identical to one still running. -->
      {#if incident.stop_reason}
        <p class="text-[10px] text-black-700 dark:text-black-600">stopped: {incident.stop_reason}</p>
      {/if}
    </div>
  {/if}

  <div class="p-4 space-y-3">
    {#if subAgents.length === 0}
      <p class="text-xs text-black-700 dark:text-black-600 py-4 px-2">
        No sub-agents for this session.
      </p>
    {:else}
      {#each groups as group (group.title)}
      <p
        data-testid="subagent-group"
        class="px-1 pt-1 text-[10px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600"
      >{group.title}</p>
      {#each group.rows as sub (sub.delegation_id)}
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
                <!-- Position, and deliberately no estimate: an honest
                     "starts in" is not available, and a dishonest one is
                     worse than saying nothing. -->
                {#if sub.status === "queued" && (sub.queue_position ?? 0) > 0}
                  <span
                    class="shrink-0 text-[10px] font-medium text-black-700 dark:text-black-600"
                    title="Place in this conversation's queue"
                  >#{sub.queue_position}</span>
                {/if}
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

            <!-- Confidence sits with the other facts about the run, not
                 with the status chips: it describes the ANSWER, and a
                 reader scanning for "can I act on this" reads it here
                 alongside how long it took and what it cost. -->
            {#if sub.envelope}
              <div class="flex items-center gap-2">
                <span
                  class={"rounded px-1.5 py-0.5 text-[10px] font-medium " +
                    confidenceCls(sub.envelope.structured ? sub.envelope.confidence : "")}
                >{confidenceLabel(sub.envelope.confidence, sub.envelope.structured)}</span>
                {#if sub.envelope.evidence?.length}
                  <span class="text-[10px] text-black-700 dark:text-black-600"
                    >{sub.envelope.evidence.length} evidence</span>
                {/if}
              </div>
            {/if}

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
