<script lang="ts">
  /* One reply a listening Slack thread heard (thread_heard). It is part of
     the conversation from the moment it arrives, so it reads like a group
     chat: the reader's own replies on the right, everyone else's on the
     left. Replies in a row from one person share one name and avatar; the
     read state is said once, under the last of them. */
  import type { HeardStatus } from "../listenHolds.js";
  import { linkifyText } from "../markdown.js";
  import { isViewer } from "../viewer.js";

  interface Props {
    turn: { text: string; ts?: string; extras?: Record<string, string> };
    status?: HeardStatus;
    /** First of a run of replies by the same author: name + avatar. */
    first?: boolean;
    /** Last of the run: time and read state. */
    last?: boolean;
  }
  let { turn, status = "unread", first = true, last = true }: Props = $props();

  const mine = $derived(isViewer(turn.extras?.wick_user_id));
  const name = $derived.by(() => {
    const n = turn.extras?.name ?? "";
    return !n || n.startsWith("<@") || /^[UWB][A-Z0-9]{6,}$/.test(n) ? "Slack user" : n.replace(/\s*\(@[^)]*\)$/, "");
  });
  const initial = $derived(name.trim().charAt(0).toUpperCase() || "?");
  /* A steady colour per person, so a thread of three people reads at a
     glance. */
  const hue = $derived.by(() => {
    let h = 0;
    for (const c of turn.extras?.user ?? name) h = (h * 31 + c.charCodeAt(0)) % 360;
    return h;
  });
  const time = $derived(turn.ts ? new Date(turn.ts).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "");
  const files = $derived((turn.extras?.files ?? "").split("\n").filter(Boolean));
  const state = $derived(status === "unread" ? "not read yet" : status === "dropped" ? "not read" : "");
</script>

<div class={"flex min-w-0 max-w-full gap-2 " + (mine ? "justify-end" : "")} data-testid="heard-message" data-status={status} data-mine={mine ? "1" : "0"}>
  {#if !mine}
    {#if first}
      <div class="mt-5 flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-xs font-semibold text-white-100" style={`background: hsl(${hue} 55% 45%)`} aria-hidden="true">{initial}</div>
    {:else}
      <div class="w-7 shrink-0"></div>
    {/if}
  {/if}
  <div class={"flex min-w-0 max-w-[90%] flex-col gap-0.5 sm:max-w-[80%] lg:max-w-[44rem] " + (mine ? "items-end" : "items-start")}>
    {#if first && !mine}
      <div class="px-1 text-xs font-semibold text-black-900 dark:text-white-100">{name}</div>
    {/if}
    {#if turn.text}
      <div
        class={"min-w-0 max-w-full whitespace-pre-wrap rounded-2xl px-3.5 py-2 text-sm leading-relaxed [overflow-wrap:anywhere] " +
          (mine ? "rounded-br-sm bg-green-500 text-white-100" : "rounded-tl-sm bg-white-200 text-black-900 ring-1 ring-white-400 dark:bg-navy-700 dark:text-white-100 dark:ring-navy-600")}
      >
        {@html linkifyText(turn.text)}
      </div>
    {/if}
    {#each files as f}
      <div class="inline-flex max-w-full items-center gap-1 rounded-md border border-white-300 px-2 py-0.5 text-[11px] text-black-700 dark:border-navy-600 dark:text-black-600">
        <svg viewBox="0 0 12 12" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.3" aria-hidden="true"><path d="M3 1.5h4l2.5 2.5v6.5h-6.5z" stroke-linejoin="round"></path></svg>
        <span class="truncate">{f}</span>
      </div>
    {/each}
    {#if last}
      <div class="flex items-center gap-1 px-1 text-[10px] text-black-600 dark:text-black-500">
        {#if status === "unread"}<span class="h-1.5 w-1.5 rounded-full bg-green-500" aria-hidden="true"></span>{/if}
        <span>{state ? state + " · " : ""}Slack{time ? " · " + time : ""}</span>
      </div>
    {/if}
  </div>
</div>
