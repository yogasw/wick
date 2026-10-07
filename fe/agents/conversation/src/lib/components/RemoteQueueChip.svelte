<script lang="ts">
  /* Where a message sent to a busy remote agent stands: queued behind the
     running turn (with a cancel), forwarded into it, or cancelled. */
  import { queueView } from "../remoteQueue.js";

  interface Props {
    turn: { role: string; kind?: string; text: string; extras?: Record<string, string> };
    agentName?: string;
    onCancel?: (queueId: string) => Promise<void>;
  }
  let { turn, agentName = "", onCancel }: Props = $props();

  const view = $derived(queueView(turn, agentName));
  let busy = $state(false);

  async function cancel() {
    const id = turn.extras?.queue_id;
    if (!onCancel || !id || busy) return;
    busy = true;
    try {
      await onCancel(id);
    } finally {
      busy = false;
    }
  }
</script>

{#if view}
  <div
    data-remote-queue={view.state}
    class="inline-flex max-w-full items-center gap-1.5 text-[11px] {view.state === 'queued' ? 'text-amber-700 dark:text-amber-300' : 'text-black-700 dark:text-black-600'}"
    title={turn.text}
  >
    <span class="min-w-0 truncate {view.state === 'cancelled' ? 'line-through' : ''}">{turn.text}</span>
    <span class="shrink-0">· {view.label}</span>
    {#if view.canCancel && onCancel}
      <button type="button" class="shrink-0 underline hover:no-underline disabled:opacity-50" disabled={busy} onclick={cancel}>Cancel</button>
    {/if}
  </div>
{/if}
