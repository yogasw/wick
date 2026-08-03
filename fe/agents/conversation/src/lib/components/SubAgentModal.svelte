<script lang="ts">
  /* Inspector for one sub-agent's session.

     A sub-agent is a real session with a real transcript, so the modal
     renders it with the same ConversationThread the main conversation uses —
     thinking blocks, tool cards and results all come for free. What the modal
     adds is the framing: an overlay that makes it obvious you are no longer
     looking at your own conversation, a breadcrumb for drilling into a
     sub-agent's own sub-agents, and a composer for asking a finished one
     follow-up questions.

     That composer is deliberately NOT take-over (delegation/takeover.go).
     Take-over steers a run whose answer still goes back to the leader, so it
     is gated per role and flags the result as steered. A follow-up question
     to a child that already reported back changes nothing anyone else reads,
     so it needs neither gate nor flag — it is an ordinary message to an
     ordinary session. */
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { toastError } from "@wick-fe/common-stores";
  import { Composer } from "@wick-fe/common-ui";

  import { createThreadStore } from "../stores/thread.js";
  import { connectSession } from "../stores/sse.js";
  import { getConversation, getTurnTrace } from "../api/sessions.js";
  import { getSubAgents, interruptSubAgent } from "../api/subagents.js";
  import { sendMessage } from "../api/messages.js";
  import ConversationThread from "./ConversationThread.svelte";
  import { timeAgo, exactTime, shortDuration, parseEventTime } from "../timeFormat.js";
  import { now } from "../stores/now.js";
  import {
    subAgentStatusCls,
    subAgentStatusLabel,
    isSubAgentLive,
    isSubAgentWorking,
  } from "../lifecycleCls.js";
  import type {
    ConversationTurn,
    LiveTurn,
    TypingState,
    SubAgentItem,
    TurnEvent,
  } from "../types/agents.js";

  type Props = {
    base: string;
    /** The conversation the first crumb was opened from — the parent of
        stack[0], needed to refresh its status. */
    sessionId: string;
    /** The row clicked in the rail. Changing it restarts the breadcrumb. */
    row: SubAgentItem;
    onClose: () => void;
    /** Fired after a Stop so the rail behind the modal can catch up. */
    onChanged?: () => void;
  };

  let { base, sessionId, row, onClose, onChanged }: Props = $props();

  function run<T>(eff: Effect.Effect<T, unknown, never>): Promise<T> {
    return Effect.runPromise(eff);
  }

  /* ── breadcrumb ─────────────────────────────────────────────────
     A sub-agent's own sub-agents push onto this stack rather than opening a
     second modal: stacked overlays give you two dialogs to dismiss and no
     sense of where you are in the tree. */
  let stack = $state<SubAgentItem[]>([row]);

  // Re-seed when the rail selects a different sub-agent. Keyed on the
  // delegation id so a status refresh of the SAME row does not throw away a
  // breadcrumb the user has already drilled into.
  let seededFrom = $state(row.delegation_id);
  $effect(() => {
    if (row.delegation_id !== seededFrom) {
      seededFrom = row.delegation_id;
      stack = [row];
    }
  });

  const current = $derived(stack[stack.length - 1]);
  const currentSessionId = $derived(current?.child_session_id ?? "");
  // Whose rail lists `current`: the conversation for the first crumb, the
  // previous crumb's session for anything deeper.
  const parentSessionId = $derived(
    stack.length > 1 ? stack[stack.length - 2].child_session_id : sessionId,
  );

  /* ── transcript ─────────────────────────────────────────────────
     One thread store per crumb. Rebuilt on navigation rather than reset,
     so no event from the previous session can land in the new one. */
  let turns = $state<ConversationTurn[]>([]);
  let live = $state<LiveTurn | null>(null);
  let typing = $state<TypingState>({ active: false });
  let loading = $state(true);

  // Plain let, not $state: reassigning it inside the effect that owns it
  // would otherwise re-trigger that effect.
  let thread = createThreadStore();

  function loadConversation(sid: string, store: ReturnType<typeof createThreadStore>) {
    return run(getConversation(base, sid).pipe(Effect.provide(WickClientLayer)))
      .then((res) => { store.setHistory(res?.turns ?? []); })
      .catch((e: unknown) =>
        toastError(`Sub-agent history: ${e instanceof Error ? e.message : String(e)}`),
      )
      .finally(() => { loading = false; });
  }

  $effect(() => {
    const sid = currentSessionId;
    if (!sid) return;

    const store = createThreadStore();
    thread = store;
    turns = [];
    live = null;
    typing = { active: false };
    loading = true;

    const unsubs = [
      store.turns.subscribe((v) => { turns = v; }),
      store.live.subscribe((v) => { live = v; }),
      store.typing.subscribe((v) => { typing = v; }),
    ];
    void loadConversation(sid, store);

    // The child publishes its own lifecycle on its own session id, so the
    // modal subscribes directly instead of piggybacking on the leader's
    // stream — that is what makes a running sub-agent watchable live.
    const stream = connectSession(base, sid);
    stream.onEvent((ev) => {
      store.handleEvent(ev);
      if (ev.type === "done" || ev.type === "error") {
        void loadConversation(sid, store);
        refreshStatus();
        loadChildren();
      } else if (ev.type === "lifecycle") {
        refreshStatus();
      }
    });

    return () => {
      unsubs.forEach((u) => u());
      stream.close();
    };
  });

  function loadTrace(turnId: string): Promise<TurnEvent[]> {
    return run(getTurnTrace(base, currentSessionId, turnId).pipe(Effect.provide(WickClientLayer)))
      .catch(() => [] as TurnEvent[]);
  }

  /* ── this crumb's own sub-agents ───────────────────────────────── */
  let children = $state<SubAgentItem[]>([]);

  function loadChildren() {
    const sid = currentSessionId;
    if (!sid) return;
    run(getSubAgents(base, sid).pipe(Effect.provide(WickClientLayer)))
      .then((rows) => { if (sid === currentSessionId) children = rows ?? []; })
      .catch(() => { /* an empty drill-down list is not worth a toast */ });
  }

  $effect(() => {
    void currentSessionId;
    children = [];
    loadChildren();
  });

  /* Status, turns used and result all move while a sub-agent runs, and they
     live on the PARENT's rail rather than on the child itself. */
  function refreshStatus() {
    const pid = parentSessionId;
    const delegationId = current?.delegation_id;
    if (!pid || !delegationId) return;
    run(getSubAgents(base, pid).pipe(Effect.provide(WickClientLayer)))
      .then((rows) => {
        const fresh = (rows ?? []).find((r) => r.delegation_id === delegationId);
        if (!fresh) return;
        stack = stack.map((s) => (s.delegation_id === delegationId ? fresh : s));
      })
      .catch(() => { /* the transcript is still readable without a fresh badge */ });
  }

  /* ── navigation ─────────────────────────────────────────────────── */
  function push(next: SubAgentItem) {
    stack = [...stack, next];
  }

  function openDelegation(delegationId: string) {
    const next = children.find((c) => c.delegation_id === delegationId);
    if (next) push(next);
  }

  function back() {
    if (stack.length > 1) stack = stack.slice(0, -1);
    else onClose();
  }

  function jumpTo(index: number) {
    if (index < stack.length - 1) stack = stack.slice(0, index + 1);
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.stopPropagation();
      back();
    }
  }

  /* ── actions ────────────────────────────────────────────────────── */
  function stop() {
    const delegationId = current?.delegation_id;
    if (!delegationId) return;
    run(interruptSubAgent(base, delegationId).pipe(Effect.provide(WickClientLayer)))
      .then(() => { refreshStatus(); onChanged?.(); })
      .catch((e: unknown) =>
        toastError(`Stop sub-agent: ${e instanceof Error ? e.message : String(e)}`),
      );
  }

  let sending = $state(false);

  function send(msg: { text: string; files: File[] }) {
    const sid = currentSessionId;
    const text = msg.text.trim();
    if (!sid || !text || sending) return;
    sending = true;
    thread.appendUserTurn(text);
    run(sendMessage(base, sid, { text }).pipe(Effect.provide(WickClientLayer)))
      .then(() => { refreshStatus(); })
      .catch((e: unknown) =>
        toastError(`Send: ${e instanceof Error ? e.message : String(e)}`),
      )
      .finally(() => { sending = false; });
  }

  const live_ = $derived(current ? isSubAgentLive(current.status) : false);
  const working = $derived(current ? isSubAgentWorking(current.status, current.lifecycle) : false);

  // A live sub-agent is dated by when it started (how long has it been at
  // this?); a finished one by when it ended (how fresh is this answer?).
  const stampRaw = $derived(
    current ? (live_ ? current.started_at : (current.ended_at ?? current.started_at)) : undefined,
  );
  const stampText = $derived.by(() => {
    if (!stampRaw) return "";
    if (!live_) return timeAgo(stampRaw, $now);
    const d = shortDuration($now - (parseEventTime(stampRaw) ?? $now));
    return d === "just now" ? "just started" : `running ${d}`;
  });
  const turnsLabel = $derived(
    current
      ? current.max_turns > 0
        ? `${current.turns_used}/${current.max_turns} turns`
        : `${current.turns_used} turns`
      : "",
  );
</script>

<svelte:window on:keydown={onKeydown} />

{#if current}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-navy-900/40 p-4"
    role="button"
    tabindex="-1"
    onclick={onClose}
    onkeydown={(e) => e.key === "Enter" && onClose()}
  >
    <div
      class="flex h-[85vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-900 shadow-2xl"
      role="dialog"
      aria-modal="true"
      aria-label={`Sub-agent ${current.profile_key}`}
      tabindex="-1"
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => e.stopPropagation()}
    >
      <!-- header: breadcrumb + status + actions -->
      <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 px-4 py-3">
<!-- Only shown once there is somewhere to go back TO. At the root crumb
             Back and Close would be the same act, and two buttons for it read
             as two different ones. -->
        {#if stack.length > 1}
          <button
            type="button"
            onclick={back}
            aria-label="Back to the previous sub-agent"
            class="shrink-0 rounded-lg p-1 text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
          >
            <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5">
              <path d="M10 3L5 8l5 5" stroke-linecap="round" stroke-linejoin="round"></path>
            </svg>
          </button>
        {/if}

        <div class="flex min-w-0 flex-1 items-center gap-1 text-xs">
          <span class="shrink-0 text-black-700 dark:text-black-600">Sub-agent</span>
          {#each stack as crumb, i (crumb.delegation_id)}
            <span class="shrink-0 text-black-600 dark:text-black-500">·</span>
            <button
              type="button"
              onclick={() => jumpTo(i)}
              disabled={i === stack.length - 1}
              class={"max-w-[10rem] truncate " +
                (i === stack.length - 1
                  ? "font-semibold text-black-900 dark:text-white-100"
                  : "text-black-700 hover:underline dark:text-black-600")}
            >{crumb.handle || crumb.profile_key}</button>
          {/each}
        </div>

        {#if working}
          <span
            class="h-3 w-3 shrink-0 rounded-full border-2 border-green-500 border-t-transparent animate-spin"
            aria-label="Working"
          ></span>
        {/if}
        <span class={"shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium " + subAgentStatusCls(current.status)}
          >{subAgentStatusLabel(current.status)}</span
        >
        <span class="shrink-0 text-[10px] text-black-700 dark:text-black-600">{turnsLabel}</span>
        {#if stampText}
          <span
            class="shrink-0 text-[10px] text-black-700 dark:text-black-600"
            title={exactTime(stampRaw)}
          >· {stampText}</span>
        {/if}

        {#if live_}
          <button
            type="button"
            onclick={stop}
            class="shrink-0 rounded px-2 py-1 text-[10px] font-medium bg-neg-100 text-neg-400 transition-colors hover:bg-neg-200"
          >Stop</button>
        {/if}
        <button
          type="button"
          onclick={onClose}
          aria-label="Close"
          class="shrink-0 rounded-lg p-1 text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
        >
          <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5">
            <path d="M4 4l8 8M12 4l-8 8" stroke-linecap="round"></path>
          </svg>
        </button>
      </div>

      <!-- the task it was given, so the transcript has context -->
      <p class="border-b border-white-300 dark:border-navy-600 px-4 py-2 text-[11px] text-black-800 dark:text-black-600">
        {current.label}
      </p>

      <!-- its own sub-agents, one level down -->
      {#if children.length > 0}
        <div class="flex flex-wrap items-center gap-1.5 border-b border-white-300 dark:border-navy-600 px-4 py-2">
          <span class="text-[10px] text-black-700 dark:text-black-600">Delegated to</span>
          {#each children as child (child.delegation_id)}
            <button
              type="button"
              onclick={() => push(child)}
              class="rounded-full border border-white-400 dark:border-navy-600 px-2 py-0.5 text-[10px] font-medium text-black-800 transition-colors hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-800"
            >{child.handle || child.profile_key}</button>
          {/each}
        </div>
      {/if}

      <div class="min-h-0 flex-1 overflow-y-auto">
        {#if loading && turns.length === 0}
          <p class="px-4 py-6 text-xs text-black-700 dark:text-black-600">Loading transcript…</p>
        {:else if turns.length === 0 && !live}
          <p class="px-4 py-6 text-xs text-black-700 dark:text-black-600">
            This sub-agent has not produced any output yet.
          </p>
        {:else}
          <ConversationThread
            {turns}
            {live}
            {typing}
            {loadTrace}
            onOpenSubAgent={openDelegation}
          />
        {/if}
      </div>

      <div class="border-t border-white-300 dark:border-navy-600 p-3">
        <Composer
          onSend={send}
          disabled={sending}
          minRows={1}
          placeholder={live_ ? "Send a message to this sub-agent…" : "Ask this sub-agent a follow-up…"}
        />
        <p class="px-1 pt-1.5 text-[10px] text-black-700 dark:text-black-600">
          Replies here stay in this sub-agent's session — nothing is sent back to the agent that delegated it.
        </p>
      </div>
    </div>
  </div>
{/if}
