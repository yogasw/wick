<script lang="ts">
  /* A Team task's thread (grouped 2b): the brief, the teammate's replies
     and questions, and each answer, oldest first, as the Hub logged them.
     A row is avatar 22 + "Name 10:06" + the text (mockup .dth .msg); the
     person's own rows carry their initials (a person icon when their name
     is not known) in green and a "You" pill. While the
     teammate still works, its last line reads in the ok colour with the
     typing dots — or, when the last word was not its own, a "Working"
     line stands in. Read once when the thread mounts (opening it) and
     again whenever the task changes, which the team_task event already
     brings in: no polling of its own. */
  import type { Snippet } from "svelte";
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import AvatarActivity from "./AvatarActivity.svelte";
  import Dots from "./Dots.svelte";
  import TeamIcon from "./TeamIcon.svelte";
  import { taskActivity } from "../../avatarActivity.js";
  import type { TeamTaskItem, TeamThreadEntry } from "../../types/agents.js";
  import { clockTime, taskProgress, taskProgressShown, taskStatus } from "../../delegations.js";
  import { questionHtml, THREAD_CODE_CLASS } from "../../questionText.js";

  type Agent = { name: string; kind?: string; shape?: string; color?: string; expression?: string; image?: import("@wick-fe/common-avatar").AvatarImageView | null };
  type Props = {
    task: TeamTaskItem;
    load: (taskId: string) => Promise<TeamThreadEntry[]>;
    agents?: Record<string, Agent>;
    captainName?: string;
    /** The person's name for their own rows; the "You" pill alone without it. */
    userName?: string;
    /** Shown under the error when the thread does not load. */
    fallback?: Snippet;
  };
  let { task, load, agents = {}, captainName = "Captain", userName = "", fallback }: Props = $props();

  let entries = $state<TeamThreadEntry[] | null>(null);
  let error = $state("");

  // Anything that changes on the task's event reads the thread again.
  const version = $derived(`${task.task_id}|${task.updated_at}|${task.state}|${task.message_count ?? ""}`);
  $effect(() => {
    void version;
    const id = task.task_id;
    let stale = false;
    load(id).then(
      (list) => { if (!stale) { entries = list; error = ""; } },
      (e) => { if (!stale) error = e instanceof Error ? e.message : String(e); },
    );
    return () => { stale = true; };
  });

  // The section head already shows the brief (grouped 2b), so the Captain's
  // first row is dropped when it only repeats the task title. The server
  // keeps the latest entries only, so entries[0] is the brief only when it
  // was logged as the task started (the Hub's own test: at <= start + 1s);
  // a later Captain line that repeats the title is never dropped.
  function isBrief(e: TeamThreadEntry | undefined): boolean {
    if (!e || e.role !== "captain" || e.text.trim() !== (task.title ?? "").trim()) return false;
    const at = Date.parse(e.at), started = Date.parse(task.started_at);
    return !Number.isNaN(at) && !Number.isNaN(started) && at <= started + 1000;
  }
  const shown = $derived(entries && isBrief(entries[0]) ? entries.slice(1) : entries);
  const working = $derived(taskStatus(task) === "working");
  const lastIsTeammate = $derived(!!shown?.length && shown[shown.length - 1].role === "teammate");
  const initials = $derived(userName.trim().split(/\s+/).map((w) => w[0] ?? "").join("").slice(0, 2).toUpperCase());

  function nameOf(e: TeamThreadEntry): string {
    if (e.role === "captain") return (e.handle && agents[e.handle]?.name) || e.name || captainName;
    return (e.handle && agents[e.handle]?.name) || e.name || task.to_name || "@" + task.to_handle;
  }
  const teammateName = $derived(agents[task.to_handle]?.name || task.to_name || "@" + task.to_handle);
  const progress = $derived(taskProgress(task));
  const progressShown = $derived(taskProgressShown(task));
  // The time on the progress line is when this client saw that line: a
  // progress push lives in the server's memory only and does not move the
  // task's updated_at, so updated_at would show when the task last changed.
  let progressAt = $state(0);
  let seenProgress = "";
  $effect(() => {
    const p = progress;
    if (p && p !== seenProgress) progressAt = Date.now();
    seenProgress = p;
  });
</script>

{#snippet avatar(handle: string | undefined, live: boolean)}
  {@const a = handle ? agents[handle] : undefined}
  <span class="mt-px shrink-0">
    {#if live}
      <AvatarActivity activity={taskActivity("working")} size={22}><AgentAvatar kind={a?.kind} image={a?.image} shape={a?.shape} expression={a?.expression} color={a?.color} size={22} /></AvatarActivity>
    {:else}
      <AgentAvatar kind={a?.kind} image={a?.image} shape={a?.shape} expression={a?.expression} color={a?.color} size={22} />
    {/if}
  </span>
{/snippet}

<div data-testid="task-thread" class="flex flex-col pb-1.5">
  {#if error}
    <p data-testid="task-thread-error" class="px-3 py-1 text-[11px] text-red-600 dark:text-red-400">{error}</p>
    {@render fallback?.()}
  {:else if entries === null}
    <p class="px-3 py-1 text-[11px] text-black-700">Loading…</p>
  {:else if shown!.length === 0 && !working}
    <p class="px-3 py-1 text-[11px] text-black-700">No messages yet.</p>
  {/if}
  {#each shown ?? [] as e, i (i)}
    {@const live = working && i === shown!.length - 1 && e.role === "teammate"}
    <div data-testid="task-thread-msg" data-role={e.role} class="flex gap-2 px-3 py-1">
      {#if e.role === "user"}
        <span aria-hidden="true" class="mt-px inline-flex h-[22px] w-[22px] shrink-0 items-center justify-center rounded-full bg-green-600 text-[9px] font-semibold text-white-100">{#if initials}{initials}{:else}<TeamIcon name="user" size={13} />{/if}</span>
      {:else}
        {@render avatar(e.handle || (e.role === "teammate" ? task.to_handle : undefined), live)}
      {/if}
      <div class="min-w-0 flex-1">
        <div class="flex items-baseline gap-1.5 text-[11px] text-black-700">
          {#if e.role === "user"}
            {#if userName}<b class="text-xs font-semibold text-black-900 dark:text-white-100">{userName}</b>{/if}
            <span data-testid="task-thread-you" class="rounded-full bg-green-500/[.18] px-[5px] text-[9px] font-bold uppercase tracking-[.06em] text-green-600 dark:text-green-400">You</span>
          {:else}
            <b class="text-xs font-semibold text-black-900 dark:text-white-100">{nameOf(e)}</b>
          {/if}
          <span>{clockTime(Date.parse(e.at))}</span>
        </div>
        <div class={["whitespace-pre-wrap break-words text-[12.5px] leading-[1.5]", live ? "text-green-600 dark:text-green-400" : "text-black-900 dark:text-white-100"]}>{@html questionHtml(e.text, THREAD_CODE_CLASS)}{#if live}<Dots />{/if}</div>
      </div>
    </div>
  {/each}
  {#if working && entries !== null && !lastIsTeammate}
    <div data-testid="task-thread-working" data-role="teammate" class="flex gap-2 px-3 py-1">
      {@render avatar(task.to_handle, true)}
      <div class="min-w-0 flex-1">
        <div class="flex items-baseline gap-1.5 text-[11px] text-black-700"><b class="text-xs font-semibold text-black-900 dark:text-white-100">{teammateName}</b>{#if progress && progressAt}<span data-testid="task-thread-working-at">{clockTime(progressAt)}</span>{/if}</div>
        <!-- The live progress line when the server sent one (combined 2a), else Working… -->
        <div class="text-[12.5px] leading-[1.5] text-green-600 dark:text-green-400">{#if progress}<span class="flex min-w-0 items-baseline"><span data-testid="task-thread-progress" class="truncate" title={progress}>{progressShown}</span><span class="shrink-0"><Dots /></span></span>{:else}Working<Dots />{/if}</div>
      </div>
    </div>
  {/if}
</div>
