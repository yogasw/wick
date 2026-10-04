<script lang="ts">
  /* Connections drawer (⋯ → Connections): one icon tab per channel —
     Slack, Telegram, A2A, REST — marked ✓ when live and ⚠ when it needs a
     look. Every card stays mounted (hidden when not picked) so each keeps
     loading its own status and the marks are right before a tab is opened.
     Slack: a three-step wizard while not connected, then the live card —
     options autosave, the health matrix shows what the app still lacks.
     Secrets are write-only: the server only says whether each is set. */
  import { Button, Toggle } from "@wick-fe/common-ui";
  import { toastOk } from "@wick-fe/common-stores";
  import DrawerHeader from "./DrawerHeader.svelte";
  import ConnectionIcon from "./ConnectionIcon.svelte";
  import ConnectionA2ACard from "./team/ConnectionA2ACard.svelte";
  import ConnectionRESTCard from "./team/ConnectionRESTCard.svelte";
  import ConnectionTelegramCard from "./team/ConnectionTelegramCard.svelte";
  import SlackInstantCard from "./team/SlackInstantCard.svelte";
  import SlackSettingsGroups from "./team/SlackSettingsGroups.svelte";
  import {
    getAgentSlack, connectAgentSlack, updateAgentSlack, disconnectAgentSlack, getAgentSlackHealth, getAgentSlackManifest, runApi,
    type AgentItem, type AgentSlackStatus, type AgentSlackHealth,
  } from "../api/team.js";
  import { MATRIX_ICON, MATRIX_LABEL, MASKED, eventsSourceNote, connectBody, statusLine, tokenError, type TokenDraft, TOKEN_HINTS } from "../slackConnection.js";
  import { getAgentSlackInstant, instantStatusLine, type AgentSlackInstantStatus } from "../slackInstant.js";
  import { CONN_TABS, CONN_MARK, CONN_MARK_LABEL, connState, defaultConnTab, type ConnTab, type ConnState } from "../connectionTabs.js";

  /* tab: the `conn=` from the URL; null = the default (first connected,
     else Slack). onTab writes the pick back to the URL. */
  type Props = { base: string; agent: AgentItem; onClose: () => void; tab?: ConnTab | null; onTab?: (t: ConnTab) => void };
  let { base, agent, onClose, tab = null, onTab }: Props = $props();

  let status = $state<AgentSlackStatus | null>(null);
  let loadError = $state("");
  let step = $state(1);
  let wizard = $state(false);
  let draft = $state<TokenDraft>({ mode: "socket", bot_token: "", app_token: "", signing_secret: "" });
  let busy = $state(false);
  let error = $state("");
  let health = $state<AgentSlackHealth | null>(null);
  let healthError = $state("");
  let manifest = $state<{ json: string; url: string } | null>(null);
  let copied = $state(false);
  let saveState = $state<"" | "saving" | "saved" | "error">("");
  /* Custom = the agent's own Slack app; Instant = a persona on a shared
     app. The two are exclusive, so the card shows one at a time. */
  let slackMode = $state<"custom" | "instant">("custom");
  let instant = $state<AgentSlackInstantStatus | null>(null);

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 font-mono text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const muted = "text-xs text-black-800 dark:text-black-600";
  /* Header line + open badge, fed by SlackSettingsGroups once it loads. */
  let accessLine = $state("");
  let accessOpen = $state(false);

  async function load() {
    try {
      [status, instant] = await Promise.all([
        runApi(getAgentSlack(base, agent.id)),
        runApi(getAgentSlackInstant(base, agent.id)).catch(() => null),
      ]);
      loadError = "";
      slackMode = instant?.enabled && !status.connected ? "instant" : "custom";
      wizard = !status.connected;
      if (status.connected) draft.mode = status.mode;
      if (status.connected && status.online) void runHealth();
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  }

  async function loadManifest() {
    try {
      const m = await runApi(getAgentSlackManifest(base, agent.id));
      manifest = { json: JSON.stringify(m.manifest, null, 2), url: m.create_url };
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function copyManifest() {
    if (!manifest) await loadManifest();
    if (!manifest) return;
    await navigator.clipboard?.writeText(manifest.json);
    copied = true;
    setTimeout(() => (copied = false), 1500);
  }

  async function openCreate() {
    if (!manifest) await loadManifest();
    if (manifest) window.open(manifest.url, "_blank", "noopener");
  }

  async function runHealth() {
    healthError = "";
    try {
      health = await runApi(getAgentSlackHealth(base, agent.id));
    } catch (e) {
      health = null;
      healthError = e instanceof Error ? e.message : String(e);
    }
  }

  const tokenProblem = $derived(tokenError(draft, status));

  let marks = $state<Partial<Record<ConnTab, ConnState>>>({});
  /* Slack is live through whichever app answers: its own, else Instant. */
  const slackState = $derived(
    status?.connected
      ? connState(true, status.online && !status.disabled, !!loadError)
      : connState(!!instant?.enabled, !!instant?.shared_online, !!loadError),
  );
  const states = $derived({ ...marks, slack: slackState });
  let picked = $state<ConnTab | null>(null);
  const active = $derived(tab ?? picked ?? defaultConnTab(states));

  function pick(t: ConnTab) {
    picked = t;
    onTab?.(t);
  }

  function tabKey(e: KeyboardEvent, i: number) {
    const step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    if (!step) return;
    e.preventDefault();
    const next = CONN_TABS[(i + step + CONN_TABS.length) % CONN_TABS.length].key;
    pick(next);
    document.getElementById(`conn-tab-${next}`)?.focus();
  }

  async function connect() {
    if (tokenProblem) return;
    busy = true;
    error = "";
    try {
      status = await runApi(connectAgentSlack(base, agent.id, connectBody(draft)));
      draft.bot_token = draft.app_token = draft.signing_secret = "";
      wizard = false;
      toastOk(`@${agent.handle} is connected to Slack`);
      await runHealth();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  async function setDMMain(v: boolean) {
    if (!status) return;
    const prev = status.dm_main_chat;
    status.dm_main_chat = v;
    saveState = "saving";
    try {
      status = await runApi(updateAgentSlack(base, agent.id, { dm_main_chat: v }));
      saveState = "saved";
    } catch {
      status.dm_main_chat = prev;
      saveState = "error";
    }
  }

  async function disconnect() {
    busy = true;
    try {
      await runApi(disconnectAgentSlack(base, agent.id));
      health = null;
      await load();
      step = 1;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  $effect(() => {
    void agent.id;
    picked = null;
    marks = {};
    void load();
  });
</script>

<DrawerHeader title="Connections" subtitle="@{agent.handle}" avatar={agent.avatar} {onClose} />
<div class="flex shrink-0 gap-1 overflow-x-auto border-b border-white-300 px-4 dark:border-navy-600" role="tablist" aria-label="Connections" data-testid="conn-tabs">
  {#each CONN_TABS as t, i (t.key)}
    {@const st = states[t.key] ?? "off"}
    <button
      type="button"
      role="tab"
      id="conn-tab-{t.key}"
      aria-controls="conn-panel-{t.key}"
      aria-selected={active === t.key}
      tabindex={active === t.key ? 0 : -1}
      class="-mb-px inline-flex shrink-0 items-center gap-2 whitespace-nowrap border-b-2 px-3 py-2.5 text-sm {active === t.key ? 'border-green-500 font-medium text-black-900 dark:text-white-100' : 'border-transparent text-black-800 hover:text-black-900 dark:text-black-600 dark:hover:text-white-100'}"
      data-testid="conn-tab-{t.key}"
      data-state={st}
      onclick={() => pick(t.key)}
      onkeydown={(e) => tabKey(e, i)}
    >
      <span class="relative inline-flex">
        <ConnectionIcon kind={t.key} />
        {#if CONN_MARK[st]}
          <span class="absolute -right-1.5 -top-1.5 inline-flex h-3.5 w-3.5 items-center justify-center rounded-full text-[9px] font-bold leading-none text-white-100 ring-2 ring-white-100 dark:ring-navy-700 {st === 'on' ? 'bg-green-500' : 'bg-yellow-500'}" data-testid="conn-mark-{t.key}" aria-hidden="true">{CONN_MARK[st]}</span>
        {/if}
      </span>
      {t.label}<span class="sr-only"> — {CONN_MARK_LABEL[st]}</span>
    </button>
  {/each}
</div>
<div class="flex-1 overflow-y-auto px-6 py-5" data-testid="agent-connections">
  <div class="space-y-5" role="tabpanel" id="conn-panel-slack" aria-labelledby="conn-tab-slack" hidden={active !== "slack"}>
  {#if loadError}
    <p class="text-sm text-neg-400">{loadError}</p>
  {/if}
  <section class="rounded-xl border border-white-300 p-4 dark:border-navy-600" data-testid="slack-card">
    <div class="flex items-center gap-3">
      <span class="text-sm font-semibold text-black-900 dark:text-white-100">Slack</span>
      <span class="rounded-full px-2 py-0.5 text-xs {(slackMode === "instant" ? instant?.enabled && instant.shared_online : status?.online) ? 'bg-green-50 text-green-700' : 'bg-white-200 text-black-800 dark:bg-navy-600 dark:text-black-600'}">
        {slackMode === "instant" ? instantStatusLine(instant) : statusLine(status)}
      </span>
      {#if slackMode === "custom" && status?.connected && accessLine}
        <span class={muted} data-testid="slack-access-summary">{accessLine}</span>
        {#if accessOpen}<span class="rounded-full bg-neg-50 px-2 py-0.5 text-xs text-neg-400" data-testid="slack-open-badge">Open to everyone in the workspace</span>{/if}
      {/if}
      {#if saveState && slackMode === "custom"}<span class="ml-auto {muted}" aria-live="polite">{saveState === "saving" ? "Saving…" : saveState === "saved" ? "Saved" : "Couldn't save"}</span>{/if}
    </div>
    <div class="mt-3 inline-flex rounded-lg border border-white-300 p-0.5 dark:border-navy-600" role="group" aria-label="Slack mode">
      {#each [["custom", "Custom app"], ["instant", "Instant (shared app)"]] as [m, lbl] (m)}
        <button type="button" class="rounded-md px-3 py-1 text-xs {slackMode === m ? 'bg-green-500 text-white-100' : 'text-black-800 dark:text-black-600'}" aria-pressed={slackMode === m} onclick={() => (slackMode = m as typeof slackMode)}>{lbl}</button>
      {/each}
    </div>

    {#if slackMode === "instant"}
      <SlackInstantCard {base} {agent} bind:status={instant} customConnected={!!status?.connected} />
    {:else if instant?.enabled}
      <p class="mt-3 text-xs text-neg-400" data-testid="custom-exclusive">@{agent.handle} answers in Slack as an Instant agent. Turn off Instant before connecting its own app — an agent answers through one Slack app at a time.</p>
    {:else}
    <p class="mt-1 {muted}">One Slack app per agent: its name and photo are the agent's own.</p>

    {#if wizard}
      <ol class="mt-4 space-y-4" data-testid="slack-wizard">
        <li class={step === 1 ? "" : "opacity-60"}>
          <p class="text-sm font-medium text-black-900 dark:text-white-100">1. Create the Slack app</p>
          <p class="mt-1 {muted}">Opens Slack with a manifest made for @{agent.handle} — scopes, events and agent view included. Pick your workspace, create, then install it.</p>
          <div class="mt-2 flex flex-wrap gap-2">
            <Button size="sm" onclick={openCreate}>Create the Slack app ↗</Button>
            <Button size="sm" variant="ghost" onclick={copyManifest}>{copied ? "Copied" : "Copy manifest"}</Button>
            <Button size="sm" variant="ghost" onclick={() => (step = 2)}>Next</Button>
          </div>
        </li>
        <li class={step === 2 ? "" : "opacity-60"}>
          <p class="text-sm font-medium text-black-900 dark:text-white-100">2. Paste the tokens</p>
          <div class="mt-2 space-y-3">
            <div>
              <span class={label}>Connection mode</span>
              <div class="inline-flex rounded-lg border border-white-300 p-0.5 dark:border-navy-600" role="group" aria-label="Connection mode">
                {#each [["socket", "Socket (recommended)"], ["http", "HTTP"]] as [m, lbl] (m)}
                  <button type="button" class="rounded-md px-3 py-1 text-xs {draft.mode === m ? 'bg-green-500 text-white-100' : 'text-black-800 dark:text-black-600'}" aria-pressed={draft.mode === m} onclick={() => (draft.mode = m as TokenDraft["mode"])}>{lbl}</button>
                {/each}
              </div>
            </div>
            <div>
              <label class={label} for="sl-bot">Bot token (xoxb-…)</label>
              <input id="sl-bot" type="password" autocomplete="off" class={input} bind:value={draft.bot_token} placeholder={status?.secrets?.bot_token ? MASKED : "xoxb-…"} onfocus={() => (step = Math.max(step, 2))} />
              <p class="mt-1 {muted}" data-testid="hint-bot">{TOKEN_HINTS.bot_token}</p>
            </div>
            {#if draft.mode === "socket"}
              <div>
                <label class={label} for="sl-app">App token (xapp-…)</label>
                <input id="sl-app" type="password" autocomplete="off" class={input} bind:value={draft.app_token} placeholder={status?.secrets?.app_token ? MASKED : "xapp-…"} />
                <p class="mt-1 {muted}" data-testid="hint-app">{TOKEN_HINTS.app_token}</p>
              </div>
            {:else}
              <div>
                <label class={label} for="sl-sign">Signing secret</label>
                <input id="sl-sign" type="password" autocomplete="off" class={input} bind:value={draft.signing_secret} placeholder={status?.secrets?.signing_secret ? MASKED : ""} />
                <p class="mt-1 {muted}" data-testid="hint-sign">{TOKEN_HINTS.signing_secret}</p>
              </div>
            {/if}
          </div>
        </li>
        <li>
          <p class="text-sm font-medium text-black-900 dark:text-white-100">3. Connect and test</p>
          {#if tokenProblem}<p class="mt-1 {muted}" data-testid="token-problem">{tokenProblem}</p>{/if}
          {#if error}<p class="mt-1 text-xs text-neg-400" data-testid="connect-error">{error}</p>{/if}
          <div class="mt-2 flex gap-2">
            <Button size="sm" disabled={busy || !!tokenProblem} onclick={connect}>{busy ? "Connecting…" : "Connect & test"}</Button>
            {#if status?.connected}<Button size="sm" variant="ghost" onclick={() => (wizard = false)}>Cancel</Button>{/if}
          </div>
        </li>
      </ol>
    {:else if status?.connected}
      <div class="mt-4 space-y-4">
        <div class="flex items-start gap-3">
          <Toggle checked={status.dm_main_chat} onChange={setDMMain} label="DMs continue the sender's main chat" describedBy="sl-dm-hint" />
          <span class="min-w-0">
            <span class="block text-sm text-black-900 dark:text-white-100">DMs continue the sender's main chat</span>
            <span id="sl-dm-hint" class="block {muted}">Off: every DM thread is its own chat. Channel threads and mentions always get their own chat; replies go back where the message came from.</span>
          </span>
        </div>
        <div class="flex flex-wrap items-center gap-2 {muted}">
          <span>Secrets:</span>
          {#each [["bot_token", "bot token"], ["app_token", "app token"], ["signing_secret", "signing secret"]] as [k, lbl] (k)}
            {#if status.secrets?.[k as "bot_token"]}<span class="rounded bg-white-200 px-1.5 py-0.5 font-mono dark:bg-navy-600" data-testid="secret-{k}">{lbl} {MASKED}</span>{/if}
          {/each}
        </div>
        <div class="flex flex-wrap gap-2">
          <Button size="sm" variant="ghost" onclick={runHealth}>Test again</Button>
          <Button size="sm" variant="ghost" onclick={() => { wizard = true; step = 2; }}>Replace tokens</Button>
          <Button size="sm" variant="ghost" onclick={copyManifest}>{copied ? "Copied" : "Regenerate manifest"}</Button>
          <Button size="sm" variant="ghost" disabled={busy} onclick={disconnect}>Disconnect</Button>
        </div>
        {#if manifest}<p class={muted}>Paste it under App Manifest on your Slack app's page, save, then reinstall the app.</p>{/if}
      </div>
      <SlackSettingsGroups {base} {agent} onSummary={(line, open) => { accessLine = line; accessOpen = open; }} />
    {/if}
    {/if}
  </section>

  {#if slackMode === "custom" && status?.connected && !wizard}
    <section data-testid="slack-health">
      <p class="text-sm font-semibold text-black-900 dark:text-white-100">Health</p>
      {#if healthError}<p class="mt-1 text-xs text-neg-400">{healthError}</p>{/if}
      {#if health?.matrix}
        <table class="mt-2 w-full text-left text-xs" data-testid="slack-matrix">
          <thead><tr class="text-black-800 dark:text-black-600"><th class="py-1 font-medium">Feature</th><th class="py-1 font-medium">Scopes</th><th class="py-1 font-medium">Events</th></tr></thead>
          <tbody>
            {#each health.matrix as row (row.key)}
              <tr class="border-t border-white-300 align-top dark:border-navy-600" data-status={row.status}>
                <td class="py-1.5 pr-2 text-black-900 dark:text-white-100"><span aria-label={MATRIX_LABEL[row.status]}>{MATRIX_ICON[row.status]}</span> {row.label}</td>
                {#each [row.scopes, row.events] as items, col (col)}
                  <td class="py-1.5 pr-2">
                    {#each items as it (it.name)}
                      <span class="block font-mono text-black-900 dark:text-white-100" title={it.hint ?? ""}>{MATRIX_ICON[it.status]} {it.name}</span>
                      {#if it.hint && it.status !== "ok"}<span class="block {muted}">{it.hint}</span>{/if}
                    {/each}
                  </td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
        <p class="mt-1 {muted}" data-testid="events-source">{eventsSourceNote(health.matrix[0]?.events_from)}</p>
      {/if}
      {#if health?.checks?.length}
        <ul class="mt-3 space-y-1 text-xs">
          {#each health.checks as c (c.name)}
            <li class="text-black-900 dark:text-white-100">{c.ok ? "✅" : "❌"} {c.name}{#if c.error}<span class="text-neg-400"> — {c.error}</span>{/if}</li>
          {/each}
        </ul>
      {/if}
    </section>
  {/if}

  </div>

  <div role="tabpanel" id="conn-panel-telegram" aria-labelledby="conn-tab-telegram" hidden={active !== "telegram"}>
    <ConnectionTelegramCard {base} {agent} onStatus={(s) => (marks.telegram = s)} />
  </div>
  <div role="tabpanel" id="conn-panel-a2a" aria-labelledby="conn-tab-a2a" hidden={active !== "a2a"}>
    <ConnectionA2ACard {base} {agent} onStatus={(s) => (marks.a2a = s)} />
  </div>
  <div role="tabpanel" id="conn-panel-rest" aria-labelledby="conn-tab-rest" hidden={active !== "rest"}>
    <ConnectionRESTCard {base} {agent} onStatus={(s) => (marks.rest = s)} />
  </div>
</div>
