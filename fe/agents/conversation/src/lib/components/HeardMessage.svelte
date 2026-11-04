<script lang="ts">
  /* One reply a listening Slack thread heard (thread_heard). It is part of
     the conversation from the moment it arrives, so it reads like a thread
     message: who said what, when, and whether the agent has read it yet. */
  import type { HeardStatus } from "../listenHolds.js";

  interface Props {
    turn: { text: string; ts?: string; extras?: Record<string, string> };
    status?: HeardStatus;
  }
  let { turn, status = "unread" }: Props = $props();

  const name = $derived.by(() => {
    const n = turn.extras?.name ?? "";
    return !n || n.startsWith("<@") ? "Slack user" : n.replace(/\s*\(@[^)]*\)$/, "");
  });
  const initial = $derived(name.trim().charAt(0).toUpperCase() || "?");
  const time = $derived(turn.ts ? new Date(turn.ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "");
  const files = $derived((turn.extras?.files ?? "").split("\n").filter(Boolean));
  const label = $derived(status === "read" ? "✓ read" : status === "dropped" ? "not read · the hold was dropped" : "👂 heard · not read yet");
</script>

<div class="flex min-w-0 max-w-full gap-2" data-testid="heard-message" data-status={status}>
  <div class="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-navy-500 text-[11px] font-semibold text-white-100" aria-hidden="true">{initial}</div>
  <div class="flex min-w-0 max-w-[90%] flex-col sm:max-w-[80%] lg:max-w-[44rem]">
    <div class="flex items-baseline gap-1.5 text-xs">
      <span class="font-semibold text-black-900 dark:text-white-100">{name}</span>
      <span class="text-black-600 dark:text-black-500">Slack{time ? " · " + time : ""}</span>
    </div>
    {#if turn.text}
      <div class="whitespace-pre-wrap break-words text-sm text-black-800 dark:text-white-200">{turn.text}</div>
    {/if}
    {#each files as f}
      <div class="mt-1 inline-flex max-w-full items-center gap-1 self-start rounded-md border border-white-300 px-2 py-0.5 text-[11px] text-black-700 dark:border-navy-600 dark:text-black-600">
        <svg viewBox="0 0 12 12" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.3" aria-hidden="true"><path d="M3 1.5h4l2.5 2.5v6.5h-6.5z" stroke-linejoin="round"></path></svg>
        <span class="truncate">{f}</span>
      </div>
    {/each}
    <div class="mt-0.5 text-[10px] {status === 'unread' ? 'text-green-600 dark:text-green-400' : status === 'dropped' ? 'text-neg-400' : 'text-black-600 dark:text-black-500'}">{label}</div>
  </div>
</div>
