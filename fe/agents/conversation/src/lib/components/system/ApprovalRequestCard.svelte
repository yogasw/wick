<script lang="ts">
  /* A gate approval the server recorded in the thread. Pending, it offers
     Accept / Accept for this agent / Decline — the same gate the approval
     modal decides, so a choice made in either settles both. Settled, it
     keeps the command with the decision as a pill. */
  import type { ConversationTurn } from "../../types/agents.js";
  import { approvalView, type ApprovalDecisionChoice } from "../../interactiveCards.js";

  type Props = {
    turn: ConversationTurn;
    onDecide?: (approvalId: string, decision: ApprovalDecisionChoice) => void;
  };
  let { turn, onDecide }: Props = $props();
  const v = $derived(approvalView(turn.text, turn.extras));
  let busy = $state(false);

  function decide(d: ApprovalDecisionChoice) {
    if (busy || !v.id) return;
    busy = true;
    try { onDecide?.(v.id, d); } finally { setTimeout(() => { busy = false; }, 1500); }
  }
</script>

<div data-testid="approval-request" data-state={v.pending ? "pending" : "settled"} class="flex w-full max-w-md flex-col gap-1.5 rounded-xl border border-amber-400/40 bg-white-100 dark:bg-navy-800 px-3 py-2 text-left text-xs shadow-sm">
  <div class="flex items-center gap-1.5 text-[11px] text-amber-700 dark:text-amber-300">
    <svg viewBox="0 0 12 12" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" aria-hidden="true">
      <path d="M6 1 10.5 2.8v3c0 2.4-1.8 4.3-4.5 5.2C3.3 10.1 1.5 8.2 1.5 5.8v-3z"></path>
    </svg>
    <span>{v.agent ? `@${v.agent} wants to run` : "Approval needed"}{v.tool ? ` · ${v.tool}` : ""}</span>
  </div>
  {#if v.cmd}
    <code class="block whitespace-pre-wrap break-all rounded-md bg-white-200 dark:bg-navy-900 px-2 py-1 font-mono text-[11px] text-black-900 dark:text-white-100">{v.cmd}</code>
  {/if}
  {#if v.pending}
    <div class="flex flex-wrap gap-1.5 pt-0.5">
      <button type="button" data-testid="approval-accept" disabled={busy || !onDecide} onclick={() => decide("accept")} class="rounded-lg bg-green-500 px-3 py-1 text-xs font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">Accept</button>
      <button type="button" data-testid="approval-accept-session" disabled={busy || !onDecide} onclick={() => decide("accept_for_session")} class="rounded-lg border border-white-300 dark:border-navy-600 px-3 py-1 text-xs font-medium text-black-800 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700 disabled:opacity-50">Accept for this agent</button>
      <button type="button" data-testid="approval-decline" disabled={busy || !onDecide} onclick={() => decide("decline")} class="rounded-lg border border-neg-400/40 px-3 py-1 text-xs font-medium text-neg-400 hover:bg-neg-400/10 disabled:opacity-50">Decline</button>
    </div>
  {:else}
    <span data-testid="approval-pill" class={"self-start rounded-full px-2 py-0.5 text-[11px] font-medium " + (v.allowed ? "bg-green-500/10 text-green-700 dark:text-green-300" : "bg-neg-400/10 text-neg-400")}>{v.allowed ? "✓ " : "✕ "}{v.outcome}</span>
  {/if}
</div>
