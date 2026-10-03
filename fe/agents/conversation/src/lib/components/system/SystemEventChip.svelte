<script lang="ts">
  /* One chip for every server-recorded system event. What it says and which
     icon it wears come from the registry in systemEvents.ts; this only draws. */
  import type { ConversationTurn } from "../../types/agents.js";
  import { getSystemEvent, systemEventParts } from "../../systemEvents.js";

  type Props = {
    turn: ConversationTurn;
    names?: Record<string, string>;
    /** Opens a Team agent's chat; unset, handles are plain text. */
    onOpenAgent?: (handle: string) => void;
  };
  let { turn, names = {}, onOpenAgent }: Props = $props();

  const r = $derived(getSystemEvent(turn.kind));
  const parts = $derived(systemEventParts(turn, { names }));
  const warn = $derived(r.tone === "warn");
</script>

<div
  data-testid="system-event"
  data-kind={turn.kind}
  title={turn.text}
  class={"inline-flex items-center gap-1.5 rounded-2xl border px-3 py-1 text-xs max-w-full " +
    (warn
      ? "border-amber-400/40 bg-amber-400/10 text-amber-700 dark:text-amber-300"
      : "border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 text-black-700 dark:text-black-600")}
>
  <svg viewBox="0 0 12 12" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
    {#if r.icon === "user-plus"}
      <circle cx="4.5" cy="4" r="2"></circle><path d="M1 10.5c.5-2 2-3 3.5-3s3 1 3.5 3M9.5 3.5v3M8 5h3"></path>
    {:else if r.icon === "key"}
      <circle cx="4" cy="6" r="2.2"></circle><path d="M6.2 6H11M9.5 6v1.8M11 6v1.3"></path>
    {:else if r.icon === "arrow"}
      <path d="M2 6h7M6.5 3.5 9 6 6.5 8.5"></path>
    {:else if r.icon === "stop"}
      <path d="M4 1.5h4L10.5 4v4L8 10.5H4L1.5 8V4z"></path><path d="M6 4v2.2M6 8v.1"></path>
    {:else if r.icon === "ban"}
      <circle cx="6" cy="6" r="4.5"></circle><path d="M2.8 9.2l6.4-6.4"></path>
    {:else if r.icon === "clock"}
      <circle cx="6" cy="6" r="4.5"></circle><path d="M6 3.5V6l1.6 1"></path>
    {:else if r.icon === "plug"}
      <path d="M4 1.5v2.5M8 1.5v2.5M3 4h6v1.5a3 3 0 01-6 0zM6 8.5v2"></path>
    {:else if r.icon === "link"}
      <path d="M5 7l2-2M4.2 5.3 3 6.5a1.8 1.8 0 002.5 2.5l1.2-1.2M7.8 6.7 9 5.5A1.8 1.8 0 006.5 3L5.3 4.2"></path>
    {:else}
      <circle cx="6" cy="6" r="4.5"></circle><path d="M6 5.5v3M6 3.6v.1"></path>
    {/if}
  </svg>
  <span class="min-w-0 truncate">{#each parts as p}{#if typeof p === "string"}{p}{:else if onOpenAgent}<button type="button" class="font-medium text-green-600 dark:text-green-400 hover:underline" onclick={() => onOpenAgent?.(p.handle)}>@{p.handle}</button>{:else}<span class="font-medium">@{p.handle}</span>{/if}{/each}</span>
</div>
