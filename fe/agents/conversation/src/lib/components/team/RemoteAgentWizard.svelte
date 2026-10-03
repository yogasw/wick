<script lang="ts">
  /* + Agent › A2A remote (plan §6.2b): another system's A2A agent joins the
     roster. Three steps:
     1 Agent card — the card (or base) URL and its auth, Fetch card for a
       preview, Test for a "ping" round trip. The handle comes from the
       card (made free server-side) and stays editable.
     2 Usage — where messages go, and who may use it.
     3 Connect — Slack bridging is not built yet, so this only skips.
     Changing the URL or auth after a fetch drops the preview: what is
     created is always what was last fetched. */
  import { AgentAvatar, defaultAvatarFor } from "@wick-fe/common-avatar";
  import RemoteAuthFields from "./RemoteAuthFields.svelte";
  import RemoteSourcePicker from "./RemoteSourcePicker.svelte";
  import {
    createRemoteAgent, resolveRemoteCard, testRemoteAgent, runApi,
    type AgentItem, type RemoteAuthType, type RemoteResolved, type RemoteTestResult,
  } from "../../api/team.js";
  import { HANDLE_RE } from "../../agentForm.js";
  import { authReq, egressWarning, testSummary } from "../../remoteAgent.js";

  type Props = {
    base: string;
    taken: string[];
    onClose: () => void;
    onCreated: (a: AgentItem) => void;
    /** Back to the wick-agent wizard, or to another remote source. */
    onType?: (t: "local" | "remote" | "slack" | "plugin") => void;
  };
  let { base, taken, onClose, onCreated, onType }: Props = $props();

  const STEPS = ["Agent card", "Usage", "Connect"];
  let step = $state(1);

  let url = $state("");
  let authType = $state<RemoteAuthType>("none");
  let secret = $state("");
  let header = $state("");
  let resolved = $state<RemoteResolved | null>(null);
  // The url+auth the preview was fetched with; any edit invalidates it.
  let resolvedKey = $state("");
  let fetching = $state(false);
  let fetchError = $state("");
  let testing = $state(false);
  let testResult = $state<RemoteTestResult | null>(null);
  let testError = $state("");

  let handle = $state("");
  let handleTouched = $state(false);
  let handleError = $state("");
  let tagline = $state("");
  let saving = $state(false);
  let error = $state("");

  const auth = $derived(authReq(authType, secret, header));
  const authOk = $derived(auth !== undefined);
  const formKey = $derived(JSON.stringify([url.trim(), auth ?? null]));
  const card = $derived(resolved && resolvedKey === formKey ? resolved : null);
  const host = $derived(card?.host ?? "");

  const handleOk = $derived(HANDLE_RE.test(handle));
  const handleTaken = $derived(taken.includes(handle));
  const step1Ok = $derived(!!card && handleOk && !handleTaken);

  async function fetchCard() {
    if (!url.trim() || !authOk || fetching) return;
    fetching = true;
    fetchError = "";
    const key = formKey;
    try {
      const r = await runApi(resolveRemoteCard(base, url.trim(), auth));
      resolved = r;
      resolvedKey = key;
      if (!handleTouched) handle = r.suggested_handle;
    } catch (e) {
      fetchError = e instanceof Error ? e.message : String(e);
    } finally {
      fetching = false;
    }
  }

  async function test() {
    if (!url.trim() || !authOk || testing) return;
    testing = true;
    testResult = null;
    testError = "";
    try {
      testResult = await runApi(testRemoteAgent(base, { url: url.trim(), auth }));
    } catch (e) {
      testError = e instanceof Error ? e.message : String(e);
    } finally {
      testing = false;
    }
  }

  async function submit() {
    if (!card || !step1Ok || saving) return;
    saving = true;
    error = "";
    handleError = "";
    try {
      const a = await runApi(
        createRemoteAgent(base, {
          url: card.card_url || url.trim(),
          ...(auth && auth.type !== "none" ? { auth } : {}),
          ...(handleTouched ? { handle } : {}),
          ...(tagline.trim() ? { tagline: tagline.trim() } : {}),
        }),
      );
      onCreated(a);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (msg.startsWith("handle")) {
        handleError = msg;
        step = 1;
      } else error = msg;
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
  const fallback = $derived(defaultAvatarFor(handle || "agent"));
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
  <RemoteSourcePicker value="a2a" onSource={(s) => { if (s === "slack" || s === "plugin") onType?.(s); }} />
{/if}

<ol class="flex items-center gap-2 px-6 pt-3 pb-4 text-xs" aria-label="Steps">
  {#each STEPS as s, i (s)}
    {@const n = i + 1}
    <li class="flex items-center gap-1.5 {n === step ? 'font-semibold text-black-900 dark:text-white-100' : 'text-black-800 dark:text-black-600'}" aria-current={n === step ? "step" : undefined}>
      <span class="flex h-5 w-5 items-center justify-center rounded-full text-[11px] font-bold {n <= step ? 'bg-green-500 text-white-100' : 'bg-white-300 text-black-800 dark:bg-navy-600 dark:text-black-600'}">{n < step ? "✓" : n}</span>
      {s}
    </li>
    {#if i < STEPS.length - 1}<li class="h-px w-8 bg-white-300 dark:bg-navy-600" aria-hidden="true"></li>{/if}
  {/each}
</ol>

<div class="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 pb-2">
  {#if step === 1}
    <div>
      <label class={label} for="rw-url">Agent card URL</label>
      <!-- svelte-ignore a11y_autofocus -->
      <input id="rw-url" class="{input} font-mono" bind:value={url} placeholder="https://agent.example.com/.well-known/agent-card.json" autofocus />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">The card URL or the agent's base URL.</p>
    </div>
    <RemoteAuthFields bind:type={authType} bind:secret bind:header idPrefix="rw" />
    <div class="flex flex-wrap items-center gap-2">
      <button type="button" class={outline} disabled={!url.trim() || !authOk || fetching} data-testid="rw-fetch" onclick={fetchCard}>
        {fetching ? "Fetching…" : "Fetch card"}
      </button>
      <button type="button" class={outline} disabled={!url.trim() || !authOk || testing} data-testid="rw-test" onclick={test}>
        {testing ? "Testing…" : "Test"}
      </button>
    </div>
    {#if fetchError}<p class="text-sm text-neg-400" data-testid="rw-fetch-error">{fetchError}</p>{/if}
    {#if testResult || testError}
      <p class="text-xs {testResult?.ok ? 'text-green-600 dark:text-green-400' : 'text-neg-400'}" data-testid="rw-test-result">
        {testResult ? testSummary(testResult) : testError}
      </p>
    {/if}
    {#if card}
      <div class="rounded-xl border border-white-300 p-3 dark:border-navy-600" data-testid="rw-card">
        <div class="flex items-center gap-3">
          {#if card.card.icon_url}
            <img src={card.card.icon_url} alt="" class="h-11 w-11 rounded-xl object-cover" />
          {:else}
            <AgentAvatar shape={fallback.shape} color={fallback.color} size={44} />
          {/if}
          <div class="min-w-0 flex-1">
            <p class="truncate text-sm font-semibold text-black-900 dark:text-white-100">{card.card.name}</p>
            <p class="truncate text-xs text-black-800 dark:text-black-600">
              {card.host}{#if card.card.version} · v{card.card.version}{/if} · {card.card.streaming ? "streaming" : "no streaming"}{#if card.card.provider} · {card.card.provider}{/if}
            </p>
          </div>
        </div>
        {#if card.card.description}<p class="mt-2 text-xs text-black-800 dark:text-black-600">{card.card.description}</p>{/if}
        {#if (card.card.skills ?? []).length > 0}
          <ul class="mt-2 flex flex-wrap gap-1.5" aria-label="Skills">
            {#each card.card.skills ?? [] as sk (sk.id)}
              <li class="rounded-full bg-white-200 px-2 py-0.5 text-[11px] text-black-900 dark:bg-navy-800 dark:text-white-100" title={sk.description ?? ""}>{sk.name || sk.id}</li>
            {/each}
          </ul>
        {:else}
          <p class="mt-2 text-xs text-black-700">No skills listed.</p>
        {/if}
      </div>
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div>
          <label class={label} for="rw-handle">Handle</label>
          <input
            id="rw-handle"
            class="{input} font-mono"
            value={handle}
            oninput={(e) => { handleTouched = true; handleError = ""; handle = (e.currentTarget as HTMLInputElement).value.toLowerCase().replace(/^@/, ""); }}
          />
          {#if handleError}
            <p class="mt-1 text-xs text-neg-400">{handleError}</p>
          {:else if handleTaken}
            <p class="mt-1 text-xs text-neg-400">@{handle} is already taken by another agent.</p>
          {:else if handle && !handleOk}
            <p class="mt-1 text-xs text-neg-400">Lowercase letters, digits and "-", 2–31 characters, starting with a letter or digit.</p>
          {/if}
        </div>
        <div>
          <label class={label} for="rw-tagline">Tagline</label>
          <input id="rw-tagline" class={input} bind:value={tagline} maxlength="32" placeholder="optional" />
        </div>
      </div>
      <p class="text-xs text-black-800 dark:text-black-600">Name and avatar come from the card; change them later in Settings.</p>
    {/if}
  {:else if step === 2}
    <p class="rounded-xl border border-amber-300 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-500 dark:bg-navy-800 dark:text-amber-300" role="alert" data-testid="rw-warning">
      {egressWarning(host)}
    </p>
    <p class="text-xs text-black-800 dark:text-black-600">Its tools run on {host || "the remote host"}, so there is no connector checklist here.</p>
  {:else}
    <div class="space-y-2" data-testid="rw-connect">
      <label class="flex items-start gap-2 rounded-xl border border-green-500 px-3 py-2 text-sm text-black-900 dark:text-white-100">
        <input type="radio" class="mt-1" checked name="rw-connect" />
        <span>Skip for now<br /><span class="text-xs text-black-800 dark:text-black-600">Chat with it here in Team.</span></span>
      </label>
      <label class="flex items-start gap-2 rounded-xl border border-white-300 px-3 py-2 text-sm text-black-700 opacity-60 dark:border-navy-600">
        <input type="radio" class="mt-1" disabled name="rw-connect" />
        <span>Slack <span class="ml-1 rounded-full bg-white-300 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wider dark:bg-navy-600">coming soon</span><br /><span class="text-xs">Mentions in Slack relayed over A2A.</span></span>
      </label>
    </div>
  {/if}
  {#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
</div>

<div class="flex items-center justify-between gap-2 px-6 py-4">
  {#if step > 1}
    <button type="button" class={ghost} onclick={() => (step -= 1)}>← Back</button>
  {:else}
    <span></span>
  {/if}
  {#if step < STEPS.length}
    <button type="button" class={primary} disabled={!step1Ok} onclick={() => (step += 1)}>Next →</button>
  {:else}
    <button type="button" class={primary} disabled={!step1Ok || saving} onclick={submit}>{saving ? "Creating…" : "Create agent"}</button>
  {/if}
</div>
