<script lang="ts">
  /* One assistant turn's team_message calls, as one block instead of a
     raw ToolCard per call: "Delegated to N teammates", a status summary
     and a chip per teammate. A question that waits for the person opens
     its answer card inside the block; one the agent is handling reads
     "Captain is answering…" with an "Answer instead" link. Once every task
     ended the block folds to a single line. */
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import type { TeamTaskItem } from "../../types/agents.js";
  import { allSettled, INTERRUPTED_LABEL, isTaskActive, quickReplies, settledLine, statusSummary, taskStatus, type TaskStatus } from "../../delegations.js";
  import { clearDraft, getDraft, setDraft } from "../../answerDrafts.js";
  import CancelConfirm from "./CancelConfirm.svelte";

  type Agent = { name: string; kind?: string; shape?: string; color?: string; expression?: string };
  type Props = {
    tasks: TeamTaskItem[];
    agents?: Record<string, Agent>;
    captainName?: string;
    onAnswer?: (taskId: string, text: string) => Promise<void>;
    onCancel?: (taskId: string) => Promise<void>;
    onOpenChat?: (t: TeamTaskItem) => void;
  };
  let { tasks, agents = {}, captainName = "Captain", onAnswer, onCancel, onOpenChat }: Props = $props();

  let showAll = $state(false);
  let unfolded = $state(false);
  let selected = $state<string | null>(null);
  // "Answer instead" on a task the agent is handling opens its card too.
  let answerOpen = $state<Record<string, boolean>>({});
  // Per task, seeded from answerDrafts so a remount keeps what was typed.
  let drafts = $state<Record<string, string>>({});
  const draftOf = (id: string) => drafts[id] ?? getDraft(id);
  let confirming = $state<string | null>(null);
  let sending = $state<string | null>(null);
  let errors = $state<Record<string, string>>({});

  const folded = $derived(allSettled(tasks) && !unfolded);
  const n = $derived(tasks.length);

  const DOT: Record<TaskStatus, string> = {
    needs_you: "bg-amber-500",
    answering: "bg-black-400 dark:bg-black-600",
    working: "bg-green-500 animate-pulse motion-reduce:animate-none",
    replied: "bg-green-500",
    failed: "bg-red-500",
    canceled: "bg-black-400 dark:bg-black-600",
  };
  function chipLabel(t: TeamTaskItem, s: TaskStatus): string {
    if (t.interrupted) return INTERRUPTED_LABEL;
    switch (s) {
      case "needs_you": return "Needs you";
      case "answering": return `${captainName} is answering…`;
      case "working": return "working";
      case "replied": return "replied";
      case "failed": return "failed";
    }
    return "canceled";
  }
  function nameOf(t: TeamTaskItem): string {
    return agents[t.to_handle]?.name || t.to_name || "@" + t.to_handle;
  }
  function cardOpen(t: TeamTaskItem): boolean {
    const s = taskStatus(t);
    return s === "needs_you" || (s === "answering" && !!answerOpen[t.task_id]);
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
  <button
    type="button"
    data-testid="delegation-folded"
    data-task-ids={tasks.map((t) => t.task_id).join(" ")}
    onclick={() => { unfolded = true; }}
    class="self-start inline-flex items-center gap-1.5 rounded-full border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2.5 py-1 text-[11px] text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700"
  >
    <span class="text-green-600 dark:text-green-400">✓</span>
    <span>{settledLine(tasks)}</span>
  </button>
{:else}
  <div data-testid="delegation-block" data-task-ids={tasks.map((t) => t.task_id).join(" ")} class="flex flex-col gap-2 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-xs">
    <div class="flex items-center gap-2">
      <span class="font-medium text-black-900 dark:text-white-100">Delegated to {n} teammate{n === 1 ? "" : "s"}</span>
      <span data-testid="delegation-summary" class="min-w-0 truncate text-black-600 dark:text-black-700">{statusSummary(tasks)}</span>
      <button
        type="button"
        onclick={() => { showAll = !showAll; if (allSettled(tasks) && !showAll) unfolded = false; }}
        class="ml-auto shrink-0 text-[11px] text-green-600 dark:text-green-400 hover:underline"
      >{showAll ? "Hide" : "Show all"}</button>
    </div>

    <div class="flex flex-wrap gap-1.5">
      {#each tasks as t (t.task_id)}
        {@const s = taskStatus(t)}
        <span class="inline-flex items-center gap-1">
          <button
            type="button"
            data-testid="delegation-chip"
            data-status={s}
            onclick={() => { selected = selected === t.task_id ? null : t.task_id; }}
            class={"inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] leading-4 transition-colors " +
              (s === "needs_you"
                ? "border-amber-500/50 bg-amber-500/10 text-amber-800 dark:text-amber-300"
                : "border-white-300 dark:border-navy-600 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700") +
              (selected === t.task_id ? " ring-1 ring-green-500" : "")}
          >
            <AgentAvatar kind={agents[t.to_handle]?.kind} shape={agents[t.to_handle]?.shape} expression={agents[t.to_handle]?.expression} color={agents[t.to_handle]?.color} size={16} />
            <span class="font-medium">{nameOf(t)}</span>
            <span class={"h-1.5 w-1.5 shrink-0 rounded-full " + DOT[s]}></span>
            <span class="opacity-80">{chipLabel(t, s)}</span>
            {#if t.age}<span class="opacity-60">· {t.age}</span>{/if}
          </button>
          {#if s === "answering" && onAnswer && !answerOpen[t.task_id]}
            <button type="button" data-testid="answer-instead" onclick={() => { answerOpen = { ...answerOpen, [t.task_id]: true }; }} class="text-[10px] text-black-600 dark:text-black-700 underline hover:no-underline">Answer instead</button>
          {/if}
        </span>
      {/each}
    </div>

    {#each tasks as t (t.task_id)}
      {#if cardOpen(t) && onAnswer}
        {@const opts = quickReplies(t.reply || t.summary || "")}
        <div data-testid="needs-you-card" class="flex flex-col gap-1.5 rounded-lg border border-amber-500/40 bg-amber-500/5 px-3 py-2">
          <span class="text-[11px] font-medium text-amber-800 dark:text-amber-300">{nameOf(t)} asks</span>
          <p class="whitespace-pre-wrap break-words text-black-900 dark:text-white-100">{t.reply || t.summary || "(no question text)"}</p>
          {#if opts.length > 0}
            <div class="flex flex-wrap gap-1">
              {#each opts as o}
                <button type="button" disabled={sending !== null} onclick={() => send(t, o)} class="rounded-full border border-amber-500/50 px-2 py-0.5 text-[11px] text-amber-800 dark:text-amber-300 hover:bg-amber-500/10 disabled:opacity-50">{o}</button>
              {/each}
            </div>
          {/if}
          <form class="flex items-center gap-1.5" onsubmit={(e) => { e.preventDefault(); send(t, draftOf(t.task_id)); }}>
            <input
              type="text"
              aria-label={"Answer @" + t.to_handle}
              placeholder="Type your answer…"
              value={draftOf(t.task_id)}
              oninput={(e) => { const v = (e.currentTarget as HTMLInputElement).value; drafts = { ...drafts, [t.task_id]: v }; setDraft(t.task_id, v); }}
              class="min-w-0 flex-1 rounded-md border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-900 px-2 py-1 text-xs text-black-900 dark:text-white-100"
            />
            <button type="submit" disabled={sending !== null || !draftOf(t.task_id).trim()} class="rounded-md bg-green-500 px-2.5 py-1 text-[11px] font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">{sending === t.task_id ? "Sending…" : "Send"}</button>
          </form>
          <span class="text-[10px] text-black-600 dark:text-black-700">Answer goes to @{t.to_handle} · {captainName} sees it too</span>
          {#if errors[t.task_id]}<span data-testid="answer-error" class="text-[11px] text-red-600 dark:text-red-400">{errors[t.task_id]}</span>{/if}
        </div>
      {/if}
    {/each}

    {#each tasks as t (t.task_id)}
      {#if showAll || selected === t.task_id}
        <div data-testid="delegation-preview" class="flex flex-col gap-1 rounded-lg bg-white-200 dark:bg-navy-900 px-3 py-2">
          <span class="text-[11px] text-black-600 dark:text-black-700"><span class="font-medium text-black-800 dark:text-black-500">{captainName} → @{t.to_handle}</span> · {t.title}</span>
          {#if t.reply || t.summary}
            <span class="whitespace-pre-wrap break-words text-black-900 dark:text-white-100 line-clamp-4"><span class="font-medium">{nameOf(t)}:</span> {t.reply || t.summary}</span>
          {:else}
            <span class="italic text-black-600 dark:text-black-700">No reply yet.</span>
          {/if}
          <span class="flex items-center gap-3 text-[11px]">
            {#if onOpenChat}
              <button type="button" onclick={() => onOpenChat?.(t)} class="text-green-600 dark:text-green-400 hover:underline">Open chat ↗</button>
            {/if}
            {#if onCancel && isTaskActive(t)}
              <button type="button" data-testid="delegation-cancel" onclick={() => { confirming = confirming === t.task_id ? null : t.task_id; }} class="text-red-600 dark:text-red-400 hover:underline">Cancel</button>
            {/if}
          </span>
          {#if confirming === t.task_id && onCancel}
            <CancelConfirm name={nameOf(t)} onConfirm={() => cancel(t)} onKeep={() => { confirming = null; }} />
          {/if}
          {#if errors[t.task_id] && !cardOpen(t)}<span class="text-[11px] text-red-600 dark:text-red-400">{errors[t.task_id]}</span>{/if}
        </div>
      {/if}
    {/each}
  </div>
{/if}
