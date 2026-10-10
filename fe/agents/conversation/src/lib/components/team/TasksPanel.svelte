<script lang="ts">
  /* The Tasks rail (mockup §4): every Team task this chat sent, grouped
     Needs input / Working / Done today / Earlier (folded) / Failed ·
     Canceled. A row reads name (+ status tag once it ended), the time on
     the right, the title, then the question in amber or, while it works, the live
     progress line in green; needs input offers
     Answer / Cancel / Jump, working Thread · N / Cancel / Jump (Thread
     opens the task's TaskThread under the row). Answer opens the shared
     AnswerBox, Cancel asks first (CancelConfirm), and the raw payload
     stays one click away under each row. A working teammate's avatar
     orbits, one waiting on the person pulses (AvatarActivity). */
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import AvatarActivity from "./AvatarActivity.svelte";
  import { taskActivity } from "../../avatarActivity.js";
  import type { TeamTaskItem, TeamThreadEntry } from "../../types/agents.js";
  import { chipElapsed, endedDay, groupTasks, isTaskActive, isUserTask, quickReplies, statusLabel, taskProgress, taskStatus, userTaskRoute, type TaskStatus } from "../../delegations.js";
  import { clearDraft, getDraft, setDraft } from "../../answerDrafts.js";
  import AnswerBox from "./AnswerBox.svelte";
  import CancelConfirm from "./CancelConfirm.svelte";
  import SmBtn from "./SmBtn.svelte";
  import TaskThread from "./TaskThread.svelte";
  import TeamIcon from "./TeamIcon.svelte";

  type Agent = { name: string; kind?: string; shape?: string; color?: string; expression?: string };
  type Props = {
    tasks: TeamTaskItem[];
    agents?: Record<string, Agent>;
    /** The agent this chat belongs to, who sent the tasks. */
    senderName?: string;
    onAnswer?: (taskId: string, text: string) => Promise<void>;
    onCancel?: (taskId: string) => Promise<void>;
    onJump?: (t: TeamTaskItem) => void;
    onOpenChat?: (t: TeamTaskItem) => void;
    /** Reads a task's thread; "Thread · N" opens it under the row. */
    loadThread?: (taskId: string) => Promise<TeamThreadEntry[]>;
    /** Draw the panel's own "Tasks · this chat · N" header; off where the
        host already titles the panel (the mobile sheet). */
    header?: boolean;
  };
  let { tasks, agents = {}, senderName = "Captain", onAnswer, onCancel, onJump, onOpenChat, loadThread, header = true }: Props = $props();

  // The timers tick once a second, only while a task still runs.
  let now = $state(Date.now());
  const ticking = $derived(tasks.some(isTaskActive));
  $effect(() => {
    if (!ticking) return;
    now = Date.now();
    const id = setInterval(() => { now = Date.now(); }, 1000);
    return () => clearInterval(id);
  });
  // Done today / Earlier splits at local midnight, ticking or not.
  $effect(() => {
    const id = setTimeout(() => { now = Date.now(); }, Math.max(1000, new Date(now).setHours(24, 0, 0, 0) - Date.now()));
    return () => clearTimeout(id);
  });

  const groups = $derived(groupTasks(tasks, now));
  let earlierOpen = $state(false);
  let answering = $state<string | null>(null);
  let confirming = $state<string | null>(null);
  let sending = $state<string | null>(null);
  let rawOpen = $state<Record<string, boolean>>({});
  let threadOpen = $state<Record<string, boolean>>({});
  // Per task, seeded from answerDrafts so a remount keeps what was typed.
  let drafts = $state<Record<string, string>>({});
  let error = $state("");
  const draftOf = (id: string) => drafts[id] ?? getDraft(id);
  const nameOf = (t: TeamTaskItem) => agents[t.to_handle]?.name || t.to_name || "@" + t.to_handle;

  // The status tag after the name of a task that ended (mockup .st).
  const TAG_TONE: Partial<Record<TaskStatus, string>> = {
    replied: "text-green-600 dark:text-green-400",
    declined: "text-red-600 dark:text-red-400",
    failed: "text-red-600 dark:text-red-400",
    canceled: "text-black-700",
  };

  async function act(f: () => Promise<void>): Promise<boolean> {
    error = "";
    try {
      await f();
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      return false;
    }
  }
  async function answer(id: string, text: string) {
    if (!text.trim() || sending) return;
    sending = id;
    const ok = await act(() => onAnswer!(id, text.trim()));
    sending = null;
    if (!ok) return;
    clearDraft(id);
    drafts = { ...drafts, [id]: "" };
    if (answering === id) answering = null;
  }
  async function cancel(id: string) {
    confirming = null;
    if ((await act(() => onCancel!(id))) && answering === id) answering = null;
  }
</script>

<div data-testid="tasks-panel" class="flex min-h-0 flex-1 flex-col overflow-hidden">
  {#if header}
  <div class="flex shrink-0 items-center gap-2 border-b border-white-300 dark:border-navy-600 px-4 py-3">
    <h2 class="flex-1 text-sm font-semibold text-black-900 dark:text-white-100">Tasks</h2>
    <span data-testid="tasks-panel-count" class="text-[11px] text-black-700">this chat · {tasks.length}</span>
  </div>
  {/if}
  <div class="min-h-0 flex-1 overflow-y-auto pb-2">
    {#if tasks.length === 0}
      <p class="px-4 py-4 text-xs text-black-600 dark:text-black-700">No Team tasks in this chat yet.</p>
    {/if}
    {#if error}<p class="px-4 pt-2.5 text-[11px] text-red-600 dark:text-red-400">{error}</p>{/if}
    {#each groups as g (g.key)}
      {@const open = !g.collapsed || earlierOpen}
      <section data-testid={"tasks-group-" + g.key} class="px-3 pb-1 pt-2.5">
        {#if g.collapsed}
          <h3 class="mb-1.5">
            <button
              type="button"
              data-testid="tasks-earlier-toggle"
              aria-expanded={earlierOpen}
              aria-controls="tasks-earlier-rows"
              onclick={() => { earlierOpen = !earlierOpen; }}
              class="flex items-center gap-1.5 text-[10.5px] font-semibold uppercase tracking-[.06em] text-black-700 hover:text-black-900 dark:hover:text-white-100"
            ><TeamIcon name="chevron" size={11} class={earlierOpen ? "" : "-rotate-90"} />{g.label} · {g.tasks.length}</button>
          </h3>
        {:else}
          <h3 class={["mb-1.5 text-[10.5px] font-semibold uppercase tracking-[.06em]", g.key === "needs_you" ? "text-amber-700 dark:text-amber-300" : "text-black-700"]}>{g.label} · {g.tasks.length}</h3>
        {/if}
        {#if open}
          <div id={g.collapsed ? "tasks-earlier-rows" : undefined}>
          {#each g.tasks as t (t.task_id)}
            {@const s = taskStatus(t)}
            {@const ask = s === "needs_you" || s === "answering"}
            {@const active = ask || s === "working"}
            <div
              data-testid="tasks-row"
              data-status={s}
              class={["mb-1.5 flex items-start gap-2.5 rounded-lg border bg-white-100 dark:bg-navy-800 px-2.5 py-2",
                s === "needs_you" ? "border-amber-500/[.45] dark:border-[rgba(251,191,36,.40)]" : "border-white-300 dark:border-navy-600"]}
            >
              <span class="shrink-0"><AvatarActivity activity={taskActivity(s)} size={26}><AgentAvatar kind={agents[t.to_handle]?.kind} shape={agents[t.to_handle]?.shape} expression={agents[t.to_handle]?.expression} color={agents[t.to_handle]?.color} size={26} /></AvatarActivity></span>
              <div class="min-w-0 flex-1">
                <div class="flex items-baseline gap-1.5 text-[12.5px]">
                  <span data-testid="tasks-panel-name" class="truncate font-semibold text-black-900 dark:text-white-100">{userTaskRoute(t) ?? nameOf(t)}</span>
                  {#if !active}<span data-testid="tasks-row-tag" class={["ml-1.5 shrink-0 text-[10px] font-medium uppercase tracking-[.05em]", TAG_TONE[s]]}>{s}</span>{/if}
                  <span class="flex-1"></span>
                  <span data-testid="tasks-row-time" class="shrink-0 font-mono text-[10px] text-black-700">{g.collapsed ? endedDay(t, now) : chipElapsed(t, now)}</span>
                </div>
                <div class="mt-px break-words text-[11.5px] text-black-700">{(s === "failed" || s === "declined" || s === "canceled") && t.summary ? t.summary : t.title}</div>
                {#if s === "needs_you" && (t.reply || t.summary)}
                  <!-- The question the quick answers come from (reply, as in the block and the tray), three lines at most. -->
                  <div data-testid="tasks-row-question" title={t.reply || t.summary} class="mt-1 line-clamp-3 whitespace-pre-line break-words text-[11.5px] text-amber-700 dark:text-amber-300">{t.reply || t.summary}</div>
                {:else if s === "answering"}
                  <div class="mt-1 text-[11.5px] text-black-700">{statusLabel(s, senderName)}…</div>
                {:else if taskProgress(t)}
                  <!-- What the teammate's turn is doing now (live, plain text, one line). -->
                  <div data-testid="tasks-row-progress" title={taskProgress(t)} class="mt-1 truncate text-[11.5px] text-green-600 dark:text-green-400">{taskProgress(t)}</div>
                {/if}
                {#if active && (onAnswer || onCancel || onJump || onOpenChat || (s === "working" && loadThread))}
                  <div class="mt-1.5 flex flex-wrap items-center gap-1">
                    {#if s === "working" && loadThread}<SmBtn icon="msg" data-testid="tasks-row-thread" aria-expanded={!!threadOpen[t.task_id]} aria-label={nameOf(t) + "'s thread"} onclick={() => { threadOpen = { ...threadOpen, [t.task_id]: !threadOpen[t.task_id] }; }}>Thread{t.message_count ? ` · ${t.message_count}` : ""}</SmBtn>{/if}
                    {#if ask && onAnswer}<SmBtn variant="warn" aria-label={"Answer " + nameOf(t)} onclick={() => { answering = answering === t.task_id ? null : t.task_id; }}>Answer</SmBtn>{/if}
                    {#if s === "needs_you" && onCancel}
                      <button type="button" aria-label={"Cancel task for " + nameOf(t)} onclick={() => { confirming = confirming === t.task_id ? null : t.task_id; }} class="px-1.5 py-[3px] text-[10.5px] font-medium text-black-700 hover:text-red-600 dark:hover:text-red-400">Cancel</button>
                    {:else if onCancel}
                      <SmBtn variant="danger" aria-label={"Cancel task for " + nameOf(t)} onclick={() => { confirming = confirming === t.task_id ? null : t.task_id; }}>Cancel</SmBtn>
                    {/if}
                    {#if onJump}<SmBtn icon="jump" aria-label={"Jump to " + nameOf(t) + "'s task"} onclick={() => onJump?.(t)}>Jump</SmBtn>{/if}
                    {#if onOpenChat}<SmBtn icon="open" aria-label={"Open " + nameOf(t) + "'s chat"} onclick={() => onOpenChat?.(t)}>Open chat</SmBtn>{/if}
                  </div>
                {/if}
                {#if s === "working" && loadThread && threadOpen[t.task_id]}
                  <div class="-ml-1 mt-1.5 rounded-md border border-white-300 bg-white-200 pt-1 dark:border-navy-600 dark:bg-navy-700"><TaskThread task={t} load={loadThread} {agents} captainName={senderName} /></div>
                {/if}
                {#if active && confirming === t.task_id && onCancel}
                  <div class="mt-1.5"><CancelConfirm name={nameOf(t)} onConfirm={() => cancel(t.task_id)} onKeep={() => { confirming = null; }} /></div>
                {/if}
                {#if ask && answering === t.task_id && onAnswer}
                  <div class="mt-1.5">
                    <AnswerBox
                      to={t.to_handle}
                      note={isUserTask(t) ? "you asked, so only you answer" : `${senderName} sees it too`}
                      value={draftOf(t.task_id)}
                      quick={quickReplies(t.reply || t.summary || "")}
                      sending={sending === t.task_id}
                      disabled={sending !== null && sending !== t.task_id}
                      onInput={(v) => { drafts = { ...drafts, [t.task_id]: v }; setDraft(t.task_id, v); }}
                      onSend={(text) => answer(t.task_id, text)}
                    />
                  </div>
                {/if}
                <!-- Raw payload below the fold; on an ended row the quiet Jump / Open chat sit beside it. -->
                <div class="mt-1 flex items-center gap-2 text-[10.5px] text-black-700">
                  <button type="button" aria-expanded={!!rawOpen[t.task_id]} onclick={() => { rawOpen = { ...rawOpen, [t.task_id]: !rawOpen[t.task_id] }; }} class="select-none hover:text-black-900 dark:hover:text-white-100">Raw payload</button>
                  {#if !active && onJump}<button type="button" aria-label={"Jump to " + nameOf(t) + "'s task"} onclick={() => onJump?.(t)} class="hover:text-green-600 dark:hover:text-green-400">Jump</button>{/if}
                  {#if !active && onOpenChat}<button type="button" aria-label={"Open " + nameOf(t) + "'s chat"} onclick={() => onOpenChat?.(t)} class="hover:text-green-600 dark:hover:text-green-400">Open chat ↗</button>{/if}
                </div>
                {#if rawOpen[t.task_id]}<pre data-testid="tasks-row-raw" class="mt-1 overflow-x-auto whitespace-pre-wrap break-words text-[10px] text-black-700 dark:text-black-600">{JSON.stringify(t, null, 2)}</pre>{/if}
              </div>
            </div>
          {/each}
          </div>
        {/if}
      </section>
    {/each}
  </div>
</div>
