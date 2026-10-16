<script lang="ts">
  /* The Tasks rail (mockup §4): every Team task this chat sent, grouped
     Needs input / Working / Done today / Earlier (folded) / Failed ·
     Canceled. A row reads name (+ status tag once it ended), the time on
     the right, the title, then the question in amber or, while it works,
     the live progress line in green; needs input offers Answer / Jump,
     working Thread · N / Cancel / Jump (Thread opens the task's
     TaskThread under the row), all on one line. Answer opens the shared
     AnswerBox, Cancel asks first (CancelConfirm), and the raw payload
     stays one click away under each row, beside the quiet links (a
     question's Cancel, Open chat ↗ once the task has a chat, an ended
     row's Jump). A working teammate's avatar orbits, one waiting on the
     person pulses (AvatarActivity). A failed
     row offers Retry and a declined one Reassign (a menu of this chat's
     other teammates) at the right of its first line; once sent again the
     row links to the new task ("Retried → Name"). */
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import AvatarActivity from "./AvatarActivity.svelte";
  import { taskActivity } from "../../avatarActivity.js";
  import type { TeamTaskItem, TeamThreadEntry } from "../../types/agents.js";
  import { chipElapsed, endedDay, groupTasks, isTaskActive, isUserTask, statusLabel, taskProgress, taskQuickReplies, taskStatus, userTaskRoute, type TaskStatus } from "../../delegations.js";
  import { questionHtml } from "../../questionText.js";
  import { clearDraft, getDraft, setDraft } from "../../answerDrafts.js";
  import AnswerBox from "./AnswerBox.svelte";
  import CancelConfirm from "./CancelConfirm.svelte";
  import SmBtn from "./SmBtn.svelte";
  import TaskThread from "./TaskThread.svelte";
  import TeamIcon from "./TeamIcon.svelte";

  type ReassignTarget = { handle: string; name: string; shared?: boolean; refused?: boolean };
  type Agent = { name: string; kind?: string; shape?: string; color?: string; expression?: string; image?: import("@wick-fe/common-avatar").AvatarImageView | null };
  type Props = {
    tasks: TeamTaskItem[];
    agents?: Record<string, Agent>;
    /** The agent this chat belongs to, who sent the tasks. */
    senderName?: string;
    /** The person's name for their own rows in a task's thread. */
    userName?: string;
    onAnswer?: (taskId: string, text: string) => Promise<void>;
    onCancel?: (taskId: string) => Promise<void>;
    /** Sends a failed task again to the same teammate (Retry). */
    onRetry?: (taskId: string) => Promise<void>;
    /** Sends a declined task to another teammate (Reassign). */
    onReassign?: (taskId: string, to: string) => Promise<void>;
    /** Who Reassign offers: this chat's teammates (the task's own is left out). */
    teammates?: ReassignTarget[];
    /** Reads who a task can go to, as the server will accept it (shared
        agents included, refusing ones marked); wins over teammates. */
    loadTargets?: (taskId: string) => Promise<ReassignTarget[]>;
    onJump?: (t: TeamTaskItem) => void;
    onOpenChat?: (t: TeamTaskItem) => void;
    /** Reads a task's thread; "Thread · N" opens it under the row. */
    loadThread?: (taskId: string) => Promise<TeamThreadEntry[]>;
    /** Draw the panel's own "Tasks · this chat · N" header; off where the
        host already titles the panel (the mobile sheet). */
    header?: boolean;
  };
  let { tasks, agents = {}, senderName = "Captain", userName, onAnswer, onCancel, onRetry, onReassign, teammates = [], loadTargets, onJump, onOpenChat, loadThread, header = true }: Props = $props();

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
  // Retry / Reassign: the task being sent again, the open menu, and an
  // error under the row it belongs to.
  let replacing = $state<string | null>(null);
  let menuFor = $state<string | null>(null);
  let rowError = $state<Record<string, string>>({});
  // Per declined task, the server's list (null while it loads), read once
  // as the row shows up.
  let loaded = $state<Record<string, ReassignTarget[] | null>>({});
  const requested = new Set<string>();
  $effect(() => {
    if (!loadTargets || !onReassign) return;
    for (const t of tasks) {
      const id = t.task_id;
      if (taskStatus(t) !== "declined" || t.replaced_by || requested.has(id)) continue;
      requested.add(id);
      loaded = { ...loaded, [id]: null };
      loadTargets(id)
        .then((list) => { loaded = { ...loaded, [id]: list }; })
        .catch((e) => {
          loaded = { ...loaded, [id]: [] };
          rowError = { ...rowError, [id]: "Couldn't load teammates: " + (e instanceof Error ? e.message : String(e)) };
        });
    }
  });
  const targetsOf = (t: TeamTaskItem): ReassignTarget[] | null =>
    loadTargets ? (loaded[t.task_id] ?? null) : teammates.filter((m) => m.handle !== t.to_handle);
  // The menu opens upward when the list has no room below the button.
  let menuUp = $state(false);
  function toggleMenu(id: string, btn: HTMLElement, count: number) {
    if (menuFor === id) { menuFor = null; return; }
    const r = btn.getBoundingClientRect();
    const box = btn.closest("[data-tasks-scroll]")?.getBoundingClientRect() ?? { top: 0, bottom: window.innerHeight };
    const below = box.bottom - r.bottom, above = r.top - box.top;
    menuUp = below < Math.min(224, 34 + 32 * count) && above > below;
    menuFor = id;
  }
  const replacedName = (t: TeamTaskItem) => agents[t.replaced_to_handle ?? ""]?.name || t.replaced_to_name || "@" + t.replaced_to_handle;
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
  async function replace(id: string, f: () => Promise<void>) {
    if (replacing) return;
    menuFor = null;
    replacing = id;
    rowError = { ...rowError, [id]: "" };
    try {
      await f();
    } catch (e) {
      rowError = { ...rowError, [id]: e instanceof Error ? e.message : String(e) };
    } finally {
      replacing = null;
    }
  }
  // The new task's row, when it is in the list: scroll to it.
  function showTask(id: string) {
    const row = [...document.querySelectorAll<HTMLElement>('[data-testid="tasks-row"]')].find((r) => r.dataset.taskId === id);
    row?.scrollIntoView({ block: "center", behavior: "smooth" });
  }
  async function cancel(id: string) {
    confirming = null;
    if ((await act(() => onCancel!(id))) && answering === id) answering = null;
  }
</script>

<svelte:window
  onkeydown={(e) => { if (e.key === "Escape") menuFor = null; }}
  onclick={(e) => { if (menuFor && !(e.target as Element | null)?.closest?.("[data-reassign]")) menuFor = null; }}
/>

<div data-testid="tasks-panel" class="flex min-h-0 flex-1 flex-col overflow-hidden">
  {#if header}
  <div class="flex shrink-0 items-center gap-2 border-b border-white-300 dark:border-navy-600 px-4 py-3">
    <h2 class="flex-1 text-sm font-semibold text-black-900 dark:text-white-100">Tasks</h2>
    <span data-testid="tasks-panel-count" class="text-[11px] text-black-700">this chat · {tasks.length}</span>
  </div>
  {/if}
  <div data-tasks-scroll class="min-h-0 flex-1 overflow-y-auto pb-2">
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
              data-task-id={t.task_id}
              class={["mb-1.5 flex items-start gap-2.5 rounded-lg border bg-white-100 dark:bg-navy-800 px-2.5 py-2",
                s === "needs_you" ? "border-amber-500/[.45] dark:border-[rgba(251,191,36,.40)]" : "border-white-300 dark:border-navy-600"]}
            >
              <span class="shrink-0"><AvatarActivity activity={taskActivity(s)} size={26}><AgentAvatar kind={agents[t.to_handle]?.kind} image={agents[t.to_handle]?.image} shape={agents[t.to_handle]?.shape} expression={agents[t.to_handle]?.expression} color={agents[t.to_handle]?.color} size={26} /></AvatarActivity></span>
              <div class="min-w-0 flex-1">
                <div class="flex items-baseline gap-1.5 text-[12.5px]">
                  <span data-testid="tasks-panel-name" class="truncate font-semibold text-black-900 dark:text-white-100">{userTaskRoute(t) ?? nameOf(t)}</span>
                  {#if !active}<span data-testid="tasks-row-tag" class={["ml-1.5 shrink-0 text-[10px] font-medium uppercase tracking-[.05em]", TAG_TONE[s]]}>{s}</span>{/if}
                  <span class="flex-1"></span>
                  {#if t.replaced_by}
                    <!-- Sent again: the link to the task that took over. -->
                    <button type="button" data-testid="tasks-row-replaced" onclick={() => showTask(t.replaced_by!)} title="Show the new task" class="shrink-0 truncate text-[10.5px] text-black-700 hover:text-green-600 dark:hover:text-green-400">{t.replaced_how === "reassign" ? "Reassigned" : "Retried"} → {replacedName(t)}</button>
                  {:else if s === "failed" && onRetry}
                    <SmBtn icon="retry" data-testid="tasks-row-retry" aria-label={"Retry task for " + nameOf(t)} disabled={replacing !== null} onclick={() => replace(t.task_id, () => onRetry!(t.task_id))}>{replacing === t.task_id ? "Retrying…" : "Retry"}</SmBtn>
                  {:else if s === "declined" && onReassign}
                    {@const targets = targetsOf(t)}
                    {@const open = targets?.filter((m) => !m.refused) ?? []}
                    <span data-reassign class="relative shrink-0">
                      <SmBtn
                        icon="recover"
                        data-testid="tasks-row-reassign"
                        aria-label={"Reassign task for " + nameOf(t)}
                        aria-haspopup="menu"
                        aria-expanded={menuFor === t.task_id}
                        title={targets === null ? "Loading teammates…" : open.length ? undefined : targets.length ? `No teammate here takes tasks from ${senderName}` : "No other teammate in this chat"}
                        disabled={open.length === 0 || replacing !== null}
                        onclick={(e: MouseEvent) => toggleMenu(t.task_id, e.currentTarget as HTMLElement, targets?.length ?? 0)}
                      >{replacing === t.task_id ? "Reassigning…" : "Reassign"}</SmBtn>
                      {#if menuFor === t.task_id}
                        <div role="menu" data-testid="tasks-reassign-menu" aria-label="Reassign to" data-placement={menuUp ? "up" : "down"} class={["absolute right-0 z-20 max-h-56 min-w-[180px] overflow-y-auto rounded-lg border border-white-300 bg-white-100 py-1 shadow-lg dark:border-navy-600 dark:bg-navy-800", menuUp ? "bottom-full mb-1" : "top-full mt-1"]}>
                          <div class="px-2.5 pb-1 pt-0.5 text-[10px] font-semibold uppercase tracking-[.06em] text-black-700">Reassign to</div>
                          {#each targets ?? [] as m (m.handle)}
                            <button
                              type="button"
                              role="menuitem"
                              data-testid="tasks-reassign-option"
                              disabled={m.refused}
                              title={m.refused ? `Doesn't take tasks from ${senderName}` : m.shared ? "Shared with you" : undefined}
                              onclick={() => replace(t.task_id, () => onReassign!(t.task_id, m.handle))}
                              class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-[11.5px] text-black-900 hover:bg-white-200 disabled:cursor-not-allowed disabled:opacity-50 dark:text-white-100 dark:hover:bg-navy-700"
                            >
                              <AgentAvatar kind={agents[m.handle]?.kind} image={agents[m.handle]?.image} shape={agents[m.handle]?.shape} expression={agents[m.handle]?.expression} color={agents[m.handle]?.color} size={18} />
                              <span class="truncate">{agents[m.handle]?.name || m.name || "@" + m.handle}</span>
                              <span class="ml-auto font-mono text-[10px] text-black-700">@{m.handle}</span>
                            </button>
                          {/each}
                        </div>
                      {/if}
                    </span>
                  {:else}
                    <span data-testid="tasks-row-time" class="shrink-0 font-mono text-[10px] text-black-700">{g.collapsed ? endedDay(t, now) : chipElapsed(t, now)}</span>
                  {/if}
                </div>
                {#if rowError[t.task_id]}<p data-testid="tasks-row-error" class="mt-0.5 text-[11px] text-red-600 dark:text-red-400">{rowError[t.task_id]}</p>{/if}
                <div class="mt-px break-words text-[11.5px] text-black-700">{(s === "failed" || s === "declined" || s === "canceled") && t.summary ? t.summary : t.title}</div>
                {#if s === "needs_you" && (t.reply || t.summary)}
                  <!-- The question the quick answers come from (reply, as in the block and the tray), three lines at most. -->
                  <div data-testid="tasks-row-question" title={t.reply || t.summary} class="mt-1 line-clamp-3 whitespace-pre-line break-words text-[11.5px] text-amber-700 dark:text-amber-300">{@html questionHtml(t.reply || t.summary)}</div>
                {:else if s === "answering"}
                  <div class="mt-1 text-[11.5px] text-black-700">{statusLabel(s, senderName)}…</div>
                {:else if taskProgress(t)}
                  <!-- What the teammate's turn is doing now (live, plain text, one line). -->
                  <div data-testid="tasks-row-progress" title={taskProgress(t)} class="mt-1 truncate text-[11.5px] text-green-600 dark:text-green-400">{taskProgress(t)}</div>
                {/if}
                {#if active && ((ask && onAnswer) || (s !== "needs_you" && onCancel) || onJump || (s === "working" && loadThread))}
                  <div data-testid="tasks-row-actions" class="mt-1.5 flex flex-wrap items-center gap-1">
                    {#if s === "working" && loadThread}<SmBtn icon="msg" data-testid="tasks-row-thread" aria-expanded={!!threadOpen[t.task_id]} aria-label={nameOf(t) + "'s thread"} onclick={() => { threadOpen = { ...threadOpen, [t.task_id]: !threadOpen[t.task_id] }; }}>Thread{t.message_count ? ` · ${t.message_count}` : ""}</SmBtn>{/if}
                    {#if ask && onAnswer}<SmBtn variant="warn" aria-label={"Answer " + nameOf(t)} onclick={() => { answering = answering === t.task_id ? null : t.task_id; }}>Answer</SmBtn>{/if}
                    {#if s !== "needs_you" && onCancel}
                      <SmBtn variant="danger" aria-label={"Cancel task for " + nameOf(t)} onclick={() => { confirming = confirming === t.task_id ? null : t.task_id; }}>Cancel</SmBtn>
                    {/if}
                    {#if onJump}<SmBtn icon="jump" aria-label={"Jump to " + nameOf(t) + "'s task"} onclick={() => onJump?.(t)}>Jump</SmBtn>{/if}
                  </div>
                {/if}
                {#if s === "working" && loadThread && threadOpen[t.task_id]}
                  <div class="-ml-1 mt-1.5 rounded-md border border-white-300 bg-white-200 pt-1 dark:border-navy-600 dark:bg-navy-700"><TaskThread task={t} load={loadThread} {agents} captainName={senderName} {userName} /></div>
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
                      quick={taskQuickReplies(t)}
                      sending={sending === t.task_id}
                      disabled={sending !== null && sending !== t.task_id}
                      onInput={(v) => { drafts = { ...drafts, [t.task_id]: v }; setDraft(t.task_id, v); }}
                      onSend={(text) => answer(t.task_id, text)}
                    />
                  </div>
                {/if}
                <!-- Raw payload below the fold, the quiet links beside it: a question's Cancel, an ended row's Jump (an active row has it above), Open chat. -->
                <div data-testid="tasks-row-footer" class="mt-1 flex items-center gap-2 text-[10.5px] text-black-700">
                  <button type="button" aria-expanded={!!rawOpen[t.task_id]} onclick={() => { rawOpen = { ...rawOpen, [t.task_id]: !rawOpen[t.task_id] }; }} class="select-none hover:text-black-900 dark:hover:text-white-100">Raw payload</button>
                  {#if s === "needs_you" && onCancel}<button type="button" aria-label={"Cancel task for " + nameOf(t)} onclick={() => { confirming = confirming === t.task_id ? null : t.task_id; }} class="hover:text-red-600 dark:hover:text-red-400">Cancel</button>{/if}
                  {#if !active && onJump}<button type="button" aria-label={"Jump to " + nameOf(t) + "'s task"} onclick={() => onJump?.(t)} class="hover:text-green-600 dark:hover:text-green-400">Jump</button>{/if}
                  {#if onOpenChat && t.chat_id}<button type="button" aria-label={"Open " + nameOf(t) + "'s chat"} onclick={() => onOpenChat?.(t)} class="hover:text-green-600 dark:hover:text-green-400">Open chat ↗</button>{/if}
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
