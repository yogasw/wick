<script lang="ts">
  /* The rows nested under a team_message call in a turn's trace (T16
     S10): one per teammate the call reached — handle, status, the
     newest line of its thread, how long it ran, Open (the teammate's
     chat) — and on a click the task's thread as the trace recorded it
     (teamTrace.ts). The live task list is laid over the recorded rows
     (withLive), so a row moves on while its teammate works. An @handle
     line sent its task with no call: those rows sit under their own
     head (mention). */
  import { statusLabel } from "../../delegations.js";
  import { traceDuration, traceLine, traceStatus, withLive, type TeamTraceTask } from "../../teamTrace.js";
  import type { TeamTaskItem } from "../../types/agents.js";
  import SmBtn from "./SmBtn.svelte";

  type Props = {
    tasks: TeamTraceTask[];
    mention?: boolean;
    captainName?: string;
    /** The chat's live tasks by id (the Team tasks stream). */
    live?: Record<string, TeamTaskItem>;
    /** Opens the teammate's chat the task runs in. */
    onOpen?: (t: TeamTaskItem) => void;
  };
  let { tasks, mention = false, captainName = "Captain", live = {}, onOpen }: Props = $props();
  const rows = $derived(tasks.map((t) => withLive(t, live[t.task_id])));

  let open = $state<Record<string, boolean>>({});

  const TONE: Record<string, string> = {
    working: "bg-green-500/15 text-green-600 dark:text-green-400",
    answering: "bg-white-300 text-black-800 dark:bg-navy-600 dark:text-black-600",
    needs_you: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
    replied: "bg-white-300 text-black-800 dark:bg-navy-600 dark:text-black-600",
    declined: "bg-red-500/15 text-red-600 dark:text-red-400",
    failed: "bg-red-500/15 text-red-600 dark:text-red-400",
    canceled: "bg-white-300 text-black-700 dark:bg-navy-600",
  };

  function clock(at?: string): string {
    const ms = at ? Date.parse(at) : NaN;
    if (!Number.isFinite(ms)) return "";
    const d = new Date(ms);
    return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`;
  }
</script>

<div data-testid="team-trace" data-mention={mention} class="-mt-1 flex flex-col overflow-hidden rounded-b-xl border border-t-0 border-white-300 bg-white-100 text-xs dark:border-navy-600 dark:bg-navy-800">
  {#if mention}
    <div data-testid="team-trace-mention" class="px-3 pt-1.5 text-[10.5px] text-black-700">Sent with an @mention in the reply</div>
  {/if}
  {#each rows as t (t.task_id)}
    {@const s = traceStatus(t)}
    {@const dur = traceDuration(t)}
    <div data-testid="team-trace-row" data-status={s} class="flex items-center gap-2 border-t border-white-300 px-3 py-[7px] first:border-t-0 dark:border-navy-600">
      <button
        type="button"
        data-testid="team-trace-toggle"
        aria-expanded={!!open[t.task_id]}
        onclick={() => { open = { ...open, [t.task_id]: !open[t.task_id] }; }}
        class="flex min-w-0 flex-1 items-center gap-2 text-left hover:opacity-80"
      >
        <span class="inline-flex shrink-0 items-center rounded-full bg-white-200 px-2 py-0.5 text-[10px] font-medium text-black-900 dark:bg-navy-700 dark:text-white-100">@{t.to_handle}</span>
        <span data-testid="team-trace-status" class={"shrink-0 rounded px-1.5 py-px text-[10px] font-medium " + TONE[s]}>{statusLabel(s, captainName)}</span>
        <span class="min-w-0 flex-1 truncate text-[11px] text-black-800 dark:text-black-600">{traceLine(t)}</span>
        {#if dur}<span data-testid="team-trace-duration" class="shrink-0 font-mono text-[10px] text-black-700">{dur}</span>{/if}
      </button>
      {#if onOpen && live[t.task_id]}
        <SmBtn icon="open" data-testid="team-trace-open" class="shrink-0" onclick={() => onOpen?.(live[t.task_id])}>Open</SmBtn>
      {/if}
    </div>
    {#if open[t.task_id]}
      <ol data-testid="team-trace-thread" class="flex flex-col gap-1 border-t border-white-300 bg-white-200 px-3 py-2 dark:border-navy-600 dark:bg-navy-700">
        {#each t.entries ?? [] as e, i (i)}
          <li class="flex gap-2 text-[11px] leading-[1.45]">
            <span class="shrink-0 font-mono text-[10px] text-black-700">{clock(e.at)}</span>
            <span class="shrink-0 font-medium text-black-900 dark:text-white-100">{e.role === "user" ? captainName : (e.name || "@" + (e.handle || t.to_handle))}</span>
            <span class="min-w-0 whitespace-pre-wrap break-words text-black-800 dark:text-black-600">{e.text}</span>
          </li>
        {:else}
          <li class="text-[11px] italic text-black-700">No thread recorded.</li>
        {/each}
      </ol>
    {/if}
  {/each}
</div>
