<script lang="ts">
  /* How wick tells a Slack reply is the answer: whose replies count, the
     END RESPONSE marker, and how long it waits (0 = server default). */
  import type { SlackListen } from "../../api/team.js";
  import { LISTEN_OPTIONS, IDLE_DEFAULT, GRACE_DEFAULT, MAX_DEFAULT, SEC_CAP, secError } from "../../slackRemote.js";

  type Props = { listen: SlackListen; marker: boolean; mention: boolean; idleSec: number; maxSec: number; pollSec?: number; graceSec?: number; idPrefix: string };
  let { listen = $bindable(), marker = $bindable(), mention = $bindable(), idleSec = $bindable(), maxSec = $bindable(), pollSec = $bindable(0), graceSec = $bindable(0), idPrefix }: Props = $props();

  const why = $derived(secError(idleSec, maxSec));
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
</script>

<div class="space-y-4">
  <fieldset class="space-y-2">
    <legend class={label}>Listen to</legend>
    {#each LISTEN_OPTIONS as o (o.value)}
      <label class="flex items-start gap-2 text-sm text-black-900 dark:text-white-100">
        <input type="radio" class="mt-1" name="{idPrefix}-listen" checked={listen === o.value} onchange={() => (listen = o.value)} />
        <span>{o.label}<br /><span class="text-xs text-black-800 dark:text-black-600">{o.hint}</span></span>
      </label>
    {/each}
  </fieldset>

  <label class="flex items-start gap-2 text-sm text-black-900 dark:text-white-100">
    <input type="checkbox" class="mt-1" bind:checked={marker} data-testid="{idPrefix}-marker" />
    <span>END RESPONSE marker<br /><span class="text-xs text-black-800 dark:text-black-600">
      {marker ? "Each turn asks the agent to end its reply with a marker line; the turn ends there." : "No marker: the turn ends once the reply has been quiet for the idle time."}
    </span></span>
  </label>

  <label class="flex items-start gap-2 text-sm text-black-900 dark:text-white-100">
    <input type="checkbox" class="mt-1" bind:checked={mention} data-testid="{idPrefix}-mention-target" />
    <span>Always @mention the target<br /><span class="text-xs text-black-800 dark:text-black-600">
      {mention ? "Each message starts with @target so bots that only answer mentions are triggered." : "Messages are posted as written, with no @mention."}
    </span></span>
  </label>

  <div class="grid grid-cols-2 gap-3">
    <div>
      <label class={label} for="{idPrefix}-idle">Idle (seconds)</label>
      <input id="{idPrefix}-idle" type="number" min="0" max={SEC_CAP} class={input} bind:value={idleSec} />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">Quiet time that ends a reply. 0 = {IDLE_DEFAULT} s.</p>
    </div>
    <div>
      <label class={label} for="{idPrefix}-max">Max (seconds)</label>
      <input id="{idPrefix}-max" type="number" min="0" max={SEC_CAP} class={input} bind:value={maxSec} />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">Longest wait for a turn. 0 = {MAX_DEFAULT} s.</p>
    </div>
    <div>
      <label class={label} for="{idPrefix}-poll">Poll every (seconds)</label>
      <input id="{idPrefix}-poll" type="number" min="0" max={SEC_CAP} class={input} bind:value={pollSec} />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">Longest gap between thread reads when no Slack event arrives. 0 = up to 10 s.</p>
    </div>
    <div>
      <label class={label} for="{idPrefix}-grace">Late replies (seconds)</label>
      <input id="{idPrefix}-grace" type="number" min="-1" max={SEC_CAP} class={input} bind:value={graceSec} />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">How long a message or edit sent after the reply ended is still passed on. 0 = {GRACE_DEFAULT} s, -1 = off.</p>
    </div>
  </div>
  {#if why}<p class="text-xs text-neg-400">{why}</p>{/if}
</div>
