<script lang="ts">
  /* One assistant turn's team_message calls, as one block instead of a
     raw ToolCard per call: "Delegated to N teammates", a status summary
     and a chip per teammate. A question that waits for the person opens
     its needs-input section (AnswerBox) under the chips; one the agent
     is handling reads "Captain is answering…" with an "Answer instead"
     link. A task the person sent with an @mention is theirs: marked "You
     asked", no answering stage, the teammate's reply shows here. The
     chevron in the head collapses the block to its chips. An open chip
     shows the task's thread (TaskThread) under a head with Reply in
     thread, Open chat, Collapse and Cancel: on a question it opens the
     answer card, on any other task a composer under the thread whose
     message joins that task (onMessage). Once every task
     ended it folds to a single row (check, avatars, "Delegated to 3
     teammates · all replied · 12 messages · 13m", chevron). */
  import { tick, untrack } from "svelte";
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import AvatarActivity from "./AvatarActivity.svelte";
  import { taskActivity } from "../../avatarActivity.js";
  import type { TeamTaskItem, TeamThreadEntry } from "../../types/agents.js";
  import { allSettled, blockHead, chipElapsed, chipLabel, clockTime, foldAvatars, isTaskActive, isUserTask, settledParts, statusParts, taskProgress, taskQuickReplies, taskStatus, teammateName, USER_TASK_MARKER, userTaskRoute, type StatusPart, type TaskStatus } from "../../delegations.js";
  import { questionHtml } from "../../questionText.js";
  import { clearDraft, getDraft, setDraft } from "../../answerDrafts.js";
  import AnswerBox from "./AnswerBox.svelte";
  import CancelConfirm from "./CancelConfirm.svelte";
  import SmBtn from "./SmBtn.svelte";
  import TaskThread from "./TaskThread.svelte";
  import TeamIcon from "./TeamIcon.svelte";

  type Agent = { name: string; kind?: string; shape?: string; color?: string; expression?: string; image?: import("@wick-fe/common-avatar").AvatarImageView | null };
  type Props = {
    tasks: TeamTaskItem[];
    agents?: Record<string, Agent>;
    captainName?: string;
    onAnswer?: (taskId: string, text: string) => Promise<void>;
    onCancel?: (taskId: string) => Promise<void>;
    /** Reply in thread on a task that asks nothing: working or ended. */
    onMessage?: (taskId: string, text: string) => Promise<void>;
    onOpenChat?: (t: TeamTaskItem) => void;
    /** Reads a task's thread for its open chip (TaskThread). */
    loadThread?: (taskId: string) => Promise<TeamThreadEntry[]>;
  };
  let { tasks, agents = {}, captainName = "Captain", onAnswer, onCancel, onMessage, onOpenChat, loadThread }: Props = $props();

  let showAll = $state(false);
  let unfolded = $state(false);
  // The head's chevron: chips stay, the open sections below them hide.
  let collapsed = $state(false);
  let collapseBtn = $state<HTMLButtonElement>();
  let selected = $state<string | null>(null);
  // "Answer instead" on a task the agent is handling opens its card too.
  let answerOpen = $state<Record<string, boolean>>({});
  // Per task, seeded from answerDrafts so a remount keeps what was typed.
  let drafts = $state<Record<string, string>>({});
  const draftOf = (id: string) => drafts[id] ?? getDraft(id);
  let confirming = $state<string | null>(null);
  let sending = $state<string | null>(null);
  let errors = $state<Record<string, string>>({});
  // Reply in thread on a task that asks nothing: its composer, its text
  // and its error, per task. The composer stays open after a send, so the
  // message shows in the thread above it.
  let replyOpen = $state<Record<string, boolean>>({});
  let replyDrafts = $state<Record<string, string>>({});
  let replyErrors = $state<Record<string, string>>({});
  // The server's cap on a message (teamlink.MaxUserMessage): the thread
  // keeps it whole up to there, so the composer stops at the same length.
  const MESSAGE_MAX = 1200;
  // A task that asks a question while its composer is open: the reply
  // becomes the answer — its card opens with what was typed, the
  // composer closes.
  $effect(() => {
    const asking = tasks.filter((t) => {
      const s = taskStatus(t);
      return (s === "needs_you" || s === "answering") && untrack(() => replyOpen[t.task_id]);
    });
    if (asking.length === 0) return;
    untrack(() => {
      for (const t of asking) {
        const typed = replyDrafts[t.task_id] ?? "";
        if (typed.trim() && !draftOf(t.task_id).trim()) {
          drafts = { ...drafts, [t.task_id]: typed };
          setDraft(t.task_id, typed);
        }
        answerOpen = { ...answerOpen, [t.task_id]: true };
        replyOpen = { ...replyOpen, [t.task_id]: false };
        replyDrafts = { ...replyDrafts, [t.task_id]: "" };
      }
    });
  });

  const folded = $derived(allSettled(tasks) && !unfolded);

  // The chips' timers tick once a second, only while a task still runs.
  let now = $state(Date.now());
  const ticking = $derived(tasks.some(isTaskActive));
  $effect(() => {
    if (!ticking) return;
    now = Date.now();
    const id = setInterval(() => { now = Date.now(); }, 1000);
    return () => clearInterval(id);
  });

  // The summary's parts in the mockup's colours: working ok, needs input
  // warn, replied tx2, a failure err, canceled tx3.
  const PART_TONE: Record<StatusPart["status"], string> = {
    working: "font-semibold text-green-600 dark:text-green-400",
    needs_you: "font-semibold text-amber-700 dark:text-amber-300",
    replied: "text-black-800 dark:text-black-600",
    declined: "text-red-600 dark:text-red-400",
    failed: "text-red-600 dark:text-red-400",
    canceled: "text-black-700",
  };
  // The chip's status word, by the same tones; answering is the grey one.
  const LABEL_TONE: Record<TaskStatus, string> = {
    needs_you: "font-semibold text-amber-700 dark:text-amber-300",
    answering: "text-black-700",
    working: "text-green-600 dark:text-green-400",
    replied: "text-green-600 dark:text-green-400",
    declined: "text-red-600 dark:text-red-400",
    failed: "text-red-600 dark:text-red-400",
    canceled: "text-black-700",
  };
  const parts = $derived(statusParts(tasks));
  const settled = $derived(settledParts(tasks));

  function nameOf(t: TeamTaskItem): string {
    return teammateName(t, agents);
  }
  // A chip is open while its preview shows (clicked, or Show all).
  function chipOpen(t: TeamTaskItem): boolean {
    return !collapsed && (showAll || selected === t.task_id);
  }
  function cardOpen(t: TeamTaskItem): boolean {
    const s = taskStatus(t);
    return s === "needs_you" || (s === "answering" && !!answerOpen[t.task_id]);
  }

  // The section head's "asked 10:12", once the server sends asked_at
  // (B1); updated_at moves with every touch of the task, so no fallback.
  function askedClock(t: TeamTaskItem): string {
    return t.asked_at ? clockTime(Date.parse(t.asked_at)) : "";
  }

  async function send(t: TeamTaskItem, text: string) {
    if (!onAnswer || !text.trim() || sending) return;
    sending = t.task_id;
    errors = { ...errors, [t.task_id]: "" };
    try {
      await onAnswer(t.task_id, text.trim());
      clearDraft(t.task_id);
      drafts = { ...drafts, [t.task_id]: "" };
      answerOpen = { ...answerOpen, [t.task_id]: false };
    } catch (e) {
      errors = { ...errors, [t.task_id]: e instanceof Error ? e.message : String(e) };
    } finally {
      sending = null;
    }
  }
  async function sendMessage(t: TeamTaskItem, text: string) {
    if (!onMessage || !text.trim() || sending) return;
    if (text.trim().length > MESSAGE_MAX) {
      replyErrors = { ...replyErrors, [t.task_id]: `Message is longer than ${MESSAGE_MAX} characters` };
      return;
    }
    sending = t.task_id;
    replyErrors = { ...replyErrors, [t.task_id]: "" };
    try {
      await onMessage(t.task_id, text.trim());
      replyDrafts = { ...replyDrafts, [t.task_id]: "" };
    } catch (e) {
      replyErrors = { ...replyErrors, [t.task_id]: e instanceof Error ? e.message : String(e) };
    } finally {
      sending = null;
    }
  }
  async function cancel(t: TeamTaskItem) {
    confirming = null;
    if (!onCancel) return;
    errors = { ...errors, [t.task_id]: "" };
    try {
      await onCancel(t.task_id);
    } catch (e) {
      errors = { ...errors, [t.task_id]: e instanceof Error ? e.message : String(e) };
    }
  }
</script>

{#if folded}
  <!-- grouped §3: the finished turn as one full-width row; a click opens
       the whole block. -->
  <button
    type="button"
    data-testid="delegation-folded"
    data-task-ids={tasks.map((t) => t.task_id).join(" ")}
    aria-expanded={false}
    onclick={async () => {
      // The row leaves the DOM; keep keyboard focus on the block's toggle.
      unfolded = true;
      await tick();
      collapseBtn?.focus();
    }}
    class="flex w-full items-center gap-2 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-[7px] text-left text-xs hover:bg-white-200 dark:hover:bg-navy-700 focus:outline-none focus-visible:ring-1 focus-visible:ring-green-500"
  >
    <TeamIcon name="check" size={14} class="text-green-600 dark:text-green-400" />
    <span data-testid="fold-avatars" aria-hidden="true" class="flex shrink-0 items-center">
      {#each foldAvatars(tasks) as a, i (a.to_agent_id || a.to_handle || a.to_name)}
        <span data-testid="fold-avatar" class={"inline-flex " + (i ? "-ml-[5px]" : "")}><AgentAvatar kind={agents[a.to_handle]?.kind} image={agents[a.to_handle]?.image} shape={agents[a.to_handle]?.shape} expression={agents[a.to_handle]?.expression} color={agents[a.to_handle]?.color} size={18} /></span>
      {/each}
    </span>
    <span data-testid="fold-title" class="shrink-0 whitespace-nowrap text-xs font-medium text-black-800 dark:text-black-600">{settled.head}</span>
    <span data-testid="fold-summary" class="min-w-0 flex-1 truncate text-[11.5px] text-black-700 dark:text-black-600">·
      {#if settled.allReplied}<b class="font-semibold text-green-600 dark:text-green-400">all replied</b>{:else}{#each settled.parts as p, i}{i ? " · " : ""}<span data-status={p.status} class={PART_TONE[p.status]}>{p.label}</span>{/each}{/if}{settled.messages ? ` · ${settled.messages}` : ""}{settled.duration ? ` · ${settled.duration}` : ""}</span>
    <TeamIcon name="chevron" class="-rotate-90 text-black-700 dark:text-black-600" />
  </button>
{:else}
  <div data-testid="delegation-block" data-task-ids={tasks.map((t) => t.task_id).join(" ")} class="flex flex-col overflow-hidden rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 text-xs">
    <div class="flex items-center gap-2 px-3 py-2">
      <TeamIcon name="tasks" size={14} class="text-green-600 dark:text-green-400" />
      <span class="shrink-0 whitespace-nowrap text-[12.5px] font-semibold text-black-900 dark:text-white-100">{blockHead(tasks)}</span>
      <span data-testid="delegation-summary" class="min-w-0 flex-1 truncate text-[11.5px] text-black-700">{#each parts as p, i}{i ? " · " : "· "}<span data-status={p.status} class={PART_TONE[p.status]}>{p.label}</span>{/each}</span>
      <SmBtn
        class="shrink-0"
        data-testid="delegation-show-all"
        aria-expanded={showAll && !collapsed}
        onclick={() => {
          // Collapsed, it always opens everything (it reads Show all then).
          if (collapsed) { collapsed = false; showAll = true; return; }
          showAll = !showAll;
          if (allSettled(tasks) && !showAll) unfolded = false;
        }}
      >{showAll && !collapsed ? "Hide" : "Show all"}</SmBtn>
      <button
        type="button"
        bind:this={collapseBtn}
        data-testid="delegation-collapse"
        aria-label={collapsed ? "Expand" : "Collapse"}
        aria-expanded={!collapsed}
        onclick={() => { collapsed = !collapsed; }}
        class="-m-1.5 inline-flex h-6 w-6 shrink-0 items-center justify-center rounded text-black-700 hover:text-black-900 focus:outline-none focus-visible:ring-1 focus-visible:ring-green-500 dark:hover:text-white-100"
      ><TeamIcon name="chevron" class={collapsed ? "-rotate-90" : ""} /></button>
    </div>

    <div class="flex flex-wrap gap-1.5 px-3 pb-2.5">
      {#each tasks as t (t.task_id)}
        {@const s = taskStatus(t)}
        {@const open = chipOpen(t)}
        {@const el = chipElapsed(t, now)}
        <span class="inline-flex items-center gap-1">
          <button
            type="button"
            data-testid="delegation-chip"
            data-status={s}
            data-open={open}
            aria-expanded={open}
            data-origin={t.origin ?? "agent"}
            onclick={() => {
              // A click on a collapsed block opens it on this chip; otherwise it toggles.
              if (collapsed) { collapsed = false; selected = t.task_id; }
              else selected = selected === t.task_id ? null : t.task_id;
            }}
            class={["inline-flex items-center gap-1.5 whitespace-nowrap rounded-full border py-[3px] pl-1 pr-2.5 text-[11.5px] leading-4 text-black-800 dark:text-black-600 transition-colors",
              open ? "border-green-500 ring-1 ring-green-500" : s === "needs_you" ? "border-amber-500/[.45] dark:border-[rgba(251,191,36,.40)]" : "border-white-300 dark:border-navy-600",
              s === "needs_you" ? "bg-[rgba(251,191,36,.12)] dark:bg-[rgba(251,191,36,.10)]" : "bg-white-200 dark:bg-navy-700 hover:bg-white-300 dark:hover:bg-navy-600"]}
          >
            <AvatarActivity activity={taskActivity(s)} size={18}><AgentAvatar kind={agents[t.to_handle]?.kind} image={agents[t.to_handle]?.image} shape={agents[t.to_handle]?.shape} expression={agents[t.to_handle]?.expression} color={agents[t.to_handle]?.color} size={18} /></AvatarActivity>
            {#if isUserTask(t)}<span data-testid="user-task-marker" class="rounded bg-link-400/10 px-1 text-[10px] font-medium text-link-400">{USER_TASK_MARKER}</span>{/if}
            <span class="font-semibold text-black-900 dark:text-white-100">{nameOf(t)}</span>
            {#if s === "working"}
              <TeamIcon name="spin" size={10} spin class="text-green-600 dark:text-green-400" />
            {:else if s === "replied"}
              <TeamIcon name="check" size={10} class="text-green-600 dark:text-green-400" />
            {/if}
            <span data-testid="chip-status" class={"text-[10.5px] " + LABEL_TONE[s]}>{chipLabel(t, captainName)}{s === "answering" ? "…" : ""}</span>
            {#if el}<span data-testid="chip-elapsed" class="font-mono text-[10px] text-black-700">{el}</span>{/if}
            {#if s !== "needs_you"}
              <TeamIcon name="chevron" size={10} class={"text-black-700 " + (open ? "" : "-rotate-90")} />
            {/if}
          </button>
          {#if s === "answering" && onAnswer && !answerOpen[t.task_id]}
            <button type="button" data-testid="answer-instead" onclick={() => { collapsed = false; answerOpen = { ...answerOpen, [t.task_id]: true }; }} class="text-[10px] text-black-600 dark:text-black-700 underline hover:no-underline">Answer instead</button>
          {/if}
        </span>
      {/each}
    </div>

    {#if !collapsed && onAnswer}
      {#each tasks as t (t.task_id)}
        {#if cardOpen(t)}
          {@const asked = askedClock(t)}
          <div data-testid="needs-you-card" class="border-t border-white-300 bg-[rgba(251,191,36,.12)] dark:border-navy-600 dark:bg-[rgba(251,191,36,.10)]">
            <div class="flex items-center gap-2 px-3 py-[7px] text-[11.5px] text-black-800 dark:text-black-600">
              <AvatarActivity activity={taskActivity("needs_you")} size={20}><AgentAvatar kind={agents[t.to_handle]?.kind} image={agents[t.to_handle]?.image} shape={agents[t.to_handle]?.shape} expression={agents[t.to_handle]?.expression} color={agents[t.to_handle]?.color} size={20} /></AvatarActivity>
              <b class="shrink-0 text-xs font-semibold text-black-900 dark:text-white-100">{nameOf(t)}</b>
              <span data-testid="needs-you-task" class="min-w-0 flex-1 truncate">· {t.title}{asked ? ` · asked ${asked}` : ""}</span>
            </div>
            <div class="flex flex-col gap-2 pb-2.5 pl-[38px] pr-3">
              <p data-testid="needs-you-question" class="whitespace-pre-wrap break-words text-[13px] leading-normal text-black-900 dark:text-white-100">{#if t.reply || t.summary}{@html questionHtml(t.reply || t.summary)}{:else}(no question text){/if}</p>
              <AnswerBox
                to={t.to_handle}
                note={isUserTask(t) ? "you asked, so only you answer" : `${captainName} sees it too`}
                value={draftOf(t.task_id)}
                quick={taskQuickReplies(t)}
                sending={sending === t.task_id}
                disabled={sending !== null && sending !== t.task_id}
                onInput={(v) => { drafts = { ...drafts, [t.task_id]: v }; setDraft(t.task_id, v); }}
                onSend={(text) => send(t, text)}
              />
              {#if errors[t.task_id]}<span data-testid="answer-error" class="text-[11px] text-red-600 dark:text-red-400">{errors[t.task_id]}</span>{/if}
            </div>
          </div>
        {/if}
      {/each}
    {/if}

    {#each tasks as t (t.task_id)}
      {#if chipOpen(t)}
        {@const s = taskStatus(t)}
        <!-- grouped 2b: the open chip's short thread, a soft section of its own. -->
        <div data-testid="delegation-preview" class="border-t border-white-300 bg-white-200 dark:border-navy-600 dark:bg-navy-700">
          <div class="flex items-center gap-2 px-3 py-[7px] text-[11.5px] text-black-700">
            <AvatarActivity activity={taskActivity(s)} size={20}><AgentAvatar kind={agents[t.to_handle]?.kind} image={agents[t.to_handle]?.image} shape={agents[t.to_handle]?.shape} expression={agents[t.to_handle]?.expression} color={agents[t.to_handle]?.color} size={20} /></AvatarActivity>
            <b class="shrink-0 text-xs font-semibold text-black-900 dark:text-white-100">{userTaskRoute(t) ?? nameOf(t)}</b>
            <span data-testid="delegation-preview-task" class="min-w-0 flex-1 truncate">· {t.title}{t.message_count ? ` · ${t.message_count} message${t.message_count === 1 ? "" : "s"}` : ""}</span>
            {#if s === "needs_you" || s === "answering"}
              <!-- A question: the reply is the answer (the card above). -->
              {#if onAnswer}
                <SmBtn icon="reply" data-testid="delegation-reply" class="shrink-0" onclick={() => { answerOpen = { ...answerOpen, [t.task_id]: true }; }}>Reply in thread</SmBtn>
              {/if}
            {:else if onMessage}
              <!-- Any other task: a message that joins it (the composer below). -->
              <SmBtn icon="reply" data-testid="delegation-reply" class="shrink-0" onclick={() => { replyOpen = { ...replyOpen, [t.task_id]: !replyOpen[t.task_id] }; }}>Reply in thread</SmBtn>
            {/if}
            {#if onOpenChat && t.chat_id}
              <SmBtn icon="open" class="shrink-0" onclick={() => onOpenChat?.(t)}>Open chat</SmBtn>
            {/if}
            <button
              type="button"
              data-testid="delegation-preview-collapse"
              onclick={() => {
                if (showAll) showAll = false;
                selected = null;
                if (allSettled(tasks)) unfolded = false;
              }}
              class="shrink-0 px-0.5 py-1 text-[11px] text-black-700 hover:text-black-900 dark:hover:text-white-100"
            >Collapse</button>
            {#if onCancel && isTaskActive(t)}
              <button type="button" data-testid="delegation-cancel" onclick={() => { confirming = confirming === t.task_id ? null : t.task_id; }} class="shrink-0 text-[11px] text-red-600 dark:text-red-400 hover:underline">Cancel</button>
            {/if}
          </div>
          {#if taskProgress(t)}
            <!-- combined 2a .prog: what the teammate's turn is doing now. -->
            <div data-testid="delegation-preview-progress" title={taskProgress(t)} class="flex items-center gap-1.5 px-3 pb-[7px] text-[11px] text-black-700">
              <span aria-hidden="true" class="text-green-600 dark:text-green-400">●</span><span class="min-w-0 flex-1 truncate">{taskProgress(t)}</span>
            </div>
          {/if}
          {#if confirming === t.task_id && onCancel}
            <div class="px-3 pb-2"><CancelConfirm name={nameOf(t)} onConfirm={() => cancel(t)} onKeep={() => { confirming = null; }} /></div>
          {/if}
          {#if errors[t.task_id] && !cardOpen(t)}<span class="block px-3 pb-1 text-[11px] text-red-600 dark:text-red-400">{errors[t.task_id]}</span>{/if}
          {#if loadThread}
            <TaskThread task={t} load={loadThread} {agents} {captainName}>
              {#snippet fallback()}
                <!-- The thread did not load: the last reply still reads. -->
                {#if t.reply || t.summary}
                  <div data-testid="delegation-preview-fallback" class="px-3 pb-1 text-[12.5px] leading-[1.5]"><span class="whitespace-pre-wrap break-words text-black-900 dark:text-white-100 line-clamp-4"><span class="font-medium">{nameOf(t)}:</span> {t.reply || t.summary}</span></div>
                {/if}
              {/snippet}
            </TaskThread>
          {:else}
            <!-- No thread to read (a test, or a host without it): the reply alone. -->
            <div class="px-3 pb-2 text-[12.5px] leading-[1.5]">
              {#if t.reply || t.summary}
                <span class="whitespace-pre-wrap break-words text-black-900 dark:text-white-100 line-clamp-4"><span class="font-medium">{nameOf(t)}:</span> {t.reply || t.summary}</span>
              {:else}
                <span class="italic text-black-600 dark:text-black-700">No reply yet.</span>
              {/if}
            </div>
          {/if}
          {#if onMessage && replyOpen[t.task_id] && s !== "needs_you" && s !== "answering"}
            <div data-testid="reply-composer" class="flex flex-col gap-1 px-3 pb-2.5">
              <AnswerBox
                to={t.to_handle}
                note={isTaskActive(t) ? "joins this task" : "joins this ended task"}
                tone="ok"
                placeholder="Write a message…"
                label={"Message @" + t.to_handle}
                maxlength={MESSAGE_MAX}
                value={replyDrafts[t.task_id] ?? ""}
                sending={sending === t.task_id}
                disabled={sending !== null && sending !== t.task_id}
                onInput={(v) => { replyDrafts = { ...replyDrafts, [t.task_id]: v }; }}
                onSend={(text) => sendMessage(t, text)}
              />
              <div class="flex items-start gap-2">
                <span data-testid="reply-error" class="min-w-0 flex-1 text-[11px] text-red-600 dark:text-red-400">{replyErrors[t.task_id] ?? ""}</span>
                <span data-testid="reply-count" class={["shrink-0 text-[10.5px] tabular-nums", (replyDrafts[t.task_id] ?? "").length >= MESSAGE_MAX ? "text-red-600 dark:text-red-400" : "text-black-700"]}>{(replyDrafts[t.task_id] ?? "").length} / {MESSAGE_MAX}</span>
              </div>
            </div>
          {/if}
        </div>
      {/if}
    {/each}
  </div>
{/if}
