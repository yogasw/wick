<script lang="ts">
  /* An A2A remote agent asked something back (TASK_STATE_INPUT_REQUIRED).
     The question itself is already in the chat as its reply; this card
     says the agent waits and that the next message answers it — the
     server sends that message to the same task. The state is read per
     chat whenever the turn settles (`turn` changes). */
  import { getRemoteAgent, runApi } from "../../api/team.js";

  type Props = {
    base: string;
    agentId: string;
    sessionId: string;
    handle: string;
    /** Changes whenever the turn may have ended (status, last activity). */
    turn: string;
    /** The question text when known (the main chat's last preview). */
    question?: string;
  };
  let { base, agentId, sessionId, handle, turn, question = "" }: Props = $props();

  let waiting = $state(false);
  $effect(() => {
    const key = `${agentId}|${sessionId}|${turn}`;
    let stale = false;
    runApi(getRemoteAgent(base, agentId, sessionId))
      .then((r) => { if (!stale) waiting = !!r.session?.input_required; })
      .catch(() => { if (!stale) waiting = false; });
    void key;
    return () => { stale = true; };
  });
</script>

{#if waiting}
  <div class="mx-4 mt-3 rounded-xl border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-500 dark:bg-navy-800 dark:text-amber-300" role="status" data-testid="remote-question">
    <p class="font-medium">❓ @{handle} is waiting for your answer</p>
    {#if question}<p class="mt-0.5 line-clamp-2 text-xs" data-testid="remote-question-text">{question}</p>{/if}
    <p class="mt-0.5 text-xs">Your next message goes to the same task.</p>
  </div>
{/if}
