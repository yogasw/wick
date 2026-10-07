<script lang="ts">
  import { ConfirmDialog, Modal, ProviderIcon, Select } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import AIRouterConfig from "$lib/components/AIRouterConfig.svelte";
  import RecentSpawns from "$lib/components/RecentSpawns.svelte";
  import ManagedBinaryPanel from "$lib/components/ManagedBinaryPanel.svelte";
  import { apiManagedList, isRunning, jobShort, type ManagedBinary } from "$lib/managedbin.js";
  import { UsageReport } from "@wick-fe/common-ui";
  import {
    apiGetProviders,
    apiRescanAll,
    apiRescanOne,
    apiGateToggle,
    apiGateModes,
    apiAutoRescanToggle,
    apiMCPInstall,
    apiMCPUninstall,
    apiDeleteProvider,
    apiCreateProvider,
    apiHookEnable,
    apiHookDisable,
    apiHookCheck,
    apiGetWickConfig,
    apiGetConnections,
  } from "$lib/api.js";
  import type { ProvidersListResponse, ProviderStatusDTO, ProviderConnection } from "$lib/types.js";
  import UsageRings from "$lib/components/UsageRings.svelte";
  import UsageCacheChip from "$lib/components/UsageCacheChip.svelte";
  import { apiLoginTTYUsageRefresh } from "$lib/logintty.js";
  import { pickWindows, connectionKey, resetHint, fmtSecsShort } from "$lib/usagerings.js";
  import {
    ACCOUNT_ISOLATED,
    accountHint,
    typeOption,
    suggestName,
    sourceLabel,
    accountStorePreview,
    validOMPProfile,
  } from "$lib/accounts.js";

  const HOOK_EVENT = "PreToolUse";

  type Props = {
    onNavigate: (type: string, name: string) => void;
    onOpenSession: (sessionID: string) => void;
    base: string;
  };
  let { onNavigate, onOpenSession, base }: Props = $props();

  let data = $state<ProvidersListResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  // Wick's model count + default label live behind its own config endpoint
  // (the /api/providers list doesn't carry them), so fetch it alongside the
  // list to fill the built-in card. Null until first fetch resolves.
  let wickInfo = $state<{ count: number; defaultLabel: string } | null>(null);
  // Account + usage per instance, keyed by `${type}/${name}`. Loaded from
  // its own endpoint after the list paints: the account read is local but
  // the usage probe is a remote call, and the cards must not wait on it.
  let connections = $state<Record<string, ProviderConnection>>({});
  // false until the first connections request settles: a card without a
  // connection row then means "not checked yet", not "logged out".
  let connectionsLoaded = $state(false);
  // Types whose binary wick can install/update itself (omp, opencode),
  // with their status — the cards only INDICATE it (version, update
  // available, a running download); every action lives on Detail. Read
  // from the server's release cache, so this never waits on GitHub.
  let managedByType = $state<Record<string, ManagedBinary>>({});
  const managedTypes = $derived(Object.keys(managedByType));
  let managedTimer: ReturnType<typeof setTimeout> | null = null;
  async function loadManagedTypes(): Promise<void> {
    try {
      const out: Record<string, ManagedBinary> = {};
      for (const t of (await apiManagedList(base)).types) if (t.enabled) out[t.type] = t;
      managedByType = out;
    } catch {
      managedByType = {};
    }
    // Follow a running download so the card's progress stays live.
    if (managedTimer) clearTimeout(managedTimer);
    // The await above can outlive the component; do not re-arm after it.
    if (managedDestroyed) return;
    if (Object.values(managedByType).some((m) => isRunning(m.job))) managedTimer = setTimeout(() => void loadManagedTypes(), 2000);
  }
  let managedDestroyed = false;
  $effect(() => () => { managedDestroyed = true; if (managedTimer) clearTimeout(managedTimer); });
  let confirmDelete = $state<ProviderStatusDTO | null>(null);
  let busy = $state<Record<string, boolean>>({});
  // Which account-hint popover is open (keyed per card, "add" for the
  // form). A tap toggles it — title alone never shows on touch screens.
  let hintOpen = $state<string | null>(null);
  let mcpOpen = $state(false);
  let addOpen = $state(false);

  let formType = $state("");
  let formName = $state("");
  let formBinary = $state("");
  let formExtraArgs = $state("");
  let formEnv = $state("");
  let formUseAirouter = $state(false);
  let formAirouterProvider = $state("");
  let formAirouterModels = $state<Record<string, string>>({});
  let formAirouterKey = $state("");
  let formAirouterRawConfig = $state("");
  // omp/opencode account store: shown read-only, editable only after the
  // operator explicitly asks to override it.
  // omp/opencode: "managed" (default) or "manual" binary path.
  let formBinarySource = $state("managed");
  let formStoreOverride = $state(false);
  let formStoreValue = $state("");
  const formIsolated = $derived(ACCOUNT_ISOLATED.has(formType));
  const formStoreError = $derived.by(() => {
    if (!formIsolated || !formStoreOverride || formStoreValue.trim() === "") return "";
    if (formType === "omp" && !validOMPProfile(formStoreValue.trim())) return "Lowercase letters, digits, '.', '_' or '-' (max 64)";
    if (formType === "opencode" && !formStoreValue.trim().startsWith("/")) return "Must be an absolute path";
    return "";
  });

  // A new omp/opencode instance gets a free name suggested (`omp`, `omp_2`,
  // …) so adding a second account is one click; other types keep an
  // empty name to type.
  /* idleLabel shows an idle-compact wait: 90s, 30m, 1m30s. */
  function idleLabel(sec: number): string {
    if (sec < 60) return `${sec}s`;
    return sec % 60 === 0 ? `${sec / 60}m` : `${Math.floor(sec / 60)}m${sec % 60}s`;
  }

  /* tokensLabel shows a token threshold: 100k, or the plain count. */
  function tokensLabel(n: number): string {
    return n % 1000 === 0 ? `${n / 1000}k` : `${n}`;
  }

  function onTypeChange(): void {
    formAirouterModels = {};
    formStoreOverride = false;
    formStoreValue = "";
    if (ACCOUNT_ISOLATED.has(formType)) {
      const taken = (data?.Providers ?? []).filter((p) => p.Instance.Type === formType).map((p) => p.Instance.Name);
      formName = suggestName(formType, taken);
    }
  }

  // The AI router picker in the create form offers the built-in routers.
  // A fresh instance has no detail payload to source them from, so list the
  // registered backends here (mirrors airouter.List() in the BE).
  const airouterRouters: { ID: string; Name: string }[] = [
    { ID: "9router", Name: "9router" },
    { ID: "omniroute", Name: "OmniRoute" },
  ];

  // An AI router only makes sense for the CLIs wick can rewire (claude/codex).
  const airouterSupported = $derived(formType === "claude" || formType === "codex");

  // The name becomes the second half of the "type/name" provider key,
  // so spaces would break the key downstream. We auto-convert spaces to
  // '_' as the user types (a space is almost always meant as a word
  // separator), and reject anything else not in [A-Za-z0-9_] inline so
  // it's caught before submit rather than as a save error. '_' is the
  // only allowed separator.
  function onNameInput() {
    formName = formName.replace(/\s+/g, "_");
  }
  let formNameError = $derived.by(() => {
    const v = formName.trim();
    if (v === "") return "";
    if (!/^[A-Za-z0-9_]+$/.test(v)) return "Use letters, digits or '_' only";
    return "";
  });

  /* Admin decides which page this is. Everything that writes provider
     configuration is admin-only however the access tags are set, so a
     non-admin gets a reading page: their accessible providers, their
     usage, and Reconnect / Re-check on the ones they were granted. The
     API enforces all of it — this only stops us rendering buttons that
     would come back 403. */
  let isAdmin = $derived(data?.IsAdmin ?? false);

  /* canManage answers the per-instance question the API will ask again:
     may this caller reconnect it and force a usage re-check? Admins
     always; everyone else through a manage tag. */
  function canManage(type: string, name: string): boolean {
    const row = data?.Providers.find((p) => p.Instance.Type === type && p.Instance.Name === name);
    return row?.CanManage ?? false;
  }

  let pollInterval: ReturnType<typeof setInterval> | null = null;

  async function load(silent = false): Promise<void> {
    if (!silent) {
      loading = true;
      error = null;
    }
    try {
      data = await apiGetProviders();
      void loadWick();
      void loadConnections();
      void loadManagedTypes();
    } catch (e) {
      if (!silent) {
        error = e instanceof Error ? e.message : "Failed to load providers";
      }
    } finally {
      if (!silent) {
        loading = false;
      }
    }
  }

  // Fetch the wick model count + default label for its card. Only runs when
  // the list actually contains a wick instance; failures are non-fatal (the
  // card just shows "Needs setup").
  async function loadWick(): Promise<void> {
    if (!data?.Providers.some((p) => p.Instance.Type === "wick")) {
      return;
    }
    try {
      const cfg = await apiGetWickConfig(base);
      const def = cfg.models.find((m) => m.Default) ?? cfg.models[0];
      wickInfo = {
        count: cfg.models.length,
        defaultLabel: def ? (def.Label || def.Model) : "",
      };
    } catch {
      wickInfo = { count: 0, defaultLabel: "" };
    }
  }

  /* loadConnections fills the per-card account + usage badges. A failure
     leaves the badges absent rather than surfacing an error: the list
     itself came from a separate, local-only payload and stays usable. */
  async function loadConnections(): Promise<void> {
    try {
      const rows = await apiGetConnections();
      const next: Record<string, ProviderConnection> = {};
      for (const c of rows) {
        next[connectionKey(c.type, c.name)] = c;
      }
      connections = next;
    } catch {
      connections = {};
    } finally {
      connectionsLoaded = true;
    }
  }

  /* Re-check state, per card.

     `rechecking` is optimistic: the server flips its own `usageChecking`
     flag on the very next poll, but the click should light up now.
     `recheckWait` holds a refusal ("the endpoint asked us to wait 4m"),
     which is information, not an error — the button explains the wait
     instead of silently doing nothing. */
  let rechecking = $state<Record<string, boolean>>({});
  let recheckWait = $state<Record<string, number>>({});

  async function recheckUsage(type: string, name: string): Promise<void> {
    const key = connectionKey(type, name);
    rechecking = { ...rechecking, [key]: true };
    recheckWait = { ...recheckWait, [key]: 0 };
    try {
      const r = await apiLoginTTYUsageRefresh(base, type, name);
      if (!r.accepted) {
        recheckWait = { ...recheckWait, [key]: r.waitS };
      }
      await loadConnections();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Failed to re-check usage");
    } finally {
      rechecking = { ...rechecking, [key]: false };
    }
  }

  function isWick(p: ProviderStatusDTO): boolean {
    return p.Instance.Type === "wick";
  }

  function setBusy(key: string, val: boolean): void {
    busy = { ...busy, [key]: val };
  }

  async function doRescanAll(): Promise<void> {
    setBusy("rescan-all", true);
    try {
      await apiRescanAll();
      toastOk("Rescan triggered");
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Rescan failed");
    } finally {
      setBusy("rescan-all", false);
    }
  }

  async function doRescanOne(p: ProviderStatusDTO): Promise<void> {
    const key = `rescan-${p.Instance.Type}-${p.Instance.Name}`;
    setBusy(key, true);
    try {
      await apiRescanOne(p.Instance.Type, p.Instance.Name);
      toastOk(`Rescanned ${p.Instance.Name}`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Rescan failed");
    } finally {
      setBusy(key, false);
    }
  }

  async function doDelete(p: ProviderStatusDTO): Promise<void> {
    confirmDelete = null;
    const key = `del-${p.Instance.Type}-${p.Instance.Name}`;
    setBusy(key, true);
    try {
      await apiDeleteProvider(p.Instance.Type, p.Instance.Name);
      toastOk(`Deleted ${p.Instance.Name}`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Delete failed");
    } finally {
      setBusy(key, false);
    }
  }

  async function doGateToggle(): Promise<void> {
    setBusy("gate", true);
    try {
      await apiGateToggle();
      toastOk("Gate toggled");
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Gate toggle failed");
    } finally {
      setBusy("gate", false);
    }
  }

  async function togglePrompt(): Promise<void> {
    setBusy("gate-mode", true);
    const next = data?.Gate.PermissionMode === "bypass" ? "on" : "bypass";
    try {
      await apiGateModes({ permission_mode: next });
      toastOk("Permission mode saved");
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Mode update failed");
    } finally {
      setBusy("gate-mode", false);
    }
  }

  async function doAutoRescanToggle(): Promise<void> {
    setBusy("auto-rescan", true);
    try {
      await apiAutoRescanToggle();
      toastOk("Auto-rescan toggled");
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Auto-rescan toggle failed");
    } finally {
      setBusy("auto-rescan", false);
    }
  }

  async function doMCPInstall(clientID: string): Promise<void> {
    setBusy(`mcp-${clientID}`, true);
    try {
      await apiMCPInstall(clientID);
      toastOk("MCP client installed");
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Install failed");
    } finally {
      setBusy(`mcp-${clientID}`, false);
    }
  }

  async function doMCPUninstall(clientID: string): Promise<void> {
    setBusy(`mcp-${clientID}`, true);
    try {
      await apiMCPUninstall(clientID);
      toastOk("MCP client uninstalled");
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Uninstall failed");
    } finally {
      setBusy(`mcp-${clientID}`, false);
    }
  }

  async function doHookEnable(p: ProviderStatusDTO): Promise<void> {
    const key = `hook-${p.Instance.Type}-${p.Instance.Name}`;
    setBusy(key, true);
    try {
      await apiHookEnable(base, p.Instance.Type, p.Instance.Name, HOOK_EVENT);
      toastOk(`Hook enabled for ${p.Instance.Name}`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Enable failed");
    } finally {
      setBusy(key, false);
    }
  }

  async function doHookDisable(p: ProviderStatusDTO): Promise<void> {
    const key = `hook-${p.Instance.Type}-${p.Instance.Name}`;
    setBusy(key, true);
    try {
      await apiHookDisable(base, p.Instance.Type, p.Instance.Name, HOOK_EVENT);
      toastOk(`Hook disabled for ${p.Instance.Name}`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Disable failed");
    } finally {
      setBusy(key, false);
    }
  }

  async function doHookCheck(p: ProviderStatusDTO): Promise<void> {
    const key = `hook-${p.Instance.Type}-${p.Instance.Name}`;
    setBusy(key, true);
    try {
      await apiHookCheck(base, p.Instance.Type, p.Instance.Name, HOOK_EVENT);
      toastOk(`Probe triggered for ${p.Instance.Name}`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Test failed");
    } finally {
      setBusy(key, false);
    }
  }

  function shortID(id: string): string {
    return id.slice(0, 8);
  }

  function openAdd(): void {
    formType = data?.SupportedKeys[0] ?? "";
    formName = "";
    formBinary = "";
    formExtraArgs = "";
    formEnv = "";
    formUseAirouter = false;
    formAirouterProvider = "";
    formAirouterModels = {};
    formAirouterKey = "";
    formAirouterRawConfig = "";
    formStoreOverride = false;
    formStoreValue = "";
    formBinarySource = "managed";
    addOpen = true;
  }

  async function doCreate(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!formType || !formName.trim() || formNameError || formStoreError) {
      return;
    }
    const createdType = formType;
    const createdName = formName.trim();
    const storeOverride = formIsolated && formStoreOverride ? formStoreValue.trim() : "";
    setBusy("create", true);
    try {
      await apiCreateProvider({
        type: formType,
        name: formName.trim(),
        binary: formIsolated && formBinarySource === "managed" ? "" : formBinary.trim(),
        extra_args: formExtraArgs.trim(),
        env: formEnv,
        use_airouter: formUseAirouter && airouterSupported,
        airouter_provider: formAirouterProvider,
        airouter_models: formAirouterModels,
        airouter_api_key: formAirouterKey,
        airouter_raw_config: formAirouterRawConfig,
        omp_profile: createdType === "omp" ? storeOverride : "",
        opencode_data_dir: createdType === "opencode" ? storeOverride : "",
      });
      toastOk(`Created ${createdName}`);
      addOpen = false;
      await load(true);
      // One instance = one account: the next step is always to log that
      // account in, so go straight to the detail page's Connection panel
      // (it opens with the login picker expanded).
      if (ACCOUNT_ISOLATED.has(createdType)) {
        onNavigate(createdType, createdName);
      }
    } catch (err) {
      toastError(err instanceof Error ? err.message : "Create failed");
    } finally {
      setBusy("create", false);
    }
  }

  function isBuiltin(p: ProviderStatusDTO): boolean {
    return p.Instance.Name === p.Instance.Type;
  }

  function capLabel(cap: ProviderStatusDTO["Cap"]): string {
    return cap.Unlimited ? `${cap.Used} / ∞` : `${cap.Used} / ${cap.Max}`;
  }

  function configuredCount(): number {
    return data?.Providers.length ?? 0;
  }

  const mcpCounts = $derived.by(() => {
    let installed = 0;
    let detected = 0;
    for (const c of data?.MCPClients.Clients ?? []) {
      if (c.Detected) {
        detected += 1;
        if (c.Installed) {
          installed += 1;
        }
      }
    }
    return { installed, detected };
  });

  const gateState = $derived.by(() => {
    const g = data?.Gate;
    if (!g) {
      return { label: "", cls: "", dot: "" };
    }
    if (g.BypassLocked) {
      return { label: "locked (bypass)", cls: "bg-amber-500", dot: "bg-amber-500" };
    }
    if (g.Enabled) {
      return { label: "enabled", cls: "bg-green-500", dot: "bg-green-500" };
    }
    return { label: "⚠ not configured", cls: "bg-red-500", dot: "bg-red-500 animate-pulse" };
  });

  $effect(() => {
    void base;
    void load();
    pollInterval = setInterval(() => void load(true), 4000);
    return () => {
      if (pollInterval !== null) {
        clearInterval(pollInterval);
      }
    };
  });
</script>

<!-- "Checking usage…" — shown while a probe for this account is in
     flight. Usage is read on a paced, shared schedule, so without this
     the page would look frozen on an old number while it works. -->
{#snippet checkingLabel()}
  <span class="inline-flex items-center gap-1 whitespace-nowrap text-black-700 dark:text-black-600" data-testid="usage-checking">
    <svg width="11" height="11" viewBox="0 0 12 12" fill="none" aria-hidden="true" class="shrink-0 animate-spin">
      <circle cx="6" cy="6" r="4.4" stroke="currentColor" stroke-width="1.2" stroke-opacity="0.25" />
      <path d="M10.4 6A4.4 4.4 0 0 0 6 1.6" stroke="currentColor" stroke-width="1.2" stroke-linecap="round" />
    </svg>
    Checking usage…
  </span>
{/snippet}

<!-- Re-check: ask for a fresh reading now, even when the card already
     has numbers. The server still decides whether a probe may go out
     (its own floor, and any cooldown the endpoint asked for), and a
     refusal comes back as a wait this button spells out. -->
{#snippet recheckButton(type: string, name: string, busy: boolean, waitS: number)}
  {#if canManage(type, name)}
  <span class="inline-flex items-center gap-1">
    <button
      type="button"
      data-testid="usage-recheck"
      class="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs text-link-400 hover:bg-white-300 dark:hover:bg-navy-600 disabled:opacity-50 disabled:hover:bg-transparent"
      disabled={busy}
      title={busy ? "Checking usage…" : "Check this account's usage now"}
      onclick={(e) => {
        e.stopPropagation();
        void recheckUsage(type, name);
      }}
    >
      Re-check
    </button>
    {#if waitS > 0}
      <span class="whitespace-nowrap text-xs text-black-600 dark:text-black-700" title="A probe now would land inside a cooldown, so it was not sent">wait {fmtSecsShort(waitS)}</span>
    {/if}
  </span>
  {/if}
{/snippet}

<!-- Info icon (circled "i") beside an account-isolated type: what one instance holds.
     Hover shows the title; a tap toggles a small popover (touch screens
     never show a title). `align` is the edge the popover hangs from, so it
     opens into the card instead of past its edge. -->
{#snippet accountHintIcon(key: string, type: string, testid: string, align: "left" | "right")}
  {@const hint = accountHint(type)}
  <span class="relative inline-flex shrink-0">
    <button
      type="button"
      data-testid={testid}
      title={hint}
      aria-label={hint}
      aria-expanded={hintOpen === key}
      class="inline-flex h-4 w-4 items-center justify-center rounded-full text-black-600 dark:text-black-700 hover:text-black-800 dark:hover:text-black-500"
      onclick={(e) => {
        e.stopPropagation();
        hintOpen = hintOpen === key ? null : key;
      }}
      onblur={() => { if (hintOpen === key) hintOpen = null; }}
    >
      <svg data-icon="info" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" aria-hidden="true" class="h-4 w-4">
        <circle cx="8" cy="8" r="6.3" />
        <path d="M8 7.3v3.6" />
        <circle cx="8" cy="5" r="0.35" fill="currentColor" />
      </svg>
    </button>
    {#if hintOpen === key}
      <span role="tooltip" data-testid={`${testid}-popover`} class="absolute {align === 'left' ? 'left-0' : 'right-0'} top-full z-20 mt-1 w-56 max-w-[calc(100vw-3rem)] rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2.5 py-1.5 text-[11px] font-normal text-black-800 dark:text-black-600 shadow-lg">{hint}</span>
    {/if}
  </span>
{/snippet}

<div class="space-y-6">
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Providers</h1>
    <div class="flex items-center gap-2">
      {#if !isAdmin}
        <span class="rounded-full border border-white-400 dark:border-navy-600 px-3 py-1 text-xs text-black-700 dark:text-black-600" title="Provider configuration is admin-only. You can view these providers and, where granted, reconnect them.">read-only</span>
      {/if}
      {#if isAdmin}
      <button
        type="button"
        onclick={doAutoRescanToggle}
        disabled={busy["auto-rescan"]}
        class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
      >Auto-rescan: {data?.AutoRescan ? "on" : "off"}</button>
      <button
        type="button"
        onclick={doRescanAll}
        disabled={busy["rescan-all"]}
        class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
      >{busy["rescan-all"] ? "Rescanning…" : "Rescan all"}</button>
      <button
        type="button"
        onclick={openAdd}
        class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 active:bg-green-700 transition-colors"
      >+ Add Custom</button>
      {/if}
    </div>
  </div>

  {#if loading}
    <div class="text-sm text-black-600 dark:text-black-500">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if data}
    <!-- Pool counters and the live process table describe the HOST, not
         a provider, so they stay with the admin page. -->
    {#if isAdmin}
    <div class="grid grid-cols-2 gap-4 sm:grid-cols-3">
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm">
        <p class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">Active Slots</p>
        <p class="mt-1 text-3xl font-bold text-blue-600 dark:text-blue-400">{data.PoolActive} / {data.PoolMax}</p>
      </div>
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm">
        <p class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">Queued</p>
        <p class="mt-1 text-3xl font-bold text-amber-600 dark:text-amber-400">{data.PoolQueueLen}</p>
      </div>
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm">
        <p class="text-xs font-medium text-black-700 dark:text-black-600 uppercase tracking-wide">Configured</p>
        <p class="mt-1 text-3xl font-bold text-black-900 dark:text-white-100">{configuredCount()}</p>
      </div>
    </div>

    {#if data.LiveProcesses.length > 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="px-5 py-3 flex items-center justify-between border-b border-white-300 dark:border-navy-600">
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Active Processes</h2>
          <span class="rounded bg-blue-100 dark:bg-blue-900 px-2 py-0.5 text-xs font-medium text-blue-700 dark:text-blue-300">{data.LiveProcesses.length} / {data.PoolMax}</span>
        </div>
        <table class="w-full text-xs">
          <thead>
            <tr class="border-b border-white-300 dark:border-navy-600 text-black-700 dark:text-black-600">
              <th class="px-5 py-2 text-left font-medium">Session</th>
              <th class="px-5 py-2 text-left font-medium">Agent</th>
              <th class="px-5 py-2 text-left font-medium">PID</th>
              <th class="px-5 py-2 text-left font-medium">State</th>
            </tr>
          </thead>
          <tbody>
            {#each data.LiveProcesses as proc (proc.SessionID)}
              <tr class="border-b border-white-300 dark:border-navy-600 last:border-0 hover:bg-white-200 dark:hover:bg-navy-800">
                <td class="px-5 py-2 font-mono text-black-900 dark:text-white-100">{shortID(proc.SessionID)}</td>
                <td class="px-5 py-2 text-black-900 dark:text-white-100">{proc.AgentName}</td>
                <td class="px-5 py-2 font-mono text-black-700 dark:text-black-600">{proc.PID > 0 ? proc.PID : "—"}</td>
                <td class="px-5 py-2">
                  <span class="rounded px-2 py-0.5 text-xs font-medium bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600">{proc.Lifecycle || "—"}{proc.Substate ? ` · ${proc.Substate}` : ""}</span>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
    {/if}

    {#if data.Providers.length === 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-12 text-center text-sm text-black-700 dark:text-black-600">
        No providers detected. Run Rescan all to discover installed AI providers.
      </div>
    {:else}
      <div class="grid grid-cols-1 gap-4 lg:grid-cols-2 overflow-x-clip">
        {#each data.Providers as p (`${p.Instance.Type}/${p.Instance.Name}`)}
          {#if isWick(p)}
            {@const ready = (wickInfo?.count ?? 0) > 0}
            <div class="rounded-xl border border-green-500 bg-white-100 dark:bg-navy-700 p-5 shadow-sm space-y-2 flex flex-col">
              <div class="flex items-start justify-between gap-3">
                <div class="flex min-w-0 flex-1 items-center gap-2"><ProviderIcon value="wick" class="w-5 h-5 shrink-0" /><p class="min-w-0 line-clamp-2 break-all text-base font-semibold text-black-900 dark:text-white-100">Wick</p></div>
                <span class="shrink-0 whitespace-nowrap rounded-full bg-green-100 dark:bg-green-900 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-300">Built-in</span>
              </div>
              <p class="text-xs text-black-700 dark:text-black-600">Runs inside wick — no CLI, no PATH setup.</p>
              <div class="flex items-center justify-between text-xs">
                <span class="text-black-700 dark:text-black-600">Models</span>
                <span class="text-black-900 dark:text-white-100">{wickInfo === null ? "…" : `${wickInfo.count} registered`}</span>
              </div>
              <div class="flex items-center justify-between text-xs">
                <span class="text-black-700 dark:text-black-600">Default</span>
                <span class="font-mono text-black-900 dark:text-white-100 truncate max-w-[60%]">{wickInfo?.defaultLabel || "—"}</span>
              </div>
              <div class="pt-3 border-t border-white-300 dark:border-navy-600 flex items-center justify-between gap-2">
                <span class="text-xs font-semibold text-black-900 dark:text-white-100">Command Gate</span>
                {#if data.Gate.BypassLocked}
                  <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-600">locked (bypass)</span>
                {:else if data.Gate.Enabled}
                  <span class="rounded bg-green-500 px-2 py-0.5 text-xs font-medium text-white-100">enforced ✓</span>
                {:else}
                  <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-800 dark:text-black-600">off</span>
                {/if}
              </div>
              <p class="text-xs text-black-700 dark:text-black-600 leading-relaxed">
                {#if data.Gate.BypassLocked}
                  Permission policy is set to bypass — the shell tool runs unguarded.
                {:else if data.Gate.Enabled}
                  Enforced in-process on every shell call — no separate Enable/Test needed here.
                {:else}
                  Off — turn on the master Command Gate below to whitelist shell commands.
                {/if}
              </p>
              <div class="mt-2 pt-3 border-t border-white-300 dark:border-navy-600 flex items-center justify-between">
                {#if ready}
                  <span class="rounded bg-green-50 dark:bg-green-900 px-2 py-0.5 text-xs font-medium text-green-700 dark:text-green-300">Ready</span>
                {:else}
                  <span class="rounded bg-amber-50 dark:bg-amber-900 px-2 py-0.5 text-xs font-medium text-amber-700 dark:text-amber-300">Needs setup</span>
                {/if}
                <button
                  type="button"
                  onclick={() => onNavigate(p.Instance.Type, p.Instance.Name)}
                  class="text-xs rounded-lg border border-white-400 dark:border-navy-600 px-2 py-1 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
                >Detail</button>
              </div>
            </div>
          {:else}
          {@const rescanKey = `rescan-${p.Instance.Type}-${p.Instance.Name}`}
          {@const delKey = `del-${p.Instance.Type}-${p.Instance.Name}`}
          {@const hookKey = `hook-${p.Instance.Type}-${p.Instance.Name}`}
          {@const hc = p.Hooks[HOOK_EVENT]}
          {@const intent = p.HookEnabled[HOOK_EVENT] === true}
          {@const conn = connections[connectionKey(p.Instance.Type, p.Instance.Name)]}
          {@const mbin = !p.Instance.Binary ? managedByType[p.Instance.Type] : undefined}
          <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm space-y-3">
            <!-- The actions keep the top-right corner at every width. The
                 cap + info icon ride right after the name (never wrapping
                 themselves); a long name clamps to two lines beside them
                 instead of pushing them, or the buttons, away. -->
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0 flex-1">
                <div class="flex items-start gap-2 min-w-0">
                  <ProviderIcon value={p.Instance.Type} class="w-5 h-5 shrink-0 mt-0.5" />
                  <p data-testid="card-name" title={`${p.Instance.Type}/${p.Instance.Name}`} class="min-w-0 line-clamp-2 break-all text-base font-semibold text-black-900 dark:text-white-100">{p.Instance.Type}/{p.Instance.Name}</p>
                  <div class="shrink-0 mt-0.5 flex items-center gap-1.5">
                    <span data-testid="card-cap" class={`whitespace-nowrap rounded px-1.5 py-0.5 text-xs font-medium ${p.Cap.Used > 0 ? "bg-blue-100 dark:bg-blue-900 text-blue-700 dark:text-blue-300" : "bg-white-300 dark:bg-navy-600 text-black-600 dark:text-black-500"}`}>{capLabel(p.Cap)}</span>
                    {#if p.Instance.IdleCompact}
                      {@const ic = p.Instance.IdleCompact}
                      <span
                        data-testid="card-idle-compact"
                        title={`Compact when idle: after ${idleLabel(ic.Seconds)} idle, once the context is at least ${ic.Trigger === "tokens" ? `${tokensLabel(ic.Threshold)} tokens` : `${ic.Threshold}%`}${ic.Scope === "all" ? ", every session" : ic.Scope === "whitelist" ? `, only sessions matching: ${ic.Match.join(", ")}` : `, except sessions matching: ${ic.Match.join(", ")}`}`}
                        class="whitespace-nowrap rounded px-1.5 py-0.5 text-xs font-medium bg-green-100 dark:bg-green-900 text-green-700 dark:text-green-300"
                      >auto-compact {idleLabel(ic.Seconds)} · {ic.Trigger === "tokens" ? tokensLabel(ic.Threshold) : `${ic.Threshold}%`}{ic.Scope === "whitelist" ? " · whitelist" : ic.Scope === "skip" ? ` · skip ${ic.Match.length}` : " · all"}</span>
                    {/if}
                    {#if ACCOUNT_ISOLATED.has(p.Instance.Type)}
                      {@render accountHintIcon(`card-${p.Instance.Type}-${p.Instance.Name}`, p.Instance.Type, "one-account-badge", "left")}
                    {/if}
                  </div>
                </div>
                {#if ACCOUNT_ISOLATED.has(p.Instance.Type)}
                  <!-- Several omp/opencode instances differ only by account,
                       so the account is part of the card's identity. -->
                  {#if !connectionsLoaded || conn?.accountUnknown}
                    <!-- Login state comes from a separate request; until it
                         lands the account is unknown, not logged out. -->
                    <p data-testid="card-account-loading" class="text-xs mt-0.5 inline-flex items-center gap-1 text-black-700 dark:text-black-600">
                      <svg class="w-3 h-3 animate-spin" viewBox="0 0 24 24" fill="none" aria-hidden="true"><circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="3" opacity="0.25" /><path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" stroke-width="3" stroke-linecap="round" /></svg>
                      checking login…
                    </p>
                  {:else}
                    <p data-testid="card-account" class="text-xs mt-0.5 font-mono truncate {conn?.connected ? 'text-black-800 dark:text-black-600' : 'text-neg-400'}">
                      {conn?.connected ? (conn.email || conn.plan || "logged in") : "not logged in — open Detail to log in"}
                    </p>
                  {/if}
                {/if}
                {#if p.Instance.Disabled}
                  <p class="text-xs text-amber-600 dark:text-amber-400 mt-0.5">disabled</p>
                {:else if !p.PathFound}
                  <p class="text-xs text-red-600 dark:text-red-400 mt-0.5">not found on PATH</p>
                {:else if p.VersionErr}
                  <p class="text-xs text-red-600 dark:text-red-400 mt-0.5">version probe failed</p>
                {:else}
                  <p class="text-xs text-green-600 dark:text-green-400 mt-0.5">{p.Version}</p>
                {/if}
              </div>
              <div class="flex shrink-0 items-center gap-2">
                <!-- Rescan re-probes the binary on the HOST and rewrites
                     the cached status, so it is admin-only like the rest
                     of the configuration surface. Hidden rather than
                     disabled: a manager has no use for it. -->
                {#if isAdmin}
                  <button
                    type="button"
                    onclick={() => doRescanOne(p)}
                    disabled={busy[rescanKey]}
                    class="rounded-lg border border-white-400 dark:border-navy-600 px-2 py-1 text-xs text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
                  >{busy[rescanKey] ? "…" : "Rescan"}</button>
                {/if}
                <button
                  type="button"
                  onclick={() => onNavigate(p.Instance.Type, p.Instance.Name)}
                  class="text-xs rounded-lg border border-white-400 dark:border-navy-600 px-2 py-1 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
                >Detail</button>
              </div>
            </div>
            <dl class="text-xs space-y-1">
              <div class="flex gap-2">
                <dt class="w-20 shrink-0 text-black-700 dark:text-black-600">resolved</dt>
                {#if p.Path}
                  <dd class="font-mono text-black-900 dark:text-white-100 break-all">
                    {p.Path}
                    {#if sourceLabel(p.Source)}
                      <span data-testid="card-binary-source" class="ml-1 rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 font-sans text-[11px] font-medium text-black-800 dark:text-black-600">{sourceLabel(p.Source)}</span>
                    {/if}
                  </dd>
                {:else if ACCOUNT_ISOLATED.has(p.Instance.Type) && !mbin}
                  <dd data-testid="card-binary-missing" class="text-neg-400">binary not installed — open Detail to download it, or set a path</dd>
                {:else}
                  <dd class="text-black-600 dark:text-black-700">—</dd>
                {/if}
              </div>
              {#if mbin}
                <div class="flex gap-2">
                  <dt class="w-20 shrink-0 text-black-700 dark:text-black-600">binary</dt>
                  <dd>
                    <button
                      type="button"
                      data-testid="card-managed-binary"
                      data-state={mbin.current ? "installed" : "missing"}
                      title="Manage versions on Detail"
                      onclick={() => onNavigate(p.Instance.Type, p.Instance.Name)}
                      class="inline-flex flex-wrap items-center gap-1.5 text-left hover:underline"
                    >
                      {#if mbin.current}
                        <span class="font-mono text-black-900 dark:text-white-100">v{mbin.current}</span>
                        <span class="text-black-700 dark:text-black-600">managed by wick</span>
                      {:else}
                        <span class="text-neg-400">binary not installed — open Detail to download it</span>
                      {/if}
                      {#if isRunning(mbin.job) && mbin.job}
                        <span data-testid="card-managed-job" class="text-black-800 dark:text-black-600">{jobShort(mbin.job)}</span>
                      {:else if mbin.updateAvailable}
                        <span data-testid="card-managed-update" class="rounded bg-cau-100 dark:bg-cau-400/20 px-1.5 py-0.5 text-[11px] font-medium text-cau-400">update available {mbin.latest}</span>
                      {/if}
                    </button>
                  </dd>
                </div>
              {/if}
              {#if p.VersionErr}
                <div class="flex gap-2">
                  <dt class="w-20 shrink-0 text-black-700 dark:text-black-600">error</dt>
                  <dd class="font-mono text-red-600 dark:text-red-400 break-all">{p.VersionErr}</dd>
                </div>
              {/if}
            </dl>
            <!-- Account + usage: which login this instance runs as, and how
                 much of its rate-limit windows is spent. Two nested arcs
                 (inner 5-hour, outer 7-day) keep it to one glance; the
                 numbers are spelled out beside them. Until the connections
                 request resolves, every non-wick card shows a "checking"
                 row instead of letting the block pop in. -->
            {#if !connectionsLoaded && p.Instance.Type !== "wick"}
              <div data-testid="conn-loading" class="pt-3 border-t border-white-300 dark:border-navy-600 flex items-center gap-2 text-xs text-black-700 dark:text-black-600">
                <svg class="w-3 h-3 animate-spin" viewBox="0 0 24 24" fill="none" aria-hidden="true"><circle cx="12" cy="12" r="9" stroke="currentColor" stroke-width="3" opacity="0.25" /><path d="M21 12a9 9 0 0 0-9-9" stroke="currentColor" stroke-width="3" stroke-linecap="round" /></svg>
                Checking login &amp; usage…
              </div>
            {:else if conn}
              {@const rings = pickWindows(conn.windows)}
              {@const ckey = connectionKey(p.Instance.Type, p.Instance.Name)}
              {@const busy = conn.usageChecking || rechecking[ckey] === true}
              <div class="pt-3 border-t border-white-300 dark:border-navy-600 flex items-center gap-3">
                {#if rings.inner || rings.outer}
                  <UsageRings windows={conn.windows} />
                {/if}
                <div class="min-w-0 flex-1 space-y-0.5">
                  <div class="flex items-center gap-2 flex-wrap">
                    {#if conn.accountUnknown}
                      <span data-testid="conn-checking" class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-500">Checking login…</span>
                    {:else if conn.connected}
                      <span class="rounded bg-pos-100 dark:bg-pos-400/20 px-2 py-0.5 text-xs font-medium text-pos-400">Connected</span>
                    {:else}
                      <span class="rounded bg-neg-100 dark:bg-neg-400/20 px-2 py-0.5 text-xs font-medium text-neg-400">Not connected</span>
                    {/if}
                    {#if conn.email}
                      <span class="min-w-0 truncate font-mono text-xs text-black-800 dark:text-black-600">{conn.email}</span>
                    {/if}
                  </div>
                  {#if rings.inner || rings.outer}
                    <!-- "5h 42% ↻3h": window, usage, and how long until it
                         resets. The ↻ is what separates the reset time from
                         the window's own length — "5h … 3h" side by side is
                         ambiguous without it — and the title spells it out.
                         Date.now() is re-read on the list's 4s poll, so the
                         countdown never sits stale. -->
                    <div class="flex items-center gap-3 text-xs text-black-700 dark:text-black-600">
                      {#each [{ w: rings.inner, tag: "5h" }, { w: rings.outer, tag: "7d" }] as slot (slot.tag)}
                        {#if slot.w}
                          {@const reset = resetHint(slot.w, Date.now())}
                          <span class="inline-flex items-baseline gap-1 whitespace-nowrap">
                            {slot.tag}
                            <span class="font-medium text-black-900 dark:text-white-100">{Math.round(slot.w.utilization)}%</span>
                            {#if reset.short}
                              <span class="text-black-600 dark:text-black-700" title={reset.full} aria-label={reset.full}>↻{reset.short}</span>
                            {/if}
                          </span>
                        {/if}
                      {/each}
                      <!-- These numbers came from a server-side cache
                           shared by every instance on this account, so
                           the row says how old they are rather than
                           implying a fetch per paint. -->
                      {#if busy}
                        {@render checkingLabel()}
                      {:else}
                        <UsageCacheChip ageS={conn.usageAgeS} nextS={conn.usageNextS} fetchedAt={conn.usageFetchedAt} />
                      {/if}
                      {@render recheckButton(p.Instance.Type, p.Instance.Name, busy, recheckWait[ckey] ?? 0)}
                    </div>
                  {:else if busy || conn.usagePending}
                    <!-- First probe for this account is queued behind the
                         pacing gate that keeps us under the endpoint's
                         rate limit. It lands on a later poll. -->
                    <div class="flex items-center gap-2 text-xs">
                      {@render checkingLabel()}
                      {@render recheckButton(p.Instance.Type, p.Instance.Name, true, recheckWait[ckey] ?? 0)}
                    </div>
                  {:else if conn.usageErr}
                    <div class="flex items-center gap-2 min-w-0 text-xs">
                      <p class="font-mono text-black-700 dark:text-black-600 truncate">usage unavailable: {conn.usageErr}</p>
                      {#if conn.usageNextS > 0}
                        <span class="whitespace-nowrap text-black-600 dark:text-black-700" title="Nothing retries on its own; this is when a Re-check would be accepted">re-check in {fmtSecsShort(conn.usageNextS)}</span>
                      {/if}
                      {@render recheckButton(p.Instance.Type, p.Instance.Name, busy, recheckWait[ckey] ?? 0)}
                    </div>
                  {:else if conn.usageSupported}
                    <div class="flex items-center gap-2 text-xs">
                      {@render recheckButton(p.Instance.Type, p.Instance.Name, busy, recheckWait[ckey] ?? 0)}
                    </div>
                  {/if}
                </div>
              </div>
            {/if}
            <!-- Per-instance Command Gate: enabling a hook changes what
                 agents may run on this host, so it stays admin-only. -->
            {#if isAdmin}
            <div class="pt-3 border-t border-white-300 dark:border-navy-600 space-y-2">
              <div class="flex items-center justify-between gap-2">
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="text-xs font-semibold text-black-900 dark:text-white-100">Command Gate</span>
                  {#if data.Gate.BypassLocked}
                    <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-600">locked (bypass)</span>
                  {:else if !data.Gate.Enabled}
                    <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-600">locked</span>
                  {:else if p.Probing}
                    <span class="rounded bg-blue-500 px-2 py-0.5 text-xs font-medium text-white-100 animate-pulse">testing…</span>
                  {:else if intent && hc?.Verified}
                    <span class="rounded bg-green-500 px-2 py-0.5 text-xs font-medium text-white-100">enabled ✓</span>
                  {:else if intent && !hc?.Verified}
                    <span class="rounded bg-amber-500 px-2 py-0.5 text-xs font-medium text-white-100">enabled (unverified)</span>
                  {:else if hc?.Verified}
                    <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-800 dark:text-black-600">ready</span>
                  {:else}
                    <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-800 dark:text-black-600">disabled</span>
                  {/if}
                  {#if hc?.Scope}
                    <span class="text-xs text-black-700 dark:text-black-600">scope: {hc.Scope}</span>
                  {/if}
                </div>
                {#if data.Gate.Enabled && !data.Gate.BypassLocked}
                  <div class="flex items-center gap-1 shrink-0">
                    {#if p.Probing}
                      <button type="button" disabled class="rounded-lg border border-white-400 dark:border-navy-600 px-2 py-1 text-xs text-black-700 dark:text-black-600 opacity-60 cursor-not-allowed">Testing…</button>
                    {:else if intent}
                      <button
                        type="button"
                        onclick={() => doHookCheck(p)}
                        disabled={busy[hookKey]}
                        class="rounded-lg border border-white-400 dark:border-navy-600 px-2 py-1 text-xs text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
                      >Test</button>
                      <button
                        type="button"
                        onclick={() => doHookDisable(p)}
                        disabled={busy[hookKey]}
                        class="rounded-lg border border-red-400 dark:border-red-700 px-2 py-1 text-xs text-red-700 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 disabled:opacity-50"
                      >Disable</button>
                    {:else}
                      <button
                        type="button"
                        onclick={() => doHookEnable(p)}
                        disabled={busy[hookKey]}
                        class="rounded-lg bg-green-500 px-3 py-1 text-xs font-medium text-white-100 hover:bg-green-600 disabled:opacity-50"
                      >Enable</button>
                    {/if}
                  </div>
                {/if}
              </div>
              {#if hc?.Error}
                <p class="font-mono text-xs text-red-600 dark:text-red-400 break-all">{hc.Error}</p>
              {/if}
              {#if hc?.ProbedAt}
                <p class="font-mono text-xs text-black-700 dark:text-black-600">last probed: {hc.ProbedAt}</p>
              {/if}
            </div>
            {/if}
            {#if !isBuiltin(p) && isAdmin}
              <div class="pt-2 border-t border-white-300 dark:border-navy-600">
                <button
                  type="button"
                  onclick={() => { confirmDelete = p; }}
                  disabled={busy[delKey]}
                  class="text-xs text-red-600 dark:text-red-400 hover:underline disabled:opacity-50"
                >Delete instance</button>
              </div>
            {/if}
          </div>
          {/if}
        {/each}
      </div>
    {/if}

    <!-- Everything below is operator surface: the master Command Gate,
         the MCP client installs and the spawn log. A viewer gets their
         providers and nothing that would let them act on the host. -->
    {#if isAdmin}
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
      <div class="border-b border-white-300 dark:border-navy-600 px-5 py-3 flex items-center justify-between gap-3">
        <div class="flex items-center gap-2">
          <span class={`inline-flex h-2 w-2 rounded-full ${gateState.dot}`}></span>
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">Command Gate</h2>
          <span class={`rounded px-2 py-0.5 text-xs font-medium text-white-100 ${gateState.cls}`}>{gateState.label}</span>
        </div>
        <div class="flex items-center gap-3">
          {#if data.Gate.Source}
            <span class="font-mono text-xs text-black-700 dark:text-black-600">resolved via {data.Gate.Source}</span>
          {/if}
          {#if data.Gate.BypassLocked}
            <button type="button" disabled class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1 text-xs font-medium text-black-700 dark:text-black-600 opacity-60 cursor-not-allowed">Locked</button>
          {:else}
            <button
              type="button"
              onclick={doGateToggle}
              disabled={busy["gate"]}
              class={data.Gate.Enabled
                ? "rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
                : "rounded-lg bg-green-500 px-3 py-1 text-xs font-medium text-white-100 hover:bg-green-600 disabled:opacity-50"}
            >{data.Gate.Enabled ? "Turn off" : "Turn on"}</button>
          {/if}
        </div>
      </div>
      <div class="px-5 py-3 space-y-3 text-xs">
        {#if data.Gate.Enabled}
          <div class="flex gap-2">
            <dt class="w-24 shrink-0 text-black-700 dark:text-black-600">binary</dt>
            <dd class="font-mono text-black-900 dark:text-white-100 break-all">{data.Gate.Binary}</dd>
          </div>
        {:else if data.Gate.Reason}
          <div class="flex gap-2">
            <dt class="w-24 shrink-0 text-black-700 dark:text-black-600">error</dt>
            <dd class="font-mono text-red-600 dark:text-red-400 break-all">{data.Gate.Reason}</dd>
          </div>
        {/if}
        <div class="flex items-center justify-between gap-3 pt-1">
          <div class="flex flex-col">
            <span class="text-xs font-medium text-black-900 dark:text-white-100">Prompt per tool call</span>
            <span class="text-xs text-black-700 dark:text-black-600">Off = run unguarded (Slack / HTTP)</span>
          </div>
          {#if data.Gate.PermissionMode === "bypass"}
            <button
              type="button"
              role="switch"
              aria-checked="false"
              aria-label="Prompt per tool call"
              onclick={togglePrompt}
              disabled={busy["gate-mode"]}
              class="relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent bg-white-400 dark:bg-navy-600 transition-colors hover:bg-white-500 dark:hover:bg-navy-500 disabled:opacity-50"
            >
              <span class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white-100 shadow ring-0 transition translate-x-0"></span>
            </button>
          {:else}
            <button
              type="button"
              role="switch"
              aria-checked="true"
              aria-label="Prompt per tool call"
              onclick={togglePrompt}
              disabled={busy["gate-mode"]}
              class="relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent bg-green-500 transition-colors hover:bg-green-600 disabled:opacity-50"
            >
              <span class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white-100 shadow ring-0 transition translate-x-4"></span>
            </button>
          {/if}
        </div>
        {#if data.Gate.Note}
          <p class="pt-1 text-black-800 dark:text-black-600 leading-relaxed">{data.Gate.Note}</p>
        {/if}
      </div>
    </div>

    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
      <button
        type="button"
        class="w-full px-5 py-3 flex items-center justify-between gap-3 text-left hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
        onclick={() => { mcpOpen = !mcpOpen; }}
      >
        <div class="flex items-center gap-2 flex-wrap">
          <svg class={`w-3.5 h-3.5 text-black-700 dark:text-black-600 transition-transform shrink-0 ${mcpOpen ? "rotate-90" : ""}`} fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7"/></svg>
          <h2 class="text-sm font-semibold text-black-900 dark:text-white-100">MCP Wick</h2>
          <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-600 font-mono">{data.MCPClients.AppName}</span>
          {#if mcpCounts.installed === mcpCounts.detected && mcpCounts.detected > 0}
            <span class="rounded bg-green-500 px-2 py-0.5 text-xs font-medium text-white-100">{mcpCounts.installed} / {mcpCounts.detected} installed</span>
          {:else if mcpCounts.installed > 0}
            <span class="rounded bg-amber-500 px-2 py-0.5 text-xs font-medium text-white-100">{mcpCounts.installed} / {mcpCounts.detected} installed</span>
          {:else if mcpCounts.detected > 0}
            <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-600">0 / {mcpCounts.detected} installed</span>
          {:else}
            <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-600">no clients detected</span>
          {/if}
        </div>
      </button>
      {#if mcpOpen}
        <div class="border-t border-white-300 dark:border-navy-600 divide-y divide-white-300 dark:divide-navy-600">
          {#each data.MCPClients.Clients as client (client.ID)}
            {@const mcpKey = `mcp-${client.ID}`}
            <div class="px-5 py-3 flex items-center justify-between gap-3">
              <div class="flex items-center gap-3 min-w-0">
                {#if client.Installed}
                  <span class="inline-flex h-2 w-2 rounded-full bg-green-500 shrink-0"></span>
                {:else}
                  <span class="inline-flex h-2 w-2 rounded-full bg-amber-500 animate-pulse shrink-0"></span>
                {/if}
                <div class="min-w-0">
                  <span class="text-xs font-medium text-black-900 dark:text-white-100">{client.Label || client.ID}</span>
                  {#if client.Installed}
                    <span class="ml-2 text-xs text-green-600 dark:text-green-400">installed</span>
                  {:else if client.Blocklisted}
                    <span class="ml-2 text-xs text-black-600 dark:text-black-700">skipped (manually uninstalled)</span>
                  {:else}
                    <span class="ml-2 text-xs text-amber-600 dark:text-amber-400">not installed</span>
                  {/if}
                  {#if client.ConfigPath}
                    <p class="font-mono text-xs text-black-600 dark:text-black-700 truncate mt-0.5" title={client.ConfigPath}>{client.ConfigPath}</p>
                  {/if}
                </div>
              </div>
              <div class="flex items-center gap-1 shrink-0">
                {#if client.Installed}
                  <button
                    type="button"
                    onclick={() => doMCPUninstall(client.ID)}
                    disabled={busy[mcpKey]}
                    class="rounded-lg border border-red-400 dark:border-red-700 px-2 py-1 text-xs text-red-700 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20 disabled:opacity-50"
                  >{busy[mcpKey] ? "…" : "Uninstall"}</button>
                {:else}
                  <button
                    type="button"
                    onclick={() => doMCPInstall(client.ID)}
                    disabled={busy[mcpKey]}
                    class="rounded-lg bg-green-500 px-3 py-1 text-xs font-medium text-white-100 hover:bg-green-600 disabled:opacity-50"
                  >{busy[mcpKey] ? "…" : "Install"}</button>
                {/if}
              </div>
            </div>
          {/each}
        </div>
      {/if}
    </div>

    <!-- The ledger sits under the provider cards and above the spawn
         log: the cards say what exists, this says what it cost, the log
         says what ran. Same page, narrowing scope. -->
    <UsageReport {base} />

    <RecentSpawns {base} {onOpenSession} />
    {/if}
  {/if}
</div>

<!-- Shared Modal: the body scrolls inside a 90vh cap and the footer stays
     pinned, so Create is reachable however tall the form grows (the managed
     binary panel alone adds a card). -->
<Modal open={addOpen} title="New Provider Instance" size="lg" closeOnBackdrop={false} onClose={() => { addOpen = false; }}>
      <form id="add-provider-form" onsubmit={doCreate} class="space-y-4">
        <div>
          <label for="add-provider-type" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Type <span class="text-red-500">*</span></label>
          <Select
            id="add-provider-type"
            name="type"
            value={formType}
            options={(data?.SupportedKeys ?? []).map(typeOption)}
            onChange={(v) => { formType = v; onTypeChange(); }}
          />
        </div>
        {#if formIsolated}
          <div data-testid="add-account-store" class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-3 py-2 space-y-2">
            <div class="flex items-center justify-between gap-2">
              <span class="text-xs font-medium text-black-800 dark:text-black-600">{formType === "omp" ? "omp profile" : "Data dir"}</span>
              {@render accountHintIcon("add", formType, "add-account-hint", "right")}
            </div>
            {#if formStoreOverride}
              <input
                type="text"
                bind:value={formStoreValue}
                placeholder={formType === "omp" ? "wick-work" : "/abs/path/to/data"}
                class="w-full rounded-lg border bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100 {formStoreError ? 'border-red-400 dark:border-red-600' : 'border-white-400 dark:border-navy-600'}"
              />
              {#if formStoreError}<p class="text-[11px] text-red-600 dark:text-red-400">{formStoreError}</p>{/if}
            {:else}
              <p class="font-mono text-xs text-black-900 dark:text-white-100 truncate">{accountStorePreview(formType, formName.trim())}</p>
            {/if}
            <p class="text-[11px] text-black-700 dark:text-black-600">
              Pinned when created — renaming the instance keeps this account.
              <button type="button" class="text-link-400 hover:underline" onclick={() => { formStoreOverride = !formStoreOverride; }}>{formStoreOverride ? "Use default" : "Override"}</button>
            </p>
            {#if formType === "opencode"}
              <p class="text-[11px] text-black-700 dark:text-black-600">Claude Pro/Max subscriptions are not supported by opencode.</p>
            {/if}
          </div>
        {/if}
        <div>
          <label for="add-provider-name" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Name <span class="text-red-500">*</span></label>
          <input
            id="add-provider-name"
            type="text"
            bind:value={formName}
            oninput={onNameInput}
            required
            placeholder="e.g. work, personal, claude_waba"
            class="w-full rounded-lg border bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 {formNameError ? 'border-red-400 dark:border-red-600' : 'border-white-400 dark:border-navy-600'}"
          />
          {#if formNameError}
            <p class="mt-1 text-[11px] text-red-600 dark:text-red-400">{formNameError}</p>
          {:else}
            <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Letters, digits and '_' only. Spaces auto-convert to '_'.</p>
          {/if}
        </div>
        {#if formIsolated && managedTypes.includes(formType)}
          <div class="space-y-2" data-testid="add-binary-source">
            <label for="add-binary-source" class="block text-xs font-medium text-black-800 dark:text-black-600">Binary</label>
            <Select
              id="add-binary-source"
              value={formBinarySource}
              options={[
                { label: "Managed by wick", value: "managed", description: "wick downloads, verifies and updates it from GitHub" },
                { label: "Manual path", value: "manual", description: "Advanced: point at a binary you installed yourself" },
              ]}
              onChange={(v) => { formBinarySource = v; }}
            />
            {#if formBinarySource === "managed"}
              <ManagedBinaryPanel {base} type={formType} compact />
            {/if}
          </div>
        {/if}
        {#if !formIsolated || !managedTypes.includes(formType) || formBinarySource === "manual"}
          <div>
            <label for="add-provider-binary" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Binary path (optional)</label>
            <input id="add-provider-binary" type="text" bind:value={formBinary} placeholder="leave empty to use PATH lookup" class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100" />
          </div>
        {/if}
        <div>
          <label for="add-provider-args" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Extra args (space separated)</label>
          <input id="add-provider-args" type="text" bind:value={formExtraArgs} class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100" />
        </div>
        <div>
          <label for="add-provider-env" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Env (one KEY=VALUE per line)</label>
          <textarea id="add-provider-env" bind:value={formEnv} rows="3" placeholder="ANTHROPIC_API_KEY=sk-..." class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100"></textarea>
        </div>
        <AIRouterConfig
          {base}
          type={formType}
          supported={airouterSupported}
          bind:useAirouter={formUseAirouter}
          bind:provider={formAirouterProvider}
          bind:models={formAirouterModels}
          bind:apiKey={formAirouterKey}
          bind:rawConfig={formAirouterRawConfig}
          routers={airouterRouters}
        />
      </form>
  {#snippet footer()}
    <button type="button" onclick={() => { addOpen = false; }} class="rounded-lg border border-white-400 dark:border-navy-600 px-4 py-2 text-sm text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">Cancel</button>
    <button type="submit" form="add-provider-form" disabled={busy["create"] || !!formNameError || !!formStoreError} class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">{busy["create"] ? "Creating…" : "Create"}</button>
  {/snippet}
</Modal>

<ConfirmDialog
  open={confirmDelete !== null}
  title={`Delete ${confirmDelete?.Instance.Name ?? ""}?`}
  body={confirmDelete && ACCOUNT_ISOLATED.has(confirmDelete.Instance.Type)
    ? "This removes the provider instance only. Its login stays on disk (omp profile under ~/.omp/profiles, or the opencode data dir under <wick data>/providers/opencode) — delete that folder yourself if you no longer need the account. Built-in providers cannot be deleted."
    : "This will remove the provider instance. Built-in providers cannot be deleted."}
  confirmLabel="Delete"
  destructive={true}
  onConfirm={() => { if (confirmDelete) { doDelete(confirmDelete); } }}
  onCancel={() => { confirmDelete = null; }}
/>
