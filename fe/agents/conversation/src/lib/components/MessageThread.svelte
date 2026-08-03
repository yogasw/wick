<script lang="ts">
  import type { AgentMessageItem } from "../types/agents.js";
  import { timeAgo, exactTime } from "../timeFormat.js";
  import { now } from "../stores/now.js";

  type Props = {
    messages: AgentMessageItem[];
    hopsLeft: number;
    onBumpHops: () => void;
  };

  let { messages, hopsLeft, onBumpHops }: Props = $props();

  // An ask is the only kind that leaves someone waiting, so it is the
  // only kind worth colouring apart. prog for "in flight", pos for the
  // answer that closes it — green stays the accent, never a status.
  function kindCls(kind: string): string {
    if (kind === "ask") return "bg-prog-100 text-prog-400";
    if (kind === "reply") return "bg-pos-100 text-pos-400";
    return "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600";
  }
</script>

<div class="flex flex-col gap-2 p-3">
  {#if hopsLeft <= 0}
    <div
      class="flex flex-col gap-2 rounded-lg bg-cau-100 px-3 py-2 text-[11px] text-cau-400 sm:flex-row sm:items-center sm:justify-between"
    >
      <span>Agents have used up their messages to each other for now.</span>
      <button
        type="button"
        onclick={onBumpHops}
        class="shrink-0 rounded px-2 py-1 font-medium bg-cau-200 hover:bg-cau-300 transition-colors"
      >Allow 10 more</button>
    </div>
  {/if}

  {#each messages as m (m.id)}
    {@const sent = timeAgo(m.created_at, $now)}
    <div class="rounded-lg border border-white-300 dark:border-navy-600 p-3">
      <div class="mb-1 flex items-center gap-1.5 text-[11px]">
        <span class="truncate font-medium text-black-900 dark:text-white-100">@{m.from_handle}</span>
        <span class="shrink-0 text-black-700 dark:text-black-600">&rarr;</span>
        <span class="truncate font-medium text-black-900 dark:text-white-100">@{m.to_handle}</span>
        <!-- Ordering alone does not say whether two agents traded messages
             seconds apart or hours apart, and that is the difference
             between a live exchange and a stalled one. -->
        {#if sent}
          <span
            class="ml-auto shrink-0 text-black-700 dark:text-black-600"
            title={exactTime(m.created_at)}
          >{sent}</span>
        {/if}
        <span class={"shrink-0 rounded-full px-2 py-0.5 " + kindCls(m.kind) + (sent ? "" : " ml-auto")}
          >{m.kind}</span
        >
      </div>
      <p class="whitespace-pre-wrap break-words text-xs text-black-800 dark:text-black-600">{m.body}</p>
      {#if m.auto_reply}
        <p class="mt-1 text-[11px] text-cau-400">
          Closing message, sent as the answer automatically — it may not answer what was asked.
        </p>
      {/if}
    </div>
  {/each}

  {#if messages.length === 0}
    <p class="px-1 py-6 text-center text-xs text-black-700 dark:text-black-600">
      No messages between agents yet.
    </p>
  {/if}
</div>
