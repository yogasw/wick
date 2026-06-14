<script lang="ts">
  import { onMount } from "svelte";
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
    apiProbeGate,
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
  let spawnOpen = $state(false);

  let pollInterval: ReturnType<typeof setInterval> | null = null;

  async function load(silent = false) {
    if (!silent) { loading = true; error = null; }
    try {
      data = await apiGetProviders();
    } catch (e) {
      if (!silent) error = e instanceof Error ? e.message : "Failed to load providers";
    } finally {
      if (!silent) loading = false;
    }
  }

  function setBusy(key: string, val: boolean) {
    busy = { ...busy, [key]: val };
  }

  async function doRescanAll() {
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

  async function doRescanOne(p: ProviderStatusDTO) {
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

  async function doDelete(p: ProviderStatusDTO) {
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

  async function doGateToggle() {
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

  async function saveGateModes(mode: string) {
    setBusy("gate-mode", true);
    try {
      await apiGateModes({ [mode]: true });
      toastOk("Permission mode saved");
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Mode update failed");
    } finally {
      setBusy("gate-mode", false);
    }
  }

  async function doAutoRescanToggle() {
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

  async function doMCPInstall(clientID: string) {
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

  async function doMCPUninstall(clientID: string) {
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

  async function doProbeGate(p: ProviderStatusDTO) {
    const key = `probe-${p.Instance.Type}-${p.Instance.Name}`;
    setBusy(key, true);
    try {
      await apiProbeGate(p.Instance.Type, p.Instance.Name);
      toastOk(`Probe triggered for ${p.Instance.Name}`);
      await load(true);
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Probe failed");
    } finally {
      setBusy(key, false);
    }
  }

  function isBuiltin(p: ProviderStatusDTO): boolean {
    return p.Instance.Name === p.Instance.Type || p.Instance.Name === "default";
  }

  function statusBadge(p: ProviderStatusDTO): { label: string; cls: string } {
    if (p.Instance.Disabled) return { label: "disabled", cls: "bg-black-600/20 text-black-600 dark:bg-navy-600 dark:text-black-500" };
    if (p.Probing) return { label: "probing…", cls: "bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-400" };
    if (!p.PathFound) return { label: "not found", cls: "bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-400" };
    if (p.VersionErr) return { label: "error", cls: "bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-400" };
    return { label: "ready", cls: "bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-400" };
  }

  onMount(() => {
    load();
    pollInterval = setInterval(() => load(true), 4000);
    return () => {
      if (pollInterval !== null) clearInterval(pollInterval);
    };
  });
</script>

<div class="space-y-6">
  <!-- header -->
  <div class="flex items-center justify-between gap-3 flex-wrap">
    <div>
      <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">Providers</h1>
      <p class="text-xs text-black-700 dark:text-black-600 mt-0.5">AI provider instances — detect status, gate control, MCP clients.</p>
    </div>
    <button
      onclick={doRescanAll}
      disabled={busy["rescan-all"]}
      class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
    >{busy["rescan-all"] ? "Rescanning…" : "Rescan All"}</button>
  </div>

  {#if loading}
    <div class="text-sm text-black-600 dark:text-black-500">Loading…</div>
  {:else if error}
    <div class="rounded-lg border border-red-300 dark:border-red-700 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
  {:else if data}

    <!-- pool stats -->
    <div class="grid grid-cols-2 sm:grid-cols-4 gap-3">
      {#each [
        { label: "Active", value: data.PoolActive },
        { label: "Queued", value: data.PoolQueueLen },
        { label: "Max", value: data.PoolMax },
        { label: "Live", value: data.LiveProcesses.length },
      ] as stat}
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-3 text-center">
          <div class="text-xl font-bold text-black-900 dark:text-white-100">{stat.value}</div>
          <div class="text-xs text-black-600 dark:text-black-500 mt-0.5">{stat.label}</div>
        </div>
      {/each}
    </div>

    <!-- gate panel -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm p-5 space-y-3">
      <div class="flex items-center justify-between gap-3 flex-wrap">
        <div>
          <div class="text-sm font-semibold text-black-900 dark:text-white-100">Permission Gate</div>
          {#if data.Gate.Source}
            <div class="text-xs text-black-600 dark:text-black-500 mt-0.5">Source: {data.Gate.Source}</div>
          {/if}
          {#if data.Gate.Note}
            <div class="text-xs text-black-600 dark:text-black-500">{data.Gate.Note}</div>
          {/if}
        </div>
        <button
          onclick={doGateToggle}
          disabled={busy["gate"] || data.Gate.BypassLocked}
          class={[
            "rounded-lg px-4 py-2 text-sm font-medium transition-colors disabled:opacity-50",
            data.Gate.Enabled
              ? "bg-green-500 text-white-100 hover:bg-green-600"
              : "bg-black-600 text-white-100 hover:bg-black-700 dark:bg-navy-600 dark:hover:bg-navy-500",
          ].join(" ")}
        >{busy["gate"] ? "…" : data.Gate.Enabled ? "Enabled" : "Disabled"}</button>
      </div>
      {#if data.Gate.PermissionMode}
        <div class="flex items-center gap-2 flex-wrap">
          <span class="text-xs text-black-700 dark:text-black-600">Mode:</span>
          {#each ["bypassPermissions", "default", "acceptEdits"] as mode}
            <button
              onclick={() => saveGateModes(mode)}
              disabled={busy["gate-mode"]}
              class={[
                "rounded-full px-3 py-0.5 text-xs font-medium border transition-colors disabled:opacity-50",
                data.Gate.PermissionMode === mode
                  ? "bg-green-500 border-green-500 text-white-100"
                  : "border-white-400 dark:border-navy-600 text-black-700 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800",
              ].join(" ")}
            >{mode}</button>
          {/each}
        </div>
      {/if}
    </div>

    <!-- provider cards -->
    {#if data.Providers.length === 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-6 py-12 text-center text-sm text-black-700 dark:text-black-600">
        No providers detected. Run Rescan All to discover installed AI providers.
      </div>
    {:else}
      <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
        {#each data.Providers as p}
          {@const badge = statusBadge(p)}
          {@const rescanKey = `rescan-${p.Instance.Type}-${p.Instance.Name}`}
          {@const delKey = `del-${p.Instance.Type}-${p.Instance.Name}`}
          {@const probeKey = `probe-${p.Instance.Type}-${p.Instance.Name}`}
          <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm p-4 space-y-3">
            <!-- card header -->
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <div class="flex items-center gap-2 flex-wrap">
                  <span class="font-mono text-sm font-semibold text-black-900 dark:text-white-100 truncate">{p.Instance.Name}</span>
                  <span class="text-xs text-black-600 dark:text-black-500">{p.Instance.Type}</span>
                </div>
                {#if !p.Cap.Unlimited}
                  <div class="text-xs text-black-600 dark:text-black-500 mt-0.5">{p.Cap.Used}/{p.Cap.Max} slots</div>
                {:else}
                  <div class="text-xs text-black-600 dark:text-black-500 mt-0.5">∞ slots</div>
                {/if}
              </div>
              <span class={`inline-flex shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${badge.cls}`}>{badge.label}</span>
            </div>

            <!-- path / version -->
            <div class="space-y-1">
              {#if p.Path}
                <div class="font-mono text-xs text-black-700 dark:text-black-600 truncate" title={p.Path}>{p.Path}</div>
              {/if}
              {#if p.Version}
                <div class="text-xs text-black-600 dark:text-black-500">{p.Version}</div>
              {/if}
              {#if p.VersionErr}
                <div class="text-xs text-red-600 dark:text-red-400">{p.VersionErr}</div>
              {/if}
            </div>

            <!-- actions -->
            <div class="flex items-center gap-2 flex-wrap pt-1 border-t border-white-300 dark:border-navy-600">
              <button
                onclick={() => onNavigate(p.Instance.Type, p.Instance.Name)}
                class="rounded px-2.5 py-1 text-xs font-medium border border-white-400 dark:border-navy-600 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
              >Configure</button>
              <button
                onclick={() => doRescanOne(p)}
                disabled={busy[rescanKey]}
                class="rounded px-2.5 py-1 text-xs font-medium border border-white-400 dark:border-navy-600 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors disabled:opacity-50"
              >{busy[rescanKey] ? "…" : "Rescan"}</button>
              <button
                onclick={() => doProbeGate(p)}
                disabled={busy[probeKey]}
                class="rounded px-2.5 py-1 text-xs font-medium border border-white-400 dark:border-navy-600 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors disabled:opacity-50"
              >{busy[probeKey] ? "…" : "Probe"}</button>
              {#if !isBuiltin(p)}
                <button
                  onclick={() => { confirmDelete = p; }}
                  disabled={busy[delKey]}
                  class="rounded px-2.5 py-1 text-xs font-medium text-red-600 dark:text-red-400 hover:underline disabled:opacity-50 ml-auto"
                >Delete</button>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}

    <!-- auto-rescan toggle -->
    <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-5 py-4 flex items-center justify-between gap-3">
      <div>
        <div class="text-sm font-medium text-black-900 dark:text-white-100">Auto Rescan</div>
        <div class="text-xs text-black-600 dark:text-black-500 mt-0.5">Automatically re-detect provider binaries on changes.</div>
      </div>
      <button
        onclick={doAutoRescanToggle}
        disabled={busy["auto-rescan"]}
        class={[
          "rounded-lg px-4 py-2 text-sm font-medium transition-colors disabled:opacity-50",
          data.AutoRescan
            ? "bg-green-500 text-white-100 hover:bg-green-600"
            : "bg-black-600 text-white-100 hover:bg-black-700 dark:bg-navy-600 dark:hover:bg-navy-500",
        ].join(" ")}
      >{busy["auto-rescan"] ? "…" : data.AutoRescan ? "On" : "Off"}</button>
    </div>

    <!-- MCP clients -->
    {#if data.MCPClients.Clients.length > 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <button
          class="w-full flex items-center justify-between px-5 py-4 text-left"
          onclick={() => { mcpOpen = !mcpOpen; }}
        >
          <div>
            <span class="text-sm font-semibold text-black-900 dark:text-white-100">MCP Clients</span>
            {#if data.MCPClients.AppName}
              <span class="ml-2 text-xs text-black-600 dark:text-black-500">({data.MCPClients.AppName})</span>
            {/if}
          </div>
          <svg class={`w-4 h-4 text-black-600 dark:text-black-500 transition-transform ${mcpOpen ? "rotate-180" : ""}`} fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7"/>
          </svg>
        </button>
        {#if mcpOpen}
          <div class="border-t border-white-300 dark:border-navy-600 divide-y divide-white-300 dark:divide-navy-600">
            {#each data.MCPClients.Clients as client}
              {@const mcpKey = `mcp-${client.ID}`}
              <div class="flex items-center justify-between gap-3 px-5 py-3">
                <div class="min-w-0">
                  <div class="text-sm font-medium text-black-900 dark:text-white-100 truncate">{client.Label || client.ID}</div>
                  <div class="flex items-center gap-2 mt-0.5">
                    {#if client.Installed}
                      <span class="text-xs text-green-600 dark:text-green-400">installed</span>
                    {:else}
                      <span class="text-xs text-black-600 dark:text-black-500">not installed</span>
                    {/if}
                    {#if client.Detected}
                      <span class="text-xs text-green-600 dark:text-green-400">detected</span>
                    {/if}
                    {#if client.Blocklisted}
                      <span class="text-xs text-red-600 dark:text-red-400">blocklisted</span>
                    {/if}
                  </div>
                </div>
                <div class="flex items-center gap-2 shrink-0">
                  {#if client.Installed}
                    <button
                      onclick={() => doMCPUninstall(client.ID)}
                      disabled={busy[mcpKey]}
                      class="rounded px-2.5 py-1 text-xs font-medium border border-white-400 dark:border-navy-600 text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-800 disabled:opacity-50"
                    >{busy[mcpKey] ? "…" : "Uninstall"}</button>
                  {:else}
                    <button
                      onclick={() => doMCPInstall(client.ID)}
                      disabled={busy[mcpKey] || client.Blocklisted}
                      class="rounded px-2.5 py-1 text-xs font-medium bg-green-500 text-white-100 hover:bg-green-600 disabled:opacity-50"
                    >{busy[mcpKey] ? "…" : "Install"}</button>
                  {/if}
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}

    <!-- spawn log -->
    {#if data.Spawns.length > 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <button
          class="w-full flex items-center justify-between px-5 py-4 text-left"
          onclick={() => { spawnOpen = !spawnOpen; }}
        >
          <span class="text-sm font-semibold text-black-900 dark:text-white-100">Spawn Log ({data.Spawns.length})</span>
          <svg class={`w-4 h-4 text-black-600 dark:text-black-500 transition-transform ${spawnOpen ? "rotate-180" : ""}`} fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7"/>
          </svg>
        </button>
        {#if spawnOpen}
          <div class="border-t border-white-300 dark:border-navy-600 divide-y divide-white-300 dark:divide-navy-600">
            {#each data.Spawns as spawn}
              <a
                href={`${base}/providers/spawns/${encodeURIComponent(spawn.Path.split("/").pop() ?? spawn.Path)}`}
                class="flex items-center justify-between gap-3 px-5 py-3 hover:bg-white-200 dark:hover:bg-navy-800 transition-colors"
              >
                <div class="min-w-0">
                  <div class="text-sm font-medium text-black-900 dark:text-white-100 truncate">{spawn.SessionID || spawn.Path.split("/").pop()}</div>
                  <div class="text-xs text-black-600 dark:text-black-500 mt-0.5 truncate">
                    {spawn.ProviderType}/{spawn.ProviderName}
                    {#if spawn.StartedAt} · {spawn.StartedAt}{/if}
                    {#if spawn.ExitReason} · {spawn.ExitReason}{/if}
                  </div>
                  {#if spawn.FirstUserMessage}
                    <div class="text-xs text-black-600 dark:text-black-500 truncate mt-0.5 italic">"{spawn.FirstUserMessage}"</div>
                  {/if}
                </div>
                <svg class="w-4 h-4 text-black-500 dark:text-black-600 shrink-0" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" d="M9 5l7 7-7 7"/>
                </svg>
              </a>
            {/each}
          </div>
        {/if}
      </div>
    {/if}

  {/if}
</div>

<ConfirmDialog
  open={confirmDelete !== null}
  title={`Delete ${confirmDelete?.Instance.Name ?? ""}?`}
  body="This will remove the provider instance. Built-in providers cannot be deleted."
  confirmLabel="Delete"
  destructive={true}
  onConfirm={() => { if (confirmDelete) doDelete(confirmDelete); }}
  onCancel={() => { confirmDelete = null; }}
/>
