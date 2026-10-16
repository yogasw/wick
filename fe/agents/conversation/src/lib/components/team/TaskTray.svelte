<script lang="ts">
  /* The thin strip above the composer: there only while a task this chat
     sent is still working or waits for an answer. Stacked avatars, the
     summary in its status colours, Answer and a chevron; a click opens the
     list (Answer / Cancel / Jump). */
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import AvatarActivity from "./AvatarActivity.svelte";
  import { taskActivity } from "../../avatarActivity.js";
  import type { TeamTaskItem } from "../../types/agents.js";
  import { isTaskActive, statusParts, taskStatus, type StatusPart, userTaskRoute } from "../../delegations.js";
  import { clearDraft, getDraft, setDraft } from "../../answerDrafts.js";
  import CancelConfirm from "./CancelConfirm.svelte";
  import SmBtn from "./SmBtn.svelte";
  import TeamIcon from "./TeamIcon.svelte";

  type Agent = { name: string; kind?: string; shape?: string; color?: string; expression?: string; image?: import("@wick-fe/common-avatar").AvatarImageView | null };
  type Props = {
    tasks: TeamTaskItem[];
    agents?: Record<string, Agent>;
    onAnswer?: (taskId: string, text: string) => Promise<void>;
    onCancel?: (taskId: string) => Promise<void>;
    onJump?: (t: TeamTaskItem) => void;
  };
  let { tasks, agents = {}, onAnswer, onCancel, onJump }: Props = $props();

  const active = $derived(tasks.filter(isTaskActive));
  const needsYou = $derived(active.some((t) => taskStatus(t) === "needs_you"));
  // A question waiting for the person goes first, the rest keep their order.
  const stacked = $derived([...active].sort((a, b) => Number(taskStatus(b) === "needs_you") - Number(taskStatus(a) === "needs_you")).slice(0, 3));
  const parts = $derived(statusParts(active));
  // The mockup's colours (DelegationBlock's summary): working ok, needs input
  // warn; an active task is never in the other states.
  const PART_TONE: Partial<Record<StatusPart["status"], string>> = {
    working: "font-semibold text-green-600 dark:text-green-400",
    needs_you: "font-semibold text-amber-700 dark:text-amber-300",
  };
  let open = $state(false);
  // The list's id for aria-controls, set only while the list is there.
  const uid = $props.id();
  const listId = uid + "-tasks";
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
  function openAnswer() {
    open = true;
    answering = active.find((t) => taskStatus(t) === "needs_you")?.task_id ?? null;
  }
</script>

{#if active.length > 0}
  <div data-testid="task-tray" class="relative mb-2">
    <div class="flex items-center gap-2.5 rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 py-1.5 pl-2.5 pr-2 text-xs">
      <button type="button" onclick={() => { open = !open; }} class="flex min-w-0 flex-1 items-center gap-2.5 text-left" aria-expanded={open} aria-controls={open ? listId : undefined}>
        <span class="flex -space-x-1.5">
          {#each stacked as t (t.task_id)}
            <span data-testid="task-tray-avatar" data-status={taskStatus(t)}><AvatarActivity activity={taskActivity(taskStatus(t))} size={22}><AgentAvatar kind={agents[t.to_handle]?.kind} image={agents[t.to_handle]?.image} shape={agents[t.to_handle]?.shape} expression={agents[t.to_handle]?.expression} color={agents[t.to_handle]?.color} size={22} /></AvatarActivity></span>
          {/each}
        </span>
        <span data-testid="task-tray-summary" class="min-w-0 truncate text-black-800 dark:text-black-600">{#each parts as p, i}{i ? " · " : ""}<span data-status={p.status} class={PART_TONE[p.status] ?? ""}>{p.label}</span>{/each}</span>
      </button>
      {#if needsYou && onAnswer}
        <SmBtn variant="warn" class="shrink-0" onclick={openAnswer}>Answer</SmBtn>
      {/if}
      <button
        type="button"
        data-testid="task-tray-toggle"
        aria-label={open ? "Hide tasks" : "Show tasks"}
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        onclick={() => { open = !open; }}
        class="-m-1 inline-flex h-5 w-5 shrink-0 items-center justify-center rounded text-black-700 hover:text-black-900 focus:outline-none focus-visible:ring-1 focus-visible:ring-green-500 dark:hover:text-white-100"
      ><TeamIcon name="chevron" class={"transition-transform " + (open ? "" : "rotate-180")} /></button>
    </div>
    {#if open}
      <div id={listId} data-testid="task-tray-popover" class="absolute bottom-full left-0 right-0 z-30 mb-1 flex max-h-72 flex-col gap-1 overflow-y-auto rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 p-2 text-xs shadow-md">
        {#each active as t (t.task_id)}
          {@const s = taskStatus(t)}
          <div class="flex flex-col gap-1 rounded-md px-1.5 py-1 hover:bg-white-200 dark:hover:bg-navy-700">
            <div class="flex items-center gap-1.5">
              <AvatarActivity activity={taskActivity(s)} size={16}><AgentAvatar kind={agents[t.to_handle]?.kind} image={agents[t.to_handle]?.image} shape={agents[t.to_handle]?.shape} expression={agents[t.to_handle]?.expression} color={agents[t.to_handle]?.color} size={16} /></AvatarActivity>
              <span data-testid="task-tray-name" class="font-medium text-black-900 dark:text-white-100">{userTaskRoute(t) ?? (agents[t.to_handle]?.name || t.to_name || "@" + t.to_handle)}</span>
              <span class={"text-[10px] " + (s === "needs_you" ? "text-amber-700 dark:text-amber-300" : "text-green-600 dark:text-green-400")}>{s === "needs_you" ? "needs you" : "working"}</span>
              <span class="ml-auto flex items-center gap-2 text-[11px]">
                {#if s !== "working" && onAnswer}<button type="button" onclick={() => { answering = answering === t.task_id ? null : t.task_id; }} class="text-amber-700 dark:text-amber-300 hover:underline">Answer</button>{/if}
                {#if onCancel}<button type="button" onclick={() => { confirming = confirming === t.task_id ? null : t.task_id; }} class="text-red-600 dark:text-red-400 hover:underline">Cancel</button>{/if}
                {#if onJump}<button type="button" onclick={() => { open = false; onJump?.(t); }} class="text-green-600 dark:text-green-400 hover:underline">Jump</button>{/if}
              </span>
            </div>
            <span class="truncate text-black-800 dark:text-black-600">{s === "working" ? t.title : (t.reply || t.summary || t.title)}</span>
            {#if confirming === t.task_id && onCancel}
              <CancelConfirm name={agents[t.to_handle]?.name || t.to_name || "@" + t.to_handle} onConfirm={() => cancel(t.task_id)} onKeep={() => { confirming = null; }} />
            {/if}
            {#if answering === t.task_id && onAnswer}
              <form class="flex items-center gap-1.5" onsubmit={(e) => { e.preventDefault(); answer(t.task_id); }}>
                <input type="text" value={draftOf(t.task_id)} oninput={(e) => typed(t.task_id, (e.currentTarget as HTMLInputElement).value)} aria-label={"Answer @" + t.to_handle} placeholder="Type your answer…" class="min-w-0 flex-1 rounded-md border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-900 px-2 py-1 text-xs text-black-900 dark:text-white-100" />
                <button type="submit" disabled={!draftOf(t.task_id).trim()} class="rounded-md bg-green-500 px-2 py-1 text-[11px] font-medium text-white-100 disabled:opacity-50">Send</button>
              </form>
            {/if}
          </div>
        {/each}
        {#if error}<span class="px-1.5 text-[11px] text-red-600 dark:text-red-400">{error}</span>{/if}
      </div>
    {/if}
  </div>
{/if}
