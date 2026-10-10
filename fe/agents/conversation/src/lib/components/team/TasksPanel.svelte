<script lang="ts">
  /* The Tasks rail: every Team task this chat sent, grouped Needs you /
     Working / Done / Failed·Canceled, each row with its actions and the
     raw payload one click away. */
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import type { TeamTaskItem } from "../../types/agents.js";
  import { groupTasks, taskLabel, taskStatus } from "../../delegations.js";
  import { clearDraft, getDraft, setDraft } from "../../answerDrafts.js";
  import CancelConfirm from "./CancelConfirm.svelte";

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
  };
  let { tasks, agents = {}, senderName = "Captain", onAnswer, onCancel, onJump, onOpenChat }: Props = $props();

  const groups = $derived(groupTasks(tasks));
  let answering = $state<string | null>(null);
  let confirming = $state<string | null>(null);
  // Per task, seeded from answerDrafts so a remount keeps what was typed.
  let drafts = $state<Record<string, string>>({});
  let error = $state("");
  const draftOf = (id: string) => drafts[id] ?? getDraft(id);
  function typed(id: string, text: string) {
    drafts = { ...drafts, [id]: text };
    setDraft(id, text);
  }

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
  async function answer(id: string) {
    const text = draftOf(id).trim();
    if (!text || !(await act(() => onAnswer!(id, text)))) return;
    clearDraft(id);
    drafts = { ...drafts, [id]: "" };
    if (answering === id) answering = null;
  }
  async function cancel(id: string) {
    confirming = null;
    if ((await act(() => onCancel!(id))) && answering === id) answering = null;
  }
</script>

<div data-testid="tasks-panel" class="flex flex-col gap-4 px-4 py-4 text-xs">
  {#if tasks.length === 0}
    <p class="text-black-600 dark:text-black-700">No Team tasks in this chat yet.</p>
  {/if}
  {#if error}<p class="text-[11px] text-red-600 dark:text-red-400">{error}</p>{/if}
  {#each groups as g (g.key)}
    <section data-testid={"tasks-group-" + g.key} class="flex flex-col gap-1.5">
      <h3 class={"text-[11px] font-semibold uppercase tracking-wide " + (g.key === "needs_you" ? "text-amber-700 dark:text-amber-300" : "text-black-600 dark:text-black-700")}>{g.label} · {g.tasks.length}</h3>
      {#each g.tasks as t (t.task_id)}
        {@const s = taskStatus(t)}
        <div class="flex flex-col gap-1 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2">
          <div class="flex items-center gap-1.5">
            <AgentAvatar kind={agents[t.to_handle]?.kind} shape={agents[t.to_handle]?.shape} expression={agents[t.to_handle]?.expression} color={agents[t.to_handle]?.color} size={18} />
            <span class="font-medium text-black-900 dark:text-white-100">{agents[t.to_handle]?.name || t.to_name || "@" + t.to_handle}</span>
            <span class="text-[10px] text-black-600 dark:text-black-700">{taskLabel(t, senderName)}{t.age ? " · " + t.age : ""}</span>
          </div>
          <span class="break-words text-black-800 dark:text-black-600">{t.title}</span>
          {#if t.summary}<span class="break-words text-black-600 dark:text-black-700">↳ {t.summary}</span>{/if}
          <div class="flex flex-wrap items-center gap-3 text-[11px]">
            {#if (s === "needs_you" || s === "answering") && onAnswer}<button type="button" onclick={() => { answering = answering === t.task_id ? null : t.task_id; }} class="text-amber-700 dark:text-amber-300 hover:underline">Answer</button>{/if}
            {#if (s === "working" || s === "needs_you" || s === "answering") && onCancel}<button type="button" onclick={() => { confirming = confirming === t.task_id ? null : t.task_id; }} class="text-red-600 dark:text-red-400 hover:underline">Cancel</button>{/if}
            {#if onJump}<button type="button" onclick={() => onJump?.(t)} class="text-green-600 dark:text-green-400 hover:underline">Jump</button>{/if}
            {#if onOpenChat}<button type="button" onclick={() => onOpenChat?.(t)} class="text-green-600 dark:text-green-400 hover:underline">Open chat ↗</button>{/if}
          </div>
          {#if confirming === t.task_id && onCancel}
            <CancelConfirm name={agents[t.to_handle]?.name || t.to_name || "@" + t.to_handle} onConfirm={() => cancel(t.task_id)} onKeep={() => { confirming = null; }} />
          {/if}
          {#if answering === t.task_id && onAnswer}
            <form class="flex items-center gap-1.5" onsubmit={(e) => { e.preventDefault(); answer(t.task_id); }}>
              <input type="text" value={draftOf(t.task_id)} oninput={(e) => typed(t.task_id, (e.currentTarget as HTMLInputElement).value)} aria-label={"Answer @" + t.to_handle} placeholder="Type your answer…" class="min-w-0 flex-1 rounded-md border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-900 px-2 py-1 text-xs text-black-900 dark:text-white-100" />
              <button type="submit" disabled={!draftOf(t.task_id).trim()} class="rounded-md bg-green-500 px-2 py-1 text-[11px] font-medium text-white-100 disabled:opacity-50">Send</button>
            </form>
          {/if}
          <details class="text-[11px]">
            <summary class="cursor-pointer select-none text-black-600 dark:text-black-700">Raw payload</summary>
            <pre class="mt-1 overflow-x-auto whitespace-pre-wrap break-words text-[10px] text-black-700 dark:text-black-600">{JSON.stringify(t, null, 2)}</pre>
          </details>
        </div>
      {/each}
    </section>
  {/each}
</div>
