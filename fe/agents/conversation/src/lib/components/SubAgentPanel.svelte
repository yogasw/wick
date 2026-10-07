<script lang="ts">
  import type { AgentMessageItem, IncidentSummary, SubAgentItem, TeamTaskItem } from "../types/agents.js";
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import { subAgentTitle, subAgentTurns, handoffState } from "../teamMention.js";
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
    /** Sends a finished sub-agent back to work in its own session.
        Optional: a surface that has not wired it shows no button, which
        beats offering one that does nothing. */
    onContinue?: (delegationId: string, task: string) => void;
    messages: AgentMessageItem[];
    hopsLeft: number;
    onBumpHops: () => void;
    /** Team (A2A) tasks this chat sent — their own section, never mixed
        with sub-agent delegations. */
    teamTasks?: TeamTaskItem[];
    teamAgents?: Record<string, { name: string; kind?: string; shape?: string; color?: string; expression?: string }>;
    onOpenAgent?: (handle: string) => void;
  };

  let {
    incident = null,
    subAgents,
    selectedId,
    onSelect,
    onInterrupt,
    onInterruptAll,
    onContinue = undefined,
    messages,
    hopsLeft,
    onBumpHops,
    teamTasks = [],
    teamAgents = {},
    onOpenAgent = undefined,
  }: Props = $props();

  function teamStateCls(state: string): string {
    switch (handoffState(state)) {
      case "working":
        return "bg-green-500/10 text-green-600 dark:text-green-400";
      case "needs input":
        return "bg-amber-400/10 text-amber-700 dark:text-amber-300";
      case "failed":
        return "bg-neg-100 text-neg-400";
    }
    return "bg-white-300 text-black-800 dark:bg-navy-700 dark:text-white-100";
  }
  function teamDuration(t: TeamTaskItem): string {
    const a = parseEventTime(t.started_at);
    if (a == null) return "";
    const end = handoffState(t.state) === "working" ? $now : (parseEventTime(t.updated_at) ?? $now);
    return shortDuration(end - a);
  }

  /* Which row has its continue composer open, and what has been typed
     into it. One at a time: two open composers on a narrow rail is a
     reader deciding which box they are in rather than what to write. */
  let continuingId = $state<string | null>(null);
  let continueTask = $state("");

  function openContinue(delegationId: string) {
    continuingId = delegationId;
    continueTask = "";
  }

  function submitContinue(delegationId: string) {
    const task = continueTask.trim();
    // An empty instruction would reach the server only to be refused, so
    // the form must not behave as though it were sent.
    if (!task || !onContinue) return;
    onContinue(delegationId, task);
    continuingId = null;
    continueTask = "";
  }

  const liveCount = $derived(subAgents.filter((s) => isSubAgentLive(s.status)).length);

  /* ── Stop, kept out of the way ─────────────────────────────────────
     A Stop on every live row is a red button the reader scrolls past all
     day and hits by accident on a phone. It stays hidden until asked for:
     hovering the row (a mouse), focusing it (a keyboard), or holding it
     (a finger, ~500 ms — the hold must not also open the row). Even then
     it asks once before stopping. */
  const HOLD_MS = 500;
  let hoverId = $state<string | null>(null);
  let focusId = $state<string | null>(null);
  let revealedId = $state<string | null>(null);
  let confirmId = $state<string | null>(null);
  let holdTimer: ReturnType<typeof setTimeout> | undefined;
  let holdFired = false;

  function stopShown(id: string): boolean {
    return hoverId === id || focusId === id || revealedId === id || confirmId === id;
  }

  function rowPointerEnter(e: PointerEvent, id: string) {
    // A tap raises pointerenter too; only a real pointer hovers.
    if (e.pointerType === "touch") return;
    hoverId = id;
  }

  function rowPointerLeave(id: string) {
    if (hoverId === id) hoverId = null;
    holdCancel();
  }

  function rowFocusOut(e: FocusEvent, id: string) {
    const next = e.relatedTarget as Node | null;
    if (next && (e.currentTarget as HTMLElement).contains(next)) return;
    if (focusId === id) focusId = null;
  }

  function holdStart(e: PointerEvent, id: string) {
    if (e.button > 0) return;
    holdFired = false;
    clearTimeout(holdTimer);
    holdTimer = setTimeout(() => {
      holdFired = true;
      revealedId = id;
    }, HOLD_MS);
  }

  function holdCancel() {
    clearTimeout(holdTimer);
    holdTimer = undefined;
  }

  function rowContextMenu(e: MouseEvent, id: string) {
    // Mobile browsers raise contextmenu on a long-press; the native menu
    // would cover the Stop it just revealed.
    e.preventDefault();
    holdCancel();
    holdFired = true;
    revealedId = id;
  }

  function rowOpen(childSessionId: string) {
    if (holdFired) {
      holdFired = false;
      return;
    }
    onSelect(childSessionId);
  }

  function askStop(id: string) {
    confirmId = id;
  }

  function confirmStop(id: string) {
    confirmId = null;
    revealedId = null;
    onInterrupt(id);
  }

  function cancelStop() {
    confirmId = null;
    revealedId = null;
  }

  /* ── header ⋯ menu ─────────────────────────────────────────────────── */
  let menuOpen = $state(false);
  let confirmAll = $state(false);
  let menuWrapEl: HTMLDivElement | undefined = $state();

  function toggleMenu() {
    menuOpen = !menuOpen;
    confirmAll = false;
  }

  function closeMenu() {
    menuOpen = false;
    confirmAll = false;
  }

  function stopAll() {
    closeMenu();
    onInterruptAll();
  }

  // One outside-tap / Escape closes whatever is open: the ⋯ menu, a held
  // row's Stop, or its confirm.
  $effect(() => {
    if (!menuOpen && !revealedId && !confirmId) return;
    function onDown(e: PointerEvent) {
      const t = e.target as Element | null;
      if (menuOpen && !(t && menuWrapEl?.contains(t))) closeMenu();
      if ((revealedId || confirmId) && !t?.closest?.(`[data-subagent-row="${confirmId ?? revealedId}"]`)) cancelStop();
    }
    function onKey(e: KeyboardEvent) {
      if (e.key !== "Escape") return;
      closeMenu();
      cancelStop();
    }
    window.addEventListener("pointerdown", onDown, true);
    window.addEventListener("keydown", onKey, true);
    return () => {
      window.removeEventListener("pointerdown", onDown, true);
      window.removeEventListener("keydown", onKey, true);
    };
  });

  $effect(() => () => clearTimeout(holdTimer));

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
    return subAgentTurns(s);
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
    {#if subAgents.length > 0}
      <div class="relative -my-2 -mr-2" bind:this={menuWrapEl}>
        <button
          type="button"
          aria-label="Sub-agent actions"
          aria-haspopup="menu"
          aria-expanded={menuOpen}
          onclick={toggleMenu}
          class="inline-flex h-10 w-10 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-700"
        >
          <svg viewBox="0 0 16 16" class="h-4 w-4" fill="currentColor" aria-hidden="true">
            <circle cx="3" cy="8" r="1.5"></circle><circle cx="8" cy="8" r="1.5"></circle><circle cx="13" cy="8" r="1.5"></circle>
          </svg>
        </button>
        {#if menuOpen}
          <!-- Hugs the rail's right edge and opens downward, so on a
               390px screen it grows inward and is never cut off. -->
          <div
            role="menu"
            aria-label="Sub-agent actions"
            class="absolute right-0 top-full z-30 mt-1 w-60 max-w-[calc(100vw-1rem)] overflow-hidden rounded-xl border border-white-300 bg-white-100 py-1 shadow-lg dark:border-navy-600 dark:bg-navy-800"
          >
            {#if confirmAll}
              <div class="space-y-2 px-3 py-2">
                <p class="text-xs text-black-900 dark:text-white-100">
                  Stop {liveCount} running sub-agent{liveCount === 1 ? "" : "s"}? Finished ones are left alone.
                </p>
                <div class="flex items-center justify-end gap-2">
                  <button
                    type="button"
                    onclick={() => (confirmAll = false)}
                    class="min-h-10 rounded-lg px-3 text-xs font-medium text-black-800 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-700"
                  >Cancel</button>
                  <button
                    type="button"
                    onclick={stopAll}
                    class="min-h-10 rounded-lg bg-neg-400 px-3 text-xs font-medium text-white-100 transition-colors hover:opacity-90"
                  >Stop all</button>
                </div>
              </div>
            {:else}
              <button
                type="button"
                role="menuitem"
                disabled={liveCount === 0}
                onclick={() => (confirmAll = true)}
                class="flex min-h-10 w-full items-center gap-2 px-3 text-left text-sm text-neg-400 transition-colors hover:bg-neg-100 disabled:cursor-not-allowed disabled:text-black-600 disabled:hover:bg-transparent dark:hover:bg-navy-700 dark:disabled:text-black-700"
              >
                <svg viewBox="0 0 16 16" class="h-3 w-3" fill="currentColor" aria-hidden="true"><rect x="2" y="2" width="12" height="12" rx="2"></rect></svg>
                Stop all ({liveCount} running)
              </button>
            {/if}
          </div>
        {/if}
      </div>
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

  {#if teamTasks.length > 0}
    <div class="px-4 pt-4 space-y-2" data-testid="team-tasks">
      <p class="px-1 text-[10px] font-semibold uppercase tracking-wide text-black-700 dark:text-black-600">Team</p>
      {#each teamTasks as t (t.task_id)}
        {@const peer = teamAgents[t.to_handle]}
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-3 space-y-1.5">
          <div class="flex items-center justify-between gap-2">
            <div class="flex min-w-0 items-center gap-2">
              <AgentAvatar kind={peer?.kind} shape={peer?.shape} expression={peer?.expression} color={peer?.color} size={18} />
              <span class="truncate text-xs font-semibold text-black-900 dark:text-white-100">{peer?.name || t.to_name || t.to_handle}</span>
              <span class="shrink-0 text-[10px] text-black-700 dark:text-black-600">@{t.to_handle}</span>
            </div>
            <span class={"shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium " + teamStateCls(t.state)}>{handoffState(t.state)}</span>
          </div>
          {#if t.title}
            <p class="text-[11px] text-black-800 dark:text-black-600 line-clamp-2">{t.title}</p>
          {/if}
          <div class="flex items-center gap-2 text-[10px] text-black-700 dark:text-black-600">
            {#if t.max_turns > 0}<span title="Turns of this exchange">turn {t.turns}/{t.max_turns}</span>{/if}
            {#if teamDuration(t)}<span>· {teamDuration(t)}</span>{/if}
            {#if onOpenAgent}
              <button type="button" class="ml-auto font-medium text-green-600 dark:text-green-400 hover:underline" onclick={() => onOpenAgent?.(t.to_handle)}>Open chat</button>
            {/if}
          </div>
        </div>
      {/each}
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
        {@const live = isSubAgentLive(sub.status)}
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
            data-subagent-row={sub.delegation_id}
            aria-label={`Open sub-agent ${sub.handle || sub.profile_key}`}
            onclick={() => rowOpen(sub.child_session_id)}
            onkeydown={(e) => {
              if (e.target !== e.currentTarget) return;
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onSelect(sub.child_session_id);
              }
            }}
            onpointerenter={live ? (e) => rowPointerEnter(e, sub.delegation_id) : undefined}
            onpointerleave={live ? () => rowPointerLeave(sub.delegation_id) : undefined}
            onpointerdown={live ? (e) => holdStart(e, sub.delegation_id) : undefined}
            onpointerup={live ? holdCancel : undefined}
            onpointercancel={live ? holdCancel : undefined}
            oncontextmenu={live ? (e) => rowContextMenu(e, sub.delegation_id) : undefined}
            onfocusin={live ? () => (focusId = sub.delegation_id) : undefined}
            onfocusout={live ? (e) => rowFocusOut(e, sub.delegation_id) : undefined}
            class={"cursor-pointer rounded-xl border p-3 space-y-2 text-left transition-colors " +
              (live ? "select-none [-webkit-touch-callout:none] " : "") +
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
              {#if live}
                {#if stopShown(sub.delegation_id) && confirmId !== sub.delegation_id}
                  <button
                    type="button"
                    onclick={(e) => { e.stopPropagation(); askStop(sub.delegation_id); }}
                    onpointerdown={(e) => e.stopPropagation()}
                    class="-my-2 -mr-2 inline-flex h-10 shrink-0 items-center rounded-lg px-3 text-[11px] font-medium text-neg-400 hover:bg-neg-100 transition-colors"
                  >Stop</button>
                {/if}
              {:else if onContinue}
                <!--
                  Only on a stopped row: continuing a working sub-agent
                  would put two drivers on one session, interleaving with
                  the turn it is already thinking through.

                  stopPropagation for the same reason Stop does it — the
                  card behind opens the transcript, and a composer with a
                  panel on top of it is unusable.
                -->
                <button
                  type="button"
                  onclick={(e) => { e.stopPropagation(); openContinue(sub.delegation_id); }}
                  class="shrink-0 rounded px-2 py-1 text-[10px] font-medium bg-white-300 text-black-800 hover:bg-white-400 dark:bg-navy-700 dark:text-white-100 dark:hover:bg-navy-600 transition-colors"
                  title="Send this sub-agent back to work in the same session, keeping everything it learned"
                >Continue</button>
              {/if}
            </div>

            {#if confirmId === sub.delegation_id}
              <div
                role="presentation"
                onclick={(e) => e.stopPropagation()}
                onkeydown={(e) => e.stopPropagation()}
                onpointerdown={(e) => e.stopPropagation()}
                class="flex items-center justify-between gap-2 rounded-lg border border-neg-200 bg-neg-100 px-2 py-1 dark:border-navy-600 dark:bg-navy-700"
              >
                <span class="min-w-0 truncate text-[11px] font-medium text-neg-400">Stop {sub.handle || sub.profile_key}?</span>
                <div class="flex shrink-0 items-center gap-1">
                  <button
                    type="button"
                    onclick={cancelStop}
                    class="min-h-10 rounded-lg px-3 text-[11px] font-medium text-black-800 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600"
                  >Cancel</button>
                  <button
                    type="button"
                    onclick={() => confirmStop(sub.delegation_id)}
                    class="min-h-10 rounded-lg bg-neg-400 px-3 text-[11px] font-medium text-white-100 transition-colors hover:opacity-90"
                  >Stop</button>
                </div>
              </div>
            {/if}

            <p class="text-[11px] text-black-800 dark:text-black-600 line-clamp-2" title={sub.label}>{subAgentTitle(sub)}</p>
            {#if (sub.resumes ?? 0) > 0}
              <span data-testid="resumed-badge" class="inline-block rounded px-1.5 py-0.5 text-[10px] font-medium bg-white-300 text-black-800 dark:bg-navy-700 dark:text-white-100"
                >Resumed ×{sub.resumes}</span>
            {/if}

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

            <!--
              The instruction is the point of continuing, so it is asked
              for rather than defaulted. A one-click "carry on" sends the
              sub-agent back with no idea what changed since it stopped,
              which is how a supervisor gets the same wrong answer twice.

              The wrapper swallows clicks and Enter so typing inside the
              composer never opens the transcript behind it.
            -->
            {#if continuingId === sub.delegation_id}
              <div
                role="presentation"
                onclick={(e) => e.stopPropagation()}
                onkeydown={(e) => e.stopPropagation()}
                class="space-y-2 rounded-lg border border-white-300 dark:border-navy-600 p-2"
              >
                <textarea
                  bind:value={continueTask}
                  rows="2"
                  placeholder="What should it do next? It still remembers its earlier work."
                  class="w-full resize-none rounded border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-900 px-2 py-1 text-[11px] text-black-900 dark:text-white-100 placeholder:text-black-600 focus:border-green-500 focus:outline-none"
                ></textarea>
                <div class="flex items-center justify-end gap-2">
                  <button
                    type="button"
                    onclick={() => { continuingId = null; continueTask = ""; }}
                    class="rounded px-2 py-1 text-[10px] font-medium text-black-800 hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-700 transition-colors"
                  >Cancel</button>
                  <button
                    type="button"
                    onclick={() => submitContinue(sub.delegation_id)}
                    class="rounded px-2 py-1 text-[10px] font-medium bg-green-500 text-white-100 hover:bg-green-600 disabled:opacity-50 transition-colors"
                  >Send</button>
                </div>
              </div>
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
