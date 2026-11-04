<script lang="ts">
  /* A listening thread waiting to be read: the agent has heard the replies
     above and reads them once the thread goes quiet. The ring counts down
     to that moment; every new reply in the thread starts it over. */
  import { onDestroy } from "svelte";
  import { holdOf } from "../listenHolds.js";

  interface Props {
    turn: { role: string; kind?: string; text: string; extras?: Record<string, string> };
    agentName?: string;
  }
  let { turn, agentName = "" }: Props = $props();

  const hold = $derived(holdOf(turn));
  const deadline = $derived(hold.deadlineMs || (hold.startedMs && hold.delaySec ? hold.startedMs + hold.delaySec * 1000 : 0));
  let now = $state(Date.now());
  const timer = setInterval(() => (now = Date.now()), 1000);
  onDestroy(() => clearInterval(timer));

  const leftMs = $derived(deadline ? Math.max(0, deadline - now) : 0);
  const totalMs = $derived(hold.delaySec ? hold.delaySec * 1000 : 0);
  const frac = $derived(totalMs ? Math.min(1, leftMs / totalMs) : 0);
  const clock = $derived.by(() => {
    const s = Math.ceil(leftMs / 1000);
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  });
  const C = 2 * Math.PI * 9;
  const who = $derived(agentName || "The agent");
  const replies = $derived(hold.heard === 1 ? "1 new reply" : hold.heard ? `${hold.heard} new replies` : "new replies");
</script>

<div class="flex justify-center py-1">
  <div data-testid="listen-countdown" class="inline-flex max-w-full items-center gap-2 rounded-2xl border border-white-300 bg-white-200 px-3 py-1 text-xs text-black-700 dark:border-navy-600 dark:bg-navy-800 dark:text-black-600">
    <svg viewBox="0 0 22 22" class="h-5 w-5 shrink-0 -rotate-90" aria-hidden="true">
      <circle cx="11" cy="11" r="9" fill="none" stroke="currentColor" stroke-opacity="0.25" stroke-width="2.5"></circle>
      <circle cx="11" cy="11" r="9" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" class="text-green-500" stroke-dasharray={C} stroke-dashoffset={C * (1 - frac)}></circle>
    </svg>
    <span class="min-w-0 truncate">
      {hold.mode === "auto_reply" ? "⚡" : "👂"} {who} heard {replies} ·
      {#if leftMs > 0}reads in <span class="tabular-nums">{clock}</span>{:else}reading now{/if}
    </span>
  </div>
</div>
