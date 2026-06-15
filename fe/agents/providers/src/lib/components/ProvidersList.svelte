<script lang="ts">
  import { ConfirmDialog } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
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
  } from "$lib/api.js";
  import type { ProvidersListResponse, ProviderStatusDTO } from "$lib/types.js";

  type Props = {
    onNavigate: (type: string, name: string) => void;
    base: string;
  };
  let { onNavigate, base }: Props = $props();

  let data = $state<ProvidersListResponse | null>(null);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let confirmDelete = $state<ProviderStatusDTO | null>(null);
  let busy = $state<Record<string, boolean>>({});
  let mcpOpen = $state(false);
  let addOpen = $state(false);

  let formType = $state("");
  let formName = $state("");
  let formBinary = $state("");
  let formExtraArgs = $state("");
  let formEnv = $state("");

  let pollInterval: ReturnType<typeof setInterval> | null = null;

  async function load(silent = false): Promise<void> {
    if (!silent) {
      loading = true;
      error = null;
    }
    try {
      data = await apiGetProviders();
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

  function openAdd(): void {
    formType = data?.SupportedKeys[0] ?? "";
    formName = "";
    formBinary = "";
    formExtraArgs = "";
    formEnv = "";
    addOpen = true;
  }

  async function doCreate(e: SubmitEvent): Promise<void> {
    e.preventDefault();
    if (!formType || !formName.trim()) {
      return;
    }
    setBusy("create", true);
    try {
      await apiCreateProvider({
        type: formType,
        name: formName.trim(),
        binary: formBinary.trim(),
        extra_args: formExtraArgs.trim(),
        env: formEnv,
      });
      toastOk(`Created ${formName.trim()}`);
      addOpen = false;
      await load(true);
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

<div class="space-y-6">
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Providers</h1>
    <div class="flex items-center gap-2">
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
    </div>
  </div>

  {#if loading}
    <div class="text-sm text-black-600 dark:text-black-500">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if data}
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

    {#if data.Providers.length === 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-12 text-center text-sm text-black-700 dark:text-black-600">
        No providers detected. Run Rescan all to discover installed AI providers.
      </div>
    {:else}
      <div class="grid grid-cols-1 gap-4 lg:grid-cols-2 overflow-x-clip">
        {#each data.Providers as p (`${p.Instance.Type}/${p.Instance.Name}`)}
          {@const rescanKey = `rescan-${p.Instance.Type}-${p.Instance.Name}`}
          {@const delKey = `del-${p.Instance.Type}-${p.Instance.Name}`}
          <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 shadow-sm space-y-3">
            <div class="flex items-start justify-between gap-3">
              <div>
                <div class="flex items-center gap-2">
                  <p class="text-base font-semibold text-black-900 dark:text-white-100">{p.Instance.Type}/{p.Instance.Name}</p>
                  <span class={`rounded px-1.5 py-0.5 text-xs font-medium ${p.Cap.Used > 0 ? "bg-blue-100 dark:bg-blue-900 text-blue-700 dark:text-blue-300" : "bg-white-300 dark:bg-navy-600 text-black-600 dark:text-black-500"}`}>{capLabel(p.Cap)}</span>
                </div>
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
              <div class="flex items-center gap-2">
                <button
                  type="button"
                  onclick={() => doRescanOne(p)}
                  disabled={busy[rescanKey]}
                  class="rounded-lg border border-white-400 dark:border-navy-600 px-2 py-1 text-xs text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
                >{busy[rescanKey] ? "…" : "Rescan"}</button>
                <button
                  type="button"
                  onclick={() => onNavigate(p.Instance.Type, p.Instance.Name)}
                  class="text-xs rounded-lg border border-white-400 dark:border-navy-600 px-2 py-1 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800"
                >Detail</button>
              </div>
            </div>
            <dl class="text-xs space-y-1">
              <div class="flex gap-2">
                <dt class="w-20 text-black-700 dark:text-black-600">resolved</dt>
                {#if p.Path}
                  <dd class="font-mono text-black-900 dark:text-white-100 break-all">{p.Path}</dd>
                {:else}
                  <dd class="text-black-600 dark:text-black-700">—</dd>
                {/if}
              </div>
              {#if p.VersionErr}
                <div class="flex gap-2">
                  <dt class="w-20 text-black-700 dark:text-black-600">error</dt>
                  <dd class="font-mono text-red-600 dark:text-red-400 break-all">{p.VersionErr}</dd>
                </div>
              {/if}
            </dl>
            <div class="pt-3 border-t border-white-300 dark:border-navy-600">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="text-xs font-semibold text-black-900 dark:text-white-100">Command Gate</span>
                {#if data.Gate.BypassLocked}
                  <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-600">locked (bypass)</span>
                {:else if !data.Gate.Enabled}
                  <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-700 dark:text-black-600">locked</span>
                {:else if p.HookEnabled["PreToolUse"]}
                  <span class="rounded bg-green-500 px-2 py-0.5 text-xs font-medium text-white-100">enabled ✓</span>
                {:else}
                  <span class="rounded bg-white-300 dark:bg-navy-600 px-2 py-0.5 text-xs font-medium text-black-800 dark:text-black-600">disabled</span>
                {/if}
              </div>
            </div>
            {#if !isBuiltin(p)}
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
        {/each}
      </div>
    {/if}

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
  {/if}
</div>

{#if addOpen}
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
    <div class="w-full max-w-lg rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-6 shadow-xl mx-4">
      <h2 class="mb-4 text-base font-semibold text-black-900 dark:text-white-100">New Provider Instance</h2>
      <form onsubmit={doCreate} class="space-y-4">
        <div>
          <label for="add-provider-type" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Type <span class="text-red-500">*</span></label>
          <select id="add-provider-type" bind:value={formType} required class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100">
            {#each data?.SupportedKeys ?? [] as k (k)}
              <option value={k}>{k}</option>
            {/each}
          </select>
        </div>
        <div>
          <label for="add-provider-name" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Name <span class="text-red-500">*</span></label>
          <input id="add-provider-name" type="text" bind:value={formName} required placeholder="e.g. work, personal" class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100" />
        </div>
        <div>
          <label for="add-provider-binary" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Binary path (optional)</label>
          <input id="add-provider-binary" type="text" bind:value={formBinary} placeholder="leave empty to use PATH lookup" class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100" />
        </div>
        <div>
          <label for="add-provider-args" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Extra args (space separated)</label>
          <input id="add-provider-args" type="text" bind:value={formExtraArgs} class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100" />
        </div>
        <div>
          <label for="add-provider-env" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">Env (one KEY=VALUE per line)</label>
          <textarea id="add-provider-env" bind:value={formEnv} rows="3" placeholder="ANTHROPIC_API_KEY=sk-..." class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100"></textarea>
        </div>
        <div class="flex justify-end gap-3 pt-2">
          <button type="button" onclick={() => { addOpen = false; }} class="rounded-lg border border-white-400 dark:border-navy-600 px-4 py-2 text-sm text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800">Cancel</button>
          <button type="submit" disabled={busy["create"]} class="rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">{busy["create"] ? "Creating…" : "Create"}</button>
        </div>
      </form>
    </div>
  </div>
{/if}

<ConfirmDialog
  open={confirmDelete !== null}
  title={`Delete ${confirmDelete?.Instance.Name ?? ""}?`}
  body="This will remove the provider instance. Built-in providers cannot be deleted."
  confirmLabel="Delete"
  destructive={true}
  onConfirm={() => { if (confirmDelete) { doDelete(confirmDelete); } }}
  onCancel={() => { confirmDelete = null; }}
/>
