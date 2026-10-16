<script lang="ts">
  import type { ConversationTurn } from "../../types/agents.js";

  /* The notice of an AUTOMATIC provider move (system turn kind
     provider_failover / provider_return). Shaped like the switch chip but
     amber for a failover — the answer moved because something broke —
     and green for the way back. Built from Extras (from, to, home,
     reason, note, continue); turn.text is only the plain-channel
     fallback. The continue message wick sent to the new provider is long
     and only matters when checking what it was told, so it folds. */
  type Props = { turn: ConversationTurn };
  let { turn }: Props = $props();

  const x = $derived(turn.extras ?? {});
  const isReturn = $derived(turn.kind === "provider_return");
  const label = $derived.by(() => {
    if (isReturn) return `Back to ${x.to || x.home || "the home provider"}`;
    const from = x.from || "provider";
    const to = x.to || "next provider";
    return `Auto-failover: ${from} → ${to}${x.reason ? ` · ${x.reason}` : ""}`;
  });
</script>

<div data-testid="provider-failover-card" data-kind={turn.kind} class="flex flex-col items-center gap-1 max-w-full" title={turn.text}>
  <div
    class="inline-flex items-start gap-1.5 rounded-2xl border px-3 py-1 text-xs max-w-full {isReturn
      ? 'border-green-500/40 bg-green-500/10 text-green-700 dark:text-green-300'
      : 'border-amber-400/40 bg-amber-400/10 text-amber-700 dark:text-amber-300'}"
  >
    <svg viewBox="0 0 12 12" class="h-3 w-3 mt-0.5 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5">
      {#if isReturn}
        <path d="M10 6H2.5M5 3.5L2.5 6 5 8.5" stroke-linecap="round" stroke-linejoin="round"></path>
      {:else}
        <path d="M2 6h7.5M7 3.5L9.5 6 7 8.5" stroke-linecap="round" stroke-linejoin="round"></path>
      {/if}
    </svg>
    <span data-testid="provider-failover-label" class="whitespace-pre-wrap break-words min-w-0">{label}</span>
  </div>
  {#if x.note}
    <p data-testid="provider-failover-note" class="text-[11px] text-black-700 dark:text-black-600 text-center max-w-prose">{x.note}</p>
  {/if}
  {#if !isReturn && x.continue}
    <details data-testid="provider-failover-continue" class="max-w-full text-[11px] text-black-700 dark:text-black-600">
      <summary class="cursor-pointer select-none text-center hover:underline">Auto-continued on {x.to || "the next provider"}</summary>
      <pre class="mt-1 max-h-64 overflow-auto whitespace-pre-wrap break-words rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-3 py-2 font-mono text-[11px]">{x.continue}</pre>
    </details>
  {/if}
</div>
