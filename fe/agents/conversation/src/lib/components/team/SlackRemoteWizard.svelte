<script lang="ts">
  /* + Agent › Remote agent › Slack: an agent reached through Slack joins
     the roster. wick posts each turn to a DM, channel or thread and reads
     the reply back. Five steps:
     1 Workspace — the Slack connector (one visible to the user).
     2 Target — DM, channel or thread.
     3 Identity — the bot, or "As me" with the user's own Slack account.
     4 Listen — whose replies count, the END RESPONSE marker, idle/max.
     5 Finish — name, Test (a real "ping"), who may use it, and the
       warning that messages leave wick for Slack. */
  import { onMount } from "svelte";
  import RemoteSourcePicker from "./RemoteSourcePicker.svelte";
  import SlackTargetFields from "./SlackTargetFields.svelte";
  import SlackIdentityFields from "./SlackIdentityFields.svelte";
  import SlackListenFields from "./SlackListenFields.svelte";
  import {
    createSlackRemote, listAgentConnectors, testSlackRemote, runApi,
    type AgentConnector, type AgentItem, type SlackIdentity, type SlackListen,
    type SlackRemoteConfig, type SlackTarget, type SlackTestResult,
  } from "../../api/team.js";
  import { HANDLE_RE, TAGLINE_MAX } from "../../agentForm.js";
  import { cleanConfig, configError, secError, slackTestSummary, slackWarning, targetLabel } from "../../slackRemote.js";

  type Props = {
    base: string;
    taken: string[];
    onClose: () => void;
    onCreated: (a: AgentItem) => void;
    /** Back to the wick-agent wizard, or to another remote source. */
    onType?: (t: "local" | "remote" | "slack" | "plugin") => void;
  };
  let { base, taken, onClose, onCreated, onType }: Props = $props();

  const STEPS = ["Workspace", "Target", "Identity", "Listen", "Finish"];
  let step = $state(1);

  let connectors = $state<AgentConnector[] | null>(null);
  let loadError = $state("");
  let connectorId = $state("");

  let target = $state<SlackTarget>("dm");
  let channel = $state("");
  let user = $state("");
  let mentionId = $state("");
  let threadTs = $state("");
  let targetName = $state("");

  let identity = $state<SlackIdentity>("bot");
  let accountId = $state("");

  let listen = $state<SlackListen>("target");
  let marker = $state(true);
  let mention = $state(true);
  let idleSec = $state(0);
  let maxSec = $state(0);

  let name = $state("");
  let handle = $state("");
  let tagline = $state("");

  let testing = $state(false);
  let testResult = $state<SlackTestResult | null>(null);
  let testError = $state("");
  let saving = $state(false);
  let error = $state("");
  let handleError = $state("");

  onMount(() => {
    runApi(listAgentConnectors(base))
      .then((all) => {
        connectors = (all ?? []).filter((c) => c.key === "slack" && !c.tool);
        if (connectors.length === 1) connectorId = connectors[0].id;
      })
      .catch((e) => { loadError = e instanceof Error ? e.message : String(e); connectors = []; });
  });

  const workspace = $derived(connectors?.find((c) => c.id === connectorId)?.label ?? "");
  const cfg = $derived<SlackRemoteConfig>(
    cleanConfig({
      connector_id: connectorId, identity, account_id: accountId, target, channel, user,
      mention_id: mentionId, thread_ts: threadTs, target_name: targetName,
      listen, marker, mention_target: mention, idle_sec: idleSec, max_sec: maxSec,
    }),
  );
  const where = $derived(targetLabel(cfg));

  // What blocks Next on each step ("" = may go on).
  const stepError = $derived.by(() => {
    if (step === 1) return connectorId ? "" : "Pick a Slack workspace.";
    if (step === 2) return configError({ ...cfg, identity: "bot", idle_sec: 0, max_sec: 0 });
    if (step === 3) return identity === "user" && !accountId ? "Pick your Slack account to post as you." : "";
    if (step === 4) return secError(idleSec, maxSec);
    return configError(cfg);
  });

  const handleTyped = $derived(handle.trim() !== "");
  const handleWhy = $derived(
    !handleTyped ? "" : !HANDLE_RE.test(handle) ? 'Lowercase letters, digits and "-", 2–31 characters, starting with a letter or digit.' : taken.includes(handle) ? `@${handle} is already taken by another agent.` : "",
  );

  // Any edit after a Test makes its result stale.
  $effect(() => {
    void JSON.stringify(cfg);
    testResult = null;
    testError = "";
  });

  async function test() {
    if (configError(cfg) || testing) return;
    testing = true;
    testError = "";
    const sent = JSON.stringify(cfg);
    try {
      const r = await runApi(testSlackRemote(base, cfg));
      if (sent === JSON.stringify(cfg)) testResult = r;
    } catch (e) {
      testError = e instanceof Error ? e.message : String(e);
    } finally {
      testing = false;
    }
  }

  async function submit() {
    if (configError(cfg) || handleWhy || saving) return;
    saving = true;
    error = "";
    handleError = "";
    try {
      const a = await runApi(
        createSlackRemote(base, {
          ...cfg,
          ...(name.trim() ? { name: name.trim() } : {}),
          ...(handleTyped ? { handle: handle.trim() } : {}),
          ...(tagline.trim() ? { tagline: tagline.trim() } : {}),
        }),
      );
      onCreated(a);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (msg.startsWith("handle")) handleError = msg;
      else error = msg;
    } finally {
      saving = false;
    }
  }

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const ghost =
    "rounded-lg px-4 py-2 text-sm text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600";
  const primary =
    "rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50";
  const outline =
    "rounded-lg border border-white-300 px-3 py-1.5 text-sm text-black-900 hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-600";
</script>

<div class="flex items-center gap-2 px-6 pt-5">
  <h2 class="flex-1 text-[17px] font-semibold text-black-900 dark:text-white-100">New agent</h2>
  <button
    type="button"
    class="flex h-8 w-8 items-center justify-center rounded-lg text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600"
    aria-label="Close"
    onclick={onClose}
  >
    <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 4l8 8M12 4l-8 8"></path></svg>
  </button>
</div>

{#if onType}
  <div class="px-6 pt-3">
    <div class="inline-flex rounded-lg border border-white-300 p-0.5 dark:border-navy-600" role="group" aria-label="Agent type">
      <button type="button" class="rounded-md px-3 py-1 text-xs text-black-800 dark:text-black-600" aria-pressed="false" onclick={() => onType?.("local")}>Wick agent</button>
      <button type="button" class="rounded-md bg-green-500 px-3 py-1 text-xs text-white-100" aria-pressed="true">Remote agent</button>
    </div>
  </div>
  <RemoteSourcePicker value="slack" onSource={(s) => { if (s === "a2a") onType?.("remote"); else if (s === "plugin") onType?.("plugin"); }} />
{/if}

<ol class="flex flex-wrap items-center gap-2 px-6 pt-3 pb-4 text-xs" aria-label="Steps">
  {#each STEPS as s, i (s)}
    {@const n = i + 1}
    <li class="flex items-center gap-1.5 {n === step ? 'font-semibold text-black-900 dark:text-white-100' : 'text-black-800 dark:text-black-600'}" aria-current={n === step ? "step" : undefined}>
      <span class="flex h-5 w-5 items-center justify-center rounded-full text-[11px] font-bold {n <= step ? 'bg-green-500 text-white-100' : 'bg-white-300 text-black-800 dark:bg-navy-600 dark:text-black-600'}">{n < step ? "✓" : n}</span>
      {s}
    </li>
    {#if i < STEPS.length - 1}<li class="h-px w-4 bg-white-300 dark:bg-navy-600" aria-hidden="true"></li>{/if}
  {/each}
</ol>

<div class="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 pb-2">
  {#if step === 1}
    {#if connectors === null}
      <p class="text-sm text-black-800 dark:text-black-600">Loading Slack connectors…</p>
    {:else if connectors.length === 0}
      <p class="text-sm text-black-800 dark:text-black-600" data-testid="sw-no-connector">
        {loadError || "No Slack connector you can use. Ask an admin to add one in Connectors."}
      </p>
    {:else}
      <div>
        <label class={label} for="sw-connector">Slack workspace</label>
        <select id="sw-connector" class={input} bind:value={connectorId}>
          <option value="" disabled>Choose a connector…</option>
          {#each connectors as c (c.id)}<option value={c.id}>{c.label}</option>{/each}
        </select>
        <p class="mt-1 text-xs text-black-800 dark:text-black-600">The Slack connector wick posts through.</p>
      </div>
    {/if}
  {:else if step === 2}
    <SlackTargetFields {base} {connectorId} {identity} {accountId} bind:target bind:channel bind:user bind:mentionId bind:threadTs bind:targetName idPrefix="sw" />
  {:else if step === 3}
    <SlackIdentityFields {base} {connectorId} bind:identity bind:accountId idPrefix="sw" />
  {:else if step === 4}
    <SlackListenFields bind:listen bind:marker bind:mention bind:idleSec bind:maxSec idPrefix="sw" />
  {:else}
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <div>
        <label class={label} for="sw-name">Name</label>
        <input id="sw-name" class={input} bind:value={name} placeholder={(targetName || where).replace(/^[#@]/, "")} />
      </div>
      <div>
        <label class={label} for="sw-handle">Handle</label>
        <input
          id="sw-handle"
          class="{input} font-mono"
          value={handle}
          placeholder="from the name"
          oninput={(e) => { handleError = ""; handle = (e.currentTarget as HTMLInputElement).value.toLowerCase().replace(/^@/, ""); }}
        />
        {#if handleError || handleWhy}<p class="mt-1 text-xs text-neg-400">{handleError || handleWhy}</p>{/if}
      </div>
      <div class="sm:col-span-2">
        <label class={label} for="sw-tagline">Tagline</label>
        <input id="sw-tagline" class={input} bind:value={tagline} maxlength={TAGLINE_MAX} placeholder="optional" />
      </div>
    </div>
    <div class="flex flex-wrap items-center gap-2">
      <button type="button" class={outline} disabled={!!configError(cfg) || testing} data-testid="sw-test" onclick={test}>
        {testing ? "Waiting for a reply…" : "Test"}
      </button>
      <span class="text-xs text-black-800 dark:text-black-600">Posts “ping” to {where} and waits up to 30 s.</span>
    </div>
    {#if testResult || testError}
      <p class="text-xs {testResult?.ok ? 'text-green-600 dark:text-green-400' : 'text-neg-400'}" data-testid="sw-test-result">
        {testResult ? slackTestSummary(testResult) : testError}
      </p>
    {/if}
    <p class="rounded-xl border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-500 dark:bg-navy-800 dark:text-amber-300" role="alert" data-testid="sw-warning">
      {slackWarning(where, workspace)}
    </p>
  {/if}
  {#if step < 5 && stepError && step !== 1}<p class="text-xs text-black-800 dark:text-black-600" data-testid="sw-step-hint">{stepError}</p>{/if}
  {#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
</div>

<div class="flex items-center justify-between gap-2 px-6 py-4">
  {#if step > 1}
    <button type="button" class={ghost} onclick={() => (step -= 1)}>← Back</button>
  {:else}
    <span></span>
  {/if}
  {#if step < STEPS.length}
    <button type="button" class={primary} disabled={!!stepError} onclick={() => (step += 1)}>Next →</button>
  {:else}
    <button type="button" class={primary} disabled={!!stepError || !!handleWhy || saving} onclick={submit}>{saving ? "Creating…" : "Create agent"}</button>
  {/if}
</div>
