<script lang="ts">
  /* Who posts a Slack remote agent's turns: the connector's bot, or "As me"
     through a Slack account the user connected themselves. The server
     lists only the caller's own accounts; with none, "As me" is offered
     disabled with the reason. */
  import { getSlackIdentities, runApi, type SlackIdentities, type SlackIdentity } from "../../api/team.js";

  type Props = {
    base: string;
    connectorId: string;
    identity: SlackIdentity;
    accountId: string;
    idPrefix: string;
  };
  let { base, connectorId, identity = $bindable(), accountId = $bindable(), idPrefix }: Props = $props();

  let ids = $state<SlackIdentities | null>(null);
  let error = $state("");
  const accounts = $derived(ids?.accounts ?? []);

  $effect(() => {
    const id = connectorId;
    ids = null;
    error = "";
    if (!id) return;
    runApi(getSlackIdentities(base, id))
      .then((r) => {
        if (id !== connectorId) return;
        ids = r;
        const own = r.accounts ?? [];
        // An account from another workspace, or none left: back to the bot.
        if (identity === "user" && !own.some((a) => a.id === accountId)) {
          if (own.length === 1) accountId = own[0].id;
          else if (own.length === 0) { identity = "bot"; accountId = ""; }
        }
      })
      .catch((e) => { error = e instanceof Error ? e.message : String(e); });
  });

  function pick(v: SlackIdentity) {
    identity = v;
    if (v === "bot") accountId = "";
    else if (!accountId && accounts.length > 0) accountId = accounts[0].id;
  }

  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
</script>

<fieldset class="space-y-2" data-testid="{idPrefix}-identity">
  <legend class={label}>Post as</legend>
  <label class="flex items-start gap-2 text-sm text-black-900 dark:text-white-100">
    <input type="radio" class="mt-1" name="{idPrefix}-identity" checked={identity === "bot"} onchange={() => pick("bot")} />
    <span>Bot<br /><span class="text-xs text-black-800 dark:text-black-600">The workspace's wick bot posts the messages.</span></span>
  </label>
  <label class="flex items-start gap-2 text-sm {accounts.length ? 'text-black-900 dark:text-white-100' : 'text-black-700 opacity-60'}">
    <input type="radio" class="mt-1" name="{idPrefix}-identity" checked={identity === "user"} disabled={accounts.length === 0} onchange={() => pick("user")} />
    <span>As me<br /><span class="text-xs text-black-800 dark:text-black-600">
      {#if !ids && !error}Checking your Slack accounts…{:else if accounts.length === 0}Connect your own Slack account on this connector first.{:else}Messages appear as you, through your connected Slack account.{/if}
    </span></span>
  </label>
  {#if identity === "user" && accounts.length > 1}
    <select class="ml-4 rounded-lg border border-white-300 bg-white-100 px-2 py-1 text-sm text-black-900 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100" aria-label="Slack account" bind:value={accountId}>
      {#each accounts as a (a.id)}<option value={a.id}>{a.display_name || a.id}</option>{/each}
    </select>
  {/if}
  {#if error}<p class="text-xs text-neg-400">{error}</p>{/if}
</fieldset>
