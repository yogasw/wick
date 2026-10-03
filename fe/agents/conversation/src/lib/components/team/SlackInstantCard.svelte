<script lang="ts">
  /* Connections › Slack › Instant: the agent answers through a Slack app
     wick already runs, as its own name and photo. Picking the app turns
     Instant on; every option after that autosaves. Custom and Instant are
     exclusive — the server answers 409 and the message is shown as-is. */
  import { Button, Toggle } from "@wick-fe/common-ui";
  import { AgentAvatar } from "@wick-fe/common-avatar";
  import { runApi, type AgentItem } from "../../api/team.js";
  import {
    listSlackInstantApps, updateAgentSlackInstant, disableAgentSlackInstant, rotateAgentSlackInstantAvatar,
    channelOf, appLabel, prefixExample, INSTANT_LIMITS,
    type AgentSlackInstantStatus, type AgentSlackInstantUpdate, type SlackInstantApp,
  } from "../../slackInstant.js";

  type Props = { base: string; agent: AgentItem; status: AgentSlackInstantStatus | null; customConnected: boolean };
  let { base, agent, status = $bindable(), customConnected }: Props = $props();

  let apps = $state<SlackInstantApp[] | null>(null);
  let error = $state("");
  let saveState = $state<"" | "saving" | "saved" | "error">("");
  let channelDraft = $state("");
  let channelError = $state("");
  let busy = $state(false);
  let rotated = $state(false);

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const muted = "text-xs text-black-800 dark:text-black-600";

  const enabled = $derived(!!status?.enabled);
  const app = $derived(apps?.find((a) => a.key === status?.shared_channel));
  const av = $derived(agent.avatar);

  async function loadApps() {
    try {
      apps = (await runApi(listSlackInstantApps(base))).apps ?? [];
    } catch (e) {
      apps = [];
      error = e instanceof Error ? e.message : String(e);
    }
  }

  async function save(patch: AgentSlackInstantUpdate): Promise<boolean> {
    saveState = "saving";
    error = "";
    try {
      status = await runApi(updateAgentSlackInstant(base, agent.id, patch));
      saveState = "saved";
      return true;
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
      saveState = "error";
      return false;
    }
  }

  function pickApp(key: string) {
    if (key && key !== status?.shared_channel) void save({ shared_channel: key });
  }

  async function addChannel() {
    const id = channelOf(channelDraft);
    if (!id) {
      channelError = "Paste a channel ID (C… or G…) or a link to the channel or a message in it.";
      return;
    }
    channelError = "";
    const have = status?.bound_channels ?? [];
    if (have.includes(id)) {
      channelDraft = "";
      return;
    }
    if (await save({ bound_channels: [...have, id] })) channelDraft = "";
  }

  function removeChannel(id: string) {
    void save({ bound_channels: (status?.bound_channels ?? []).filter((c) => c !== id) });
  }

  async function act(fn: () => Promise<void>) {
    busy = true;
    error = "";
    try {
      await fn();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  const rotate = () =>
    act(async () => {
      status = await runApi(rotateAgentSlackInstantAvatar(base, agent.id));
      rotated = true;
    });

  const turnOff = () =>
    act(async () => {
      await runApi(disableAgentSlackInstant(base, agent.id));
      status = { ...(status as AgentSlackInstantStatus), enabled: false, bound_channels: [], warnings: [] };
      saveState = "";
    });

  $effect(() => {
    void agent.id;
    rotated = false;
    void loadApps();
  });
</script>

<div class="mt-4 space-y-4" data-testid="slack-instant">
  <p class={muted}>Answer through a Slack app wick already runs — no app of your own. Replies show @{agent.handle}'s name and photo.</p>
  {#if saveState}<p class={muted} aria-live="polite" data-testid="instant-save">{saveState === "saving" ? "Saving…" : saveState === "saved" ? "Saved" : "Couldn't save"}</p>{/if}
  {#if customConnected}
    <p class="text-xs text-neg-400" data-testid="instant-exclusive">This agent has its own Slack app (Custom mode). Disconnect it under Custom app before switching to Instant — an agent answers through one Slack app at a time.</p>
  {/if}
  {#if error}<p class="text-xs text-neg-400" data-testid="instant-error">{error}</p>{/if}

  <div>
    <label class={label} for="si-app">Shared Slack app</label>
    {#if apps && !apps.length}
      <p class={muted} data-testid="instant-no-apps">No shared Slack app is available to you. Ask an admin to connect wick's Slack app first.</p>
    {:else}
      <select id="si-app" class={input} disabled={!apps || customConnected} value={status?.shared_channel ?? ""} onchange={(e) => pickApp((e.currentTarget as HTMLSelectElement).value)}>
        {#if !status?.shared_channel}<option value="">Pick an app…</option>{/if}
        {#each apps ?? [] as a (a.key)}<option value={a.key}>{appLabel(a)}</option>{/each}
      </select>
    {/if}
  </div>

  {#if enabled && status}
    <div>
      <span class={label}>How it looks in Slack</span>
      <div class="flex items-start gap-2 rounded-lg border border-white-300 p-3 dark:border-navy-600" data-testid="instant-preview">
        {#if av}<AgentAvatar kind={av.kind} shape={av.shape} expression={av.expression} color={av.color} size={36} still />{/if}
        <span class="min-w-0">
          <span class="text-sm font-semibold text-black-900 dark:text-white-100" data-testid="instant-username">{status.username || agent.handle}</span>
          <span class="ml-1 rounded bg-white-200 px-1 py-0.5 text-[10px] font-medium uppercase text-black-800 dark:bg-navy-600 dark:text-black-600">App</span>
          <span class="block text-sm text-black-900 dark:text-white-100">Here's the summary you asked for…</span>
        </span>
      </div>
      {#if !status.avatar_ready}<p class="mt-1 {muted}">wick has no public URL, so Slack shows :robot_face: instead of the photo.</p>{/if}
      <div class="mt-2 flex flex-wrap items-center gap-2">
        <Button size="sm" variant="ghost" disabled={busy} onclick={rotate}>Rotate avatar link</Button>
        <span class={muted}>{rotated ? "New link issued — the old one no longer works." : "Issues a new private image link for the photo; the old one stops working."}</span>
      </div>
    </div>

    <div data-testid="instant-scope" data-scope={status.customize_scope}>
      {#if status.customize_scope === "ok"}
        <p class="text-xs text-black-900 dark:text-white-100">✅ <span class="font-mono">chat:write.customize</span> granted — replies carry the agent's name and photo.</p>
      {:else if status.customize_scope === "missing"}
        <div class="rounded-lg border border-neg-400 p-2 text-xs text-black-900 dark:text-white-100">
          <p>⚠️ The shared app lacks <span class="font-mono">chat:write.customize</span>. Replies still go out, but as the shared bot's own name and photo.</p>
          <p class="mt-1 {muted}">To fix: on the app's page at api.slack.com, add <span class="font-mono">chat:write.customize</span> under OAuth &amp; Permissions › Bot Token Scopes, then reinstall the app to the workspace.</p>
        </div>
      {:else}
        <p class={muted}>⚠️ Couldn't check <span class="font-mono">chat:write.customize</span> — the shared app may be offline.</p>
      {/if}
    </div>
    {#if status.warnings?.length}
      <ul class="space-y-1 text-xs text-black-900 dark:text-white-100" data-testid="instant-warnings">
        {#each status.warnings as w (w)}<li>⚠️ {w}</li>{/each}
      </ul>
    {/if}

    <div>
      <span class={label}>Bound channels</span>
      <p class={muted}>Messages that mention the shared bot in these channels are answered by @{agent.handle}. One channel answers as one agent.</p>
      {#if status.bound_channels.length}
        <ul class="mt-2 flex flex-wrap gap-2" data-testid="instant-channels">
          {#each status.bound_channels as c (c)}
            <li class="inline-flex items-center gap-1 rounded bg-white-200 px-2 py-0.5 font-mono text-xs text-black-900 dark:bg-navy-600 dark:text-white-100">
              {c}<button type="button" class="text-black-800 hover:text-neg-400 dark:text-black-600" aria-label="Unbind {c}" onclick={() => removeChannel(c)}>×</button>
            </li>
          {/each}
        </ul>
      {/if}
      <div class="mt-2 flex gap-2">
        <input class="{input} font-mono" aria-label="Channel ID or link" placeholder="C0123ABCD or https://….slack.com/archives/C0123ABCD" bind:value={channelDraft} onkeydown={(e) => e.key === "Enter" && addChannel()} />
        <Button size="sm" disabled={!channelDraft.trim()} onclick={addChannel}>Bind</Button>
      </div>
      {#if channelError}<p class="mt-1 text-xs text-neg-400" data-testid="channel-error">{channelError}</p>{/if}
      <p class="mt-1 {muted}">In Slack: right-click the channel › Copy link, or open its details — the ID is at the bottom. Invite the shared bot to the channel.</p>
    </div>

    <div class="flex items-start gap-3">
      <Toggle checked={status.prefix_enabled} onChange={(v: boolean) => save({ prefix_enabled: v })} label="Answer to its handle" describedBy="si-prefix-hint" />
      <span class="min-w-0">
        <span class="block text-sm text-black-900 dark:text-white-100">Answer to its handle</span>
        <span id="si-prefix-hint" class="block {muted}">In any other channel, or a DM to the shared bot, call it with <span class="font-mono" data-testid="instant-prefix-example">{prefixExample(app?.bot_name, agent.handle)}</span>. The thread then stays with @{agent.handle}.</span>
      </span>
    </div>

    <div>
      <span class={label}>Limits</span>
      <ul class="list-disc space-y-1 pl-4 {muted}" data-testid="instant-limits">
        {#each INSTANT_LIMITS as l (l)}<li>{l}</li>{/each}
      </ul>
    </div>

    <div><Button size="sm" variant="ghost" disabled={busy} onclick={turnOff}>Turn off Instant</Button></div>
  {/if}
</div>
