<script lang="ts">
  import { onMount } from "svelte";
  import { Button, Select, SavedResetsSection } from "@wick-fe/common-ui";
  import { seedUsage } from "@wick-fe/common-ui/usage";
  import { toastError } from "@wick-fe/common-stores";
  import {
    apiLoginTTYStatus,
    apiLoginTTYUsage,
    apiLoginTTYStart,
    apiLoginTTYUsageRefresh,
    defaultLoginChoice,
    usageLabel,
    fmtResetsIn,
    prettyPlan,
    validTime,
    apiLoginTTYLogout,
    apiSetAPIKey,
    type LoginTTYStatus,
    type LoginTTYSession,
    type UsageResult,
    type LoginAccount,
  } from "$lib/logintty.js";
  import LoginTerminalModal from "$lib/components/LoginTerminalModal.svelte";
  import UsageCacheChip from "$lib/components/UsageCacheChip.svelte";
  import AccountList from "$lib/components/AccountList.svelte";
  import { fmtSecsShort } from "$lib/usagerings.js";

  type Props = { base: string; type: string; name: string; defaultExpanded?: boolean };
  let { base, type, name, defaultExpanded = false }: Props = $props();

  let status = $state<LoginTTYStatus | null>(null);
  let usage = $state<UsageResult | null>(null);
  let loading = $state(true);
  let starting = $state(false);
  let session = $state<LoginTTYSession | null>(null);
  let showTerminal = $state(false);
  /* Collapsed by default — the header row is the summary; details
     (account rows + usage) render only when expanded. */
  let expanded = $state(defaultExpanded);
  /* omp/opencode: which OAuth provider the next login targets. */
  let loginChoice = $state("");
  let choice = $derived((status?.loginChoices ?? []).find((c) => c.id === loginChoice) ?? null);
  /* API-key login: provider picked + the key being typed (never echoed). */
  let apiKeyProvider = $state("");
  let apiKeyValue = $state("");
  let apiKeyBusy = $state(false);
  let apiKeyChoice = $derived((status?.apiKeys ?? []).find((k) => k.id === apiKeyProvider) ?? null);
  let loggingOut = $state("");

  let connected = $derived(status?.account.connected ?? false);
  /* Shared login: the owner instance; login controls live there. */
  let sharedFrom = $derived(status?.authFrom ?? "");
  /* Reconnect shows while collapsed ONLY when a login is actually
     needed; expanded always offers it (deliberate re-login). */
  let showReconnect = $derived.by(() => {
    if (!status?.supported) return false;
    if (status.authFrom) return false;
    if (status.session?.state === "running") return false;
    return expanded || (!connected && !status.account.unknown);
  });

  // An unreadable login ("unknown") is retried on its own, so "Checking
  // login…" settles without the user reloading the page.
  let unknownRetry: ReturnType<typeof setTimeout> | null = null;
  // refresh() awaits before re-arming; after unmount it must not.
  let destroyed = false;
  onMount(() => () => {
    destroyed = true;
    if (unknownRetry !== null) clearTimeout(unknownRetry);
  });

  async function refresh() {
    try {
      status = await apiLoginTTYStatus(base, type, name);
    } catch {
      status = null;
    }
    if (unknownRetry !== null) clearTimeout(unknownRetry);
    unknownRetry = status?.account.unknown && !destroyed ? setTimeout(() => void refresh(), 5000) : null;
    try {
      usage = await apiLoginTTYUsage(base, type, name);
      shareUsage();
    } catch {
      usage = null;
    }
  }

  /* shareUsage hands the panel's reading to the shared usage store, so the
     card chip, picker rings and /usage popover show the same numbers. */
  function shareUsage() {
    if (!usage?.supported || usage.pending) return;
    seedUsage(`${type}/${name}`, {
      supported: true,
      checked: true,
      windows: usage.windows,
      savedResets: usage.savedResets ?? null,
      error: usage.error,
      fetchedAt: usage.fetchedAt,
    });
  }

  /* Re-check: ask the server for a fresh reading now. Allowed even when
     numbers are already on screen — the server owns the decision about
     whether a probe may actually go out, and tells us the wait when it
     declines. */
  let rechecking = $state(false);
  let recheckWait = $state(0);

  async function recheckUsage() {
    rechecking = true;
    recheckWait = 0;
    try {
      const r = await apiLoginTTYUsageRefresh(base, type, name);
      if (!r.accepted) recheckWait = r.waitS;
      usage = await apiLoginTTYUsage(base, type, name);
      shareUsage();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Failed to re-check usage");
    } finally {
      rechecking = false;
    }
  }

  let usageBusy = $derived(rechecking || usage?.checking === true);

  onMount(async () => {
    await refresh();
    loading = false;
    loginChoice = defaultLoginChoice(status?.loginChoices ?? []);
    // A fresh omp/opencode instance needs a provider picked before Login,
    // so open the details (where the picker lives) instead of hiding it.
    if (status && (status.loginChoices ?? []).length > 0 && !status.account.connected && !status.account.unknown) {
      expanded = true;
    }
  });

  // Also bound directly as an onclick handler, so the argument may be the
  // click event: only a string names an opencode account folder.
  async function reconnect(accountArg?: unknown) {
    const account = typeof accountArg === "string" ? accountArg : "";
    starting = true;
    try {
      const s = account
        ? await apiLoginTTYStart(base, type, name, loginChoice, account)
        : await apiLoginTTYStart(base, type, name, loginChoice);
      if (s) {
        session = s;
        showTerminal = true;
      }
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Failed to start login session");
    } finally {
      starting = false;
    }
  }

  function openRunning() {
    if (status?.session) {
      session = status.session;
      showTerminal = true;
    }
  }

  function onFinished(_account: LoginAccount | null) {
    void refresh();
  }

  function closeTerminal() {
    showTerminal = false;
    void refresh();
  }

  function fmtExpiry(iso: string): string {
    if (!validTime(iso)) return "";
    return new Date(iso).toLocaleString();
  }

  async function logoutProvider(prov: string, accountId = "") {
    const n = (status?.accounts ?? []).filter((a) => a.provider === prov).length;
    // opencode row ids are "<folder>/<provider>"; the folder picks which
    // account's auth.json the logout runs against.
    const folder = accountId ? accountId.split("/")[0] : "";
    const msg = type === "omp"
      ? `Log out of ${prov}? omp removes ALL ${n} stored account(s) of this provider from the profile — it has no single-account logout outside its TUI.`
      : `Remove the ${prov} credential of account ${folder || "main"} (opencode auth logout)?`;
    if (!confirm(msg)) return;
    loggingOut = accountId || prov;
    try {
      await apiLoginTTYLogout(base, type, name, prov, folder);
      await refresh();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Logout failed");
    } finally {
      loggingOut = "";
    }
  }

  async function saveAPIKey(remove = false) {
    if (!apiKeyProvider) return;
    apiKeyBusy = true;
    try {
      await apiSetAPIKey(base, type, name, apiKeyProvider, remove ? "" : apiKeyValue);
      apiKeyValue = "";
      await refresh();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Failed to save API key");
    } finally {
      apiKeyBusy = false;
    }
  }

  function barColor(pct: number): string {
    if (pct >= 90) return "bg-neg-400";
    if (pct >= 70) return "bg-cau-400";
    return "bg-link-400";
  }

  // Account rows in the CLI's Account & Usage order. Empty values are
  // skipped at render.
  let accountRows = $derived.by(() => {
    if (!status) return [] as { label: string; value: string }[];
    const a = status.account;
    // omp/opencode set authMethod to the pool provider id (openai-codex,
    // anthropic…); the other types are single-provider, so the type is it.
    const provider = type === "omp" || type === "opencode" ? a.authMethod : type;
    return [
      { label: "Auth method", value: a.authMethod },
      { label: "Email", value: a.email },
      { label: "Organization", value: a.org },
      { label: "Plan", value: prettyPlan(a.plan, provider) },
    ].filter((r) => r.value !== "");
  });
</script>

<!-- Re-check button, shared by the usage header and the error line. The
     wait it prints after a refusal is the point: it says the probe was
     deliberately not sent, rather than leaving a dead-looking button. -->
{#snippet recheck()}
  <span class="inline-flex items-center gap-1">
    <button
      type="button"
      data-testid="panel-usage-recheck"
      class="rounded px-1.5 py-0.5 text-[11px] text-link-400 hover:bg-white-300 dark:hover:bg-navy-600 disabled:opacity-50 disabled:hover:bg-transparent"
      disabled={usageBusy}
      title={usageBusy ? "Checking usage…" : "Check this account's usage now"}
      onclick={(e) => {
        e.stopPropagation();
        void recheckUsage();
      }}
    >
      Re-check
    </button>
    {#if recheckWait > 0}
      <span class="text-[11px] text-black-600 dark:text-black-700">wait {fmtSecsShort(recheckWait)}</span>
    {/if}
  </span>
{/snippet}

{#if showTerminal && session}
  <LoginTerminalModal
    {base}
    {type}
    {name}
    session={session}
    extendS={status?.extendS ?? 300}
    onClose={closeTerminal}
    onFinished={onFinished}
  />
{/if}

<div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
  <!-- Summary row: status badge (+ email when connected); Reconnect
       only when a login is needed. Clicking the row toggles details;
       the action buttons stop propagation so they don't toggle. -->
  <div
    role="button"
    tabindex="0"
    aria-expanded={expanded}
    aria-label="Toggle connection details"
    onclick={() => { expanded = !expanded; }}
    onkeydown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); expanded = !expanded; } }}
    class="flex items-center gap-3 px-5 py-3 cursor-pointer select-none hover:bg-white-200 dark:hover:bg-navy-800 transition-colors {expanded ? 'border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800' : ''}"
  >
    <svg class="h-3.5 w-3.5 shrink-0 text-black-600 transition-transform {expanded ? 'rotate-90' : ''}" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path></svg>
    <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Connection</h3>
    {#if loading}
      <span class="text-xs text-black-700 dark:text-black-600">Checking…</span>
    {:else if status}
      {#if status.account.unknown}
        <span data-testid="panel-checking" class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-semibold text-black-700 dark:text-black-500">Checking login…</span>
      {:else if connected}
        <span class="rounded bg-pos-100 dark:bg-pos-400/20 px-2 py-0.5 text-xs font-semibold text-pos-400">Connected</span>
        {#if status.account.email}
          <span class="hidden sm:inline min-w-0 truncate font-mono text-xs text-black-800 dark:text-black-600">{status.account.email}</span>
        {/if}
      {:else}
        <span class="rounded bg-neg-100 dark:bg-neg-400/20 px-2 py-0.5 text-xs font-semibold text-neg-400">Not connected</span>
      {/if}
    {:else}
      <span class="text-xs text-black-700 dark:text-black-600">Status unavailable</span>
    {/if}
    <span class="ml-auto"></span>
    <!-- svelte-ignore a11y_no_static_element_interactions -->
    <span onclick={(e) => e.stopPropagation()} onkeydown={(e) => e.stopPropagation()} class="flex items-center gap-2">
      {#if status?.session?.state === "running"}
        <Button variant="secondary" onclick={openRunning}>Open login terminal</Button>
      {:else if showReconnect}
        <Button variant="primary" disabled={starting} onclick={reconnect}>
          {starting ? "Starting…" : "Reconnect"}
        </Button>
      {/if}
    </span>
  </div>
  {#if expanded}
  <div class="p-5 space-y-4">
    {#if loading}
      <p class="text-xs text-black-700 dark:text-black-600">Checking credentials…</p>
    {:else if !status}
      <p class="text-xs text-black-700 dark:text-black-600">Connection status unavailable.</p>
    {:else}
      <!-- ACCOUNT — CLI Account & Usage layout: label left, value right.
           The connect badge lives in the summary row, not repeated here. -->
      <div class="space-y-2">
        <p class="text-[11px] font-semibold tracking-wide text-black-700 dark:text-black-600">ACCOUNT</p>
        {#if sharedFrom}
          <p data-testid="panel-shared-login" class="rounded-lg bg-white-200 dark:bg-navy-800 px-3 py-2 text-[11px] text-black-900 dark:text-white-100 break-words">
            Uses the login of <a href={`${base}/${type}/${encodeURIComponent(sharedFrom)}`} data-testid="panel-shared-login-owner" class="font-mono text-link-400 hover:underline">{sharedFrom}</a> — log in, log out and add accounts there.
          </p>
          {#if type === "opencode"}
            <p data-testid="panel-shared-login-race" class="rounded-lg border border-cau-400 bg-cau-100 dark:bg-cau-400/20 px-3 py-2 text-[11px] text-black-900 dark:text-white-100">Instances sharing a ChatGPT login can occasionally hit a token-refresh race when turns run on both at once; the turn that loses fails and can be retried.</p>
          {/if}
        {/if}
        {#each accountRows as row (row.label)}
          <div class="flex items-baseline justify-between gap-4 text-xs">
            <span class="shrink-0 text-black-800 dark:text-black-600">{row.label}</span>
            <span class="min-w-0 truncate text-right font-medium text-black-900 dark:text-white-100">{row.value}</span>
          </div>
        {/each}
        {#if status.account.connected && validTime(status.account.expiresAt)}
          <p class="text-[11px] text-black-700 dark:text-black-600">Token expires {fmtExpiry(status.account.expiresAt)}</p>
        {/if}
        {#if status.accountStore}
          <div class="flex items-baseline justify-between gap-4 text-xs">
            <span class="shrink-0 text-black-800 dark:text-black-600">Account store</span>
            <span data-testid="panel-account-store" class="min-w-0 truncate text-right font-mono text-black-900 dark:text-white-100">{status.accountStore}</span>
          </div>
          {#if type === "omp"}
            <p data-testid="panel-multi-account-note" class="text-[11px] text-black-700 dark:text-black-600">One instance can hold several accounts: omp rotates to the next one automatically when an account hits its usage limit. Separate instances still work if you want accounts kept apart.</p>
          {:else}
            <p data-testid="panel-multi-provider-note" class="text-[11px] text-black-700 dark:text-black-600">One instance can hold many providers, one login each. For two accounts of the same provider, use "Second account of a provider" (or a separate instance).</p>
          {/if}
        {/if}
        {#if !status.supported}
          <p class="text-[11px] text-black-700 dark:text-black-600">Reconnect via terminal is not available for this provider type yet.</p>
        {/if}
      </div>

      {#if sharedFrom}
        <!-- A sharer's accounts are the owner's; managed on the owner. -->
      {:else}
      {#if (status.accounts ?? []).length > 0}
        <AccountList
          accounts={status.accounts ?? []}
          removeLabel={type === "omp" ? "Log out provider" : "Remove"}
          removing={loggingOut}
          canAdd={status.supported && status.session?.state !== "running"}
          adding={starting}
          onAdd={() => void reconnect()}
          onRemove={(prov, id) => void logoutProvider(prov, id)}
          perAccount={type === "opencode"}
        >
          {#snippet note()}
            {#if type === "omp"}
              "Add another account" runs the login below again into this same profile. omp has no single-account removal outside its own TUI, so logout here is per provider.
            {:else}
              One instance can hold many providers (one login each). "Add another account" logs in to another provider — pick it below, or use an API key. "Second account of a provider" logs in to a new data folder of this instance; the model picker then offers Auto (wick rotates on usage limits) or that account.
            {/if}
          {/snippet}
        </AccountList>
        {#if type === "opencode" && status.supported && status.session?.state !== "running"}
          <button
            type="button"
            data-testid="panel-add-account-folder"
            disabled={starting}
            onclick={() => void reconnect("new")}
            class="self-start rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1.5 text-xs text-black-900 dark:text-white-100 hover:border-green-500 disabled:opacity-50"
          >Second account of a provider</button>
        {/if}
      {/if}

      {#if status.supported && (status.loginChoices ?? []).length > 0}
        <div class="space-y-2" data-testid="panel-login-choice">
          <label for="login-choice-{type}-{name}" class="block text-[11px] font-semibold tracking-wide text-black-700 dark:text-black-600">LOG IN WITH</label>
          <Select
            id="login-choice-{type}-{name}"
            value={loginChoice}
            searchable
            options={status.loginChoices.map((c) => ({ label: c.label, value: c.id, ...(c.warning ? { badge: "policy" } : c.beta ? { badge: "beta" } : {}) }))}
            onChange={(v) => { loginChoice = v; }}
          />
          {#if choice?.warning}
            <p data-testid="panel-login-warning" class="rounded-lg border border-cau-400 bg-cau-100 dark:bg-cau-400/20 px-3 py-2 text-[11px] text-black-900 dark:text-white-100">{choice.warning}</p>
          {/if}
          {#if status.loginNote}
            <p class="text-[11px] text-black-700 dark:text-black-600">{status.loginNote}</p>
          {/if}
          <p class="text-[11px] text-black-700 dark:text-black-600">Browser flows redirect to localhost, which this host never receives: when the page fails to load, copy its full URL from the address bar and paste it into the login terminal.</p>
        </div>
      {/if}

      {#if (status.apiKeys ?? []).length > 0}
        <div class="space-y-2" data-testid="panel-api-key">
          <label for="api-key-provider-{type}-{name}" class="block text-[11px] font-semibold tracking-wide text-black-700 dark:text-black-600">OR USE AN API KEY</label>
          <Select
            id="api-key-provider-{type}-{name}"
            value={apiKeyProvider}
            searchable
            placeholder="Pick a provider…"
            options={(status.apiKeys ?? []).map((k) => ({ label: k.label, value: k.id, description: k.env, ...(k.set ? { badge: "key set" } : {}) }))}
            onChange={(v) => { apiKeyProvider = v; apiKeyValue = ""; }}
          />
          {#if apiKeyChoice}
            <div class="flex items-center gap-2">
              <input
                type="password"
                autocomplete="off"
                data-testid="panel-api-key-input"
                placeholder={apiKeyChoice.set ? "Key set — paste a new one to replace" : `Paste ${apiKeyChoice.env}`}
                bind:value={apiKeyValue}
                class="min-w-0 flex-1 rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1 font-mono text-xs text-black-900 dark:text-white-100"
              />
              <Button variant="primary" testid="panel-api-key-save" disabled={apiKeyBusy || apiKeyValue.trim() === ""} onclick={() => void saveAPIKey()}>Save</Button>
              {#if apiKeyChoice.set}
                <Button variant="secondary" testid="panel-api-key-remove" disabled={apiKeyBusy} onclick={() => void saveAPIKey(true)}>Remove</Button>
              {/if}
            </div>
            <p class="text-[11px] text-black-700 dark:text-black-600">Saved as the instance env var <span class="font-mono">{apiKeyChoice.env}</span> (masked like every other secret env); live models refresh after saving.</p>
          {/if}
          {#if (status.apiKeys ?? []).some((k) => k.set)}
            <p data-testid="panel-api-keys-set" class="text-[11px] text-black-700 dark:text-black-600">Keys set: {(status.apiKeys ?? []).filter((k) => k.set).map((k) => k.label).join(", ")}</p>
          {/if}
        </div>
      {/if}

      <!-- USAGE — one block per window: name + %, bar, resets-in -->
      {/if}

      {#if usage && !usage.supported && type === "opencode"}
        <p class="text-[11px] text-black-700 dark:text-black-600">Usage not available — opencode has no usage command.</p>
      {/if}
      {#if usage?.supported}
        {#if usage.windows.length > 0}
          <div class="space-y-3 pt-1">
            <div class="flex items-center justify-between gap-2">
              <p class="text-[11px] font-semibold tracking-wide text-black-700 dark:text-black-600">USAGE</p>
              {@render recheck()}
            </div>
            {#each usage.windows as w (w.key)}
              {@const resetsIn = fmtResetsIn(w.resetsAt, Date.now())}
              <div class="space-y-1">
                <div class="flex items-baseline justify-between gap-4 text-xs">
                  <span class="text-black-900 dark:text-white-100">{usageLabel(w.key)}</span>
                  <span class="font-medium text-black-900 dark:text-white-100">{Math.round(w.utilization)}%</span>
                </div>
                <div class="h-2 w-full rounded-full bg-white-300 dark:bg-navy-600 overflow-hidden">
                  <div class="h-full rounded-full {barColor(w.utilization)}" style={`width: ${Math.min(100, Math.max(0, w.utilization))}%`}></div>
                </div>
                {#if resetsIn}
                  <p class="text-[11px] text-black-700 dark:text-black-600">Resets in {resetsIn}</p>
                {/if}
              </div>
            {/each}
            <!-- Saved resets ride in the same cached reading; hidden when
                 the type reports none or the read failed. -->
            <SavedResetsSection resets={usage.savedResets} />
            <!-- Shared, cached reading (see UsageCacheChip): say its age
                 rather than implying this panel fetched it on open. -->
            <p class="flex items-center gap-1 text-[11px] text-black-700 dark:text-black-600">
              {#if usageBusy}
                <span data-testid="panel-usage-checking">Checking usage…</span>
              {:else}
                <UsageCacheChip ageS={usage.ageS} nextS={usage.nextS} fetchedAt={usage.fetchedAt} />
              {/if}
            </p>
          </div>
        {:else if usage.pending || usageBusy}
          <p class="text-[11px] text-black-700 dark:text-black-600">Checking usage… (probes are paced to stay under the endpoint's rate limit)</p>
        {:else if usage.error}
          <p class="text-[11px] text-black-700 dark:text-black-600">
            Usage unavailable: <span class="font-mono">{usage.error}</span>
            {#if usage.nextS > 0}<span class="text-black-600 dark:text-black-700"> — next probe in {fmtSecsShort(usage.nextS)}</span>{/if}
          </p>
          {@render recheck()}
        {/if}
      {/if}
    {/if}
  </div>
  {/if}
</div>
