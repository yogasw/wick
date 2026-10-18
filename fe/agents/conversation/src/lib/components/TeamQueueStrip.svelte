<script lang="ts">
  /* The team tasks waiting for their turn in this chat (TurnGate), as one
     clean strip over the composer: "⏳ Queued (2) · next: @yelan — <task>".
     A click lists them all. Without it a teammate's message that waits for
     the running turn to end is invisible on this side. Polls while the tab
     is visible; gone when the line is empty. */
  import { Effect } from "effect";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { getTeamQueue, type QueuedTeamTask } from "../api/subagents.js";

  type Props = {
    base: string;
    sessionId: string;
    /** For tests: the line to show instead of fetching it. */
    initial?: QueuedTeamTask[];
    pollMs?: number;
  };
  let { base, sessionId, initial, pollMs = 5000 }: Props = $props();

  let queue = $state<QueuedTeamTask[]>([]);
  let open = $state(false);

  $effect(() => {
    if (initial) {
      queue = initial;
      return;
    }
    const id = sessionId;
    let alive = true;
    const load = async () => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
      try {
        const q = await Effect.runPromise(getTeamQueue(base, id).pipe(Effect.provide(WickClientLayer)));
        if (alive) queue = q;
      } catch {
        /* the strip is a hint: a failed read keeps the last line */
      }
    };
    queue = [];
    void load();
    const t = setInterval(load, pollMs);
    return () => {
      alive = false;
      clearInterval(t);
    };
  });

  const next = $derived(queue[0]);
  function who(q: QueuedTeamTask): string {
    return q.from_handle ? "@" + q.from_handle : q.from_name || "a teammate";
  }
</script>

{#if next}
  <div data-testid="team-queue-strip" class="mb-2 rounded-xl bg-white-200 px-3 py-1.5 text-[12.5px] leading-5 dark:bg-navy-700">
    <button
      type="button"
      data-testid="team-queue-toggle"
      aria-expanded={open}
      onclick={() => (open = !open)}
      class="flex w-full min-w-0 items-center gap-2 text-left focus-visible:outline focus-visible:outline-2 focus-visible:outline-green-500"
      title="Team tasks waiting for this chat's current turn to end"
    >
      <span aria-hidden="true" class="h-[7px] w-[7px] shrink-0 rounded-full bg-warning-400"></span>
      <span class="shrink-0 font-semibold text-black-900 dark:text-white-100">Queued ({queue.length})</span>
      <span class="min-w-0 truncate text-black-700 dark:text-black-600">· next: {who(next)} — {next.title}</span>
      <span class="flex-1"></span>
      <span aria-hidden="true" class="shrink-0 text-black-700 dark:text-black-600">{open ? "▴" : "▾"}</span>
    </button>
    {#if open}
      <ol data-testid="team-queue-list" class="mt-1 flex flex-col gap-0.5">
        {#each queue as q (q.task_id)}
          <li class="flex min-w-0 items-center gap-2 text-black-700 dark:text-black-600" title={"Task " + q.task_id}>
            <span class="w-5 shrink-0 text-right tabular-nums">#{q.pos}</span>
            <span class="shrink-0 font-medium text-black-900 dark:text-white-100">{who(q)}</span>
            <span class="min-w-0 truncate">{q.title}</span>
            {#if q.revisions}
              <span class="shrink-0">· ↻ {q.revisions}</span>
            {/if}
          </li>
        {/each}
      </ol>
    {/if}
  </div>
{/if}
