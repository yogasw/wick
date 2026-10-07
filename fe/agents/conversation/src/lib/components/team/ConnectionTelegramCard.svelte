<script lang="ts">
  /* Connections › Telegram: the agent's own bot. The BotFather token is
     pasted once and cleared from the field as soon as it is sent; the card
     only ever shows the bot's username and link. */
  import { Button } from "@wick-fe/common-ui";
  import { runApi, type AgentItem } from "../../api/team.js";
  import { connState, type ConnState } from "../../connectionTabs.js";
  import {
    getAgentTelegram, connectAgentTelegram, testAgentTelegram, disconnectAgentTelegram,
    type AgentTelegramStatus, type TelegramTestResult,
  } from "../../telegramConnection.js";

  /* onStatus feeds the drawer's tab mark (✓ / ⚠). */
  type Props = { base: string; agent: AgentItem; onStatus?: (s: ConnState) => void };
  let { base, agent, onStatus }: Props = $props();

  let status = $state<AgentTelegramStatus | null>(null);
  let error = $state("");
  let token = $state("");
  let busy = $state(false);
  let result = $state<TelegramTestResult | null>(null);

  const muted = "text-xs text-black-800 dark:text-black-600";
  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 font-mono text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";

  const msg = (e: unknown) => (e instanceof Error ? e.message : String(e));

  async function load() {
    try {
      status = await runApi(getAgentTelegram(base, agent.id));
      error = "";
    } catch (e) {
      error = msg(e);
    }
  }

  async function connect() {
    const t = token.trim();
    if (!t) return;
    busy = true;
    error = "";
    result = null;
    try {
      status = await runApi(connectAgentTelegram(base, agent.id, t));
    } catch (e) {
      error = msg(e);
    } finally {
      token = "";
      busy = false;
    }
  }

  async function runTest() {
    busy = true;
    result = null;
    try {
      result = await runApi(testAgentTelegram(base, agent.id));
    } catch (e) {
      result = { ok: false, detail: msg(e) };
    } finally {
      busy = false;
    }
  }

  async function disconnect() {
    busy = true;
    result = null;
    try {
      await runApi(disconnectAgentTelegram(base, agent.id));
      await load();
    } catch (e) {
      error = msg(e);
    } finally {
      busy = false;
    }
  }

  $effect(() => {
    void agent.id;
    result = null;
    token = "";
    void load();
  });

  const badge = $derived(
    !status?.connected ? "Not connected" : status.disabled ? "Agent disabled" : status.online ? "Online" : "Offline",
  );

  $effect(() => onStatus?.(connState(!!status?.connected, !!status?.online && !status.disabled, !!error)));
</script>

<section class="rounded-xl border border-white-300 p-4 dark:border-navy-600" data-testid="telegram-card">
  <div class="flex items-center gap-3">
    <span class="text-sm font-semibold text-black-900 dark:text-white-100">Telegram</span>
    <span class="rounded-full px-2 py-0.5 text-xs {status?.connected && status.online ? 'bg-green-50 text-green-700' : 'bg-white-200 text-black-800 dark:bg-navy-600 dark:text-black-600'}" data-testid="telegram-badge">{badge}</span>
  </div>
  <p class="mt-1 {muted}">Give @{agent.handle} its own Telegram bot. Messages to the bot are answered by this agent with its persona and access; action cards arrive as numbered choices.</p>
  {#if error}<p class="mt-2 text-xs text-neg-400" data-testid="telegram-error">{error}</p>{/if}

  {#if status && !status.connected}
    <div class="mt-4 space-y-2">
      <label for="tg-token-{agent.id}" class="block {muted}">Bot token from @BotFather</label>
      <input id="tg-token-{agent.id}" type="password" autocomplete="off" class={input} bind:value={token} placeholder="123456:ABC…" data-testid="telegram-token" />
      <p class={muted}>Create a bot with <a href="https://t.me/BotFather" target="_blank" rel="noopener" class="font-medium underline hover:no-underline">@BotFather</a> (/newbot) and paste its token. One bot serves one agent; the token is stored encrypted and never shown again.</p>
      <Button size="sm" disabled={busy || !token.trim()} onclick={connect}>{busy ? "Connecting…" : "Connect"}</Button>
    </div>
  {:else if status?.connected}
    <div class="mt-4 space-y-3">
      <p class="text-sm text-black-900 dark:text-white-100">
        Bot
        {#if status.link}<a href={status.link} target="_blank" rel="noopener" class="font-mono font-medium underline hover:no-underline" data-testid="telegram-link">@{status.bot_username}</a>{:else}<span class="font-mono">{status.bot_id}</span>{/if}
      </p>
      <div class="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="ghost" disabled={busy} onclick={runTest}>Test</Button>
        <Button size="sm" variant="ghost" disabled={busy} onclick={disconnect}>Disconnect</Button>
        {#if result}<span class="text-xs {result.ok ? 'text-green-700' : 'text-neg-400'}" data-testid="telegram-test">{result.ok ? "OK" : "Not reachable"} — {result.detail}</span>{/if}
      </div>
    </div>
  {/if}
</section>
