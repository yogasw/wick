<script lang="ts">
  /* An ask_user question kept in the thread. While pending, the answer is
     still given in the box above the composer — this card is the record, not
     a second input, so the two never ask twice. Once settled it shows the
     answer as a pill (a secret field arrives already masked). */
  import type { ConversationTurn } from "../../types/agents.js";
  import { inputRequestView } from "../../interactiveCards.js";

  let { turn }: { turn: ConversationTurn } = $props();
  const v = $derived(inputRequestView(turn.text, turn.extras));
</script>

<div data-testid="input-request" data-state={v.state} class="flex w-full max-w-md flex-col gap-1.5 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-left text-xs shadow-sm">
  <div class="flex items-center gap-1.5 text-[11px] text-black-600 dark:text-black-500">
    <svg viewBox="0 0 12 12" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true">
      <circle cx="6" cy="6" r="4.5"></circle><path d="M4.6 4.7a1.5 1.5 0 012.8.6c0 1-1.4 1.2-1.4 2M6 8.6v.1"></path>
    </svg>
    <span>{turn.extras?.agent ? `@${turn.extras.agent} asked` : "Question"}</span>
  </div>
  <p class="whitespace-pre-wrap break-words text-sm text-black-900 dark:text-white-100">{v.question}</p>
  {#if v.pending}
    {#if v.options.length > 0}
      <div class="flex flex-wrap gap-1">
        {#each v.options as o}
          <span class="rounded-full border border-white-300 dark:border-navy-600 px-2 py-0.5 text-[11px] text-black-700 dark:text-black-600">{o}</span>
        {/each}
      </div>
    {/if}
    <span class="text-[11px] italic text-black-600 dark:text-black-500">Waiting for your answer below ↓</span>
  {:else}
    <span
      data-testid="input-request-pill"
      class={"self-start rounded-full px-2 py-0.5 text-[11px] font-medium " +
        (v.state === "answered"
          ? "bg-green-500/10 text-green-700 dark:text-green-300"
          : "bg-white-200 dark:bg-navy-700 text-black-700 dark:text-black-600")}
    >{v.state === "answered" ? "✓ " : ""}{v.pill}</span>
  {/if}
</div>
