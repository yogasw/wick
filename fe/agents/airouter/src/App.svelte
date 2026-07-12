<script lang="ts">
  import { ToastHost, Modal, CodeEditor } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { Effect } from "effect";
  import {
    fetchRouters,
    fetchStatus,
    install,
    start,
    stop,
    restart,
    setAutostart,
    setExternal,
    reqStreamURL,
    logStreamURL,
    dashboardURL,
    LOG_RESET,
  } from "$lib/api.js";
  import type { Status, ReqRow, RouterInfo } from "$lib/types.js";
  import { prettyJSON } from "$lib/json.js";
  import RequestRow from "./lib/RequestRow.svelte";

  // Run an api Effect against the real HTTP client layer. api.ts exposes
  // Effects (no layer provided) so tests can swap in a mock layer; the SPA
  // provides WickClientLayer here at the edge.
  const run = <T>(eff: Effect.Effect<T, unknown, never>): Promise<T> =>
    Effect.runPromise(eff.pipe(Effect.provide(WickClientLayer)) as Effect.Effect<T, unknown, never>);

  const app = document.getElementById("app");
  const base: string = (app?.dataset.base ?? "").replace(/\/$/, "");

  const DEFAULT_STATUS: Status = { installed: false, version: "", running: false, state: "stopped" };

  // ── router registry + per-router status ──
  let routers = $state<RouterInfo[]>([]);
  let statuses = $state<Record<string, Status>>({});
  let activeId = $state<string>("");
  let loaded = $state(false);
  // focused guards the one-time auto-focus of the running router on first load,
  // so a later manual switch is never overridden by a status refresh.
  let focused = $state(false);

  const active = $derived(routers.find((r) => r.id === activeId));
  const status = $derived<Status>(statuses[activeId] ?? DEFAULT_STATUS);
  const autostart = $derived(active?.autostart ?? false);
  const external = $derived(active?.external ?? false);

  type Tab = "dash" | "req" | "set";
  let tab = $state<Tab>("dash");

  let logs = $state("");
  let logSource = $state<EventSource | null>(null);
  let logEl: HTMLPreElement | undefined = $state();
  let busy = $state(false);

  // ── request stream (SSE) ──
  let rows = $state<ReqRow[]>([]);
  let follow = $state(true);
  let reqSource: EventSource | null = null;
  let streamState = $state<"off" | "connecting" | "live" | "error">("off");
  let nextId = 0;
  let listEl: HTMLDivElement | undefined = $state();

  // ── analysis modal ──
  let analysisOpen = $state(false);
  let analysisTitle = $state("");
  let analysisText = $state("");

  function openAnalysis(title: string, raw: string): void {
    analysisTitle = title;
    analysisText = prettyJSON(raw);
    analysisOpen = true;
  }

  function updateRouter(id: string, patch: Partial<RouterInfo>): void {
    routers = routers.map((r) => (r.id === id ? { ...r, ...patch } : r));
  }

  // ── load router list + status ──
  async function loadRouters(): Promise<void> {
    try {
      const res = await run(fetchRouters(base));
      routers = res.routers ?? [];
      if (routers.length && !routers.find((r) => r.id === activeId)) {
        activeId = routers[0].id; // provisional until statuses resolve
      }
      await refreshAll();
      // Auto-focus the running router on first open (the "active" one) so the
      // page lands on whatever is actually up rather than the first tile. Only
      // on initial load — a later manual switch is never overridden.
      if (!focused && routers.length) {
        const running = routers.find((r) => statuses[r.id]?.state === "running");
        if (running) activeId = running.id;
        focused = true;
      }
    } catch (e) {
      toastError("Load routers failed", String(e));
    } finally {
      loaded = true;
    }
  }

  async function refresh(): Promise<void> {
    const id = activeId;
    if (!id) return;
    try {
      const st = await run(fetchStatus(base, id));
      statuses = { ...statuses, [id]: st };
    } catch {
      /* transient — keep last known */
    }
  }

  async function refreshAll(): Promise<void> {
    for (const r of routers) {
      try {
        const st = await run(fetchStatus(base, r.id));
        statuses = { ...statuses, [r.id]: st };
      } catch {
        /* transient */
      }
    }
  }

  function switchTo(id: string): void {
    if (id === activeId) return;
    stopStream();
    stopLogStream();
    activeId = id;
    void refresh();
    if (tab === "req") startStream();
    if (tab === "set") startLogStream();
  }

  // ── log stream (SSE), scoped to the active router ──
  function startLogStream(): void {
    if (logSource || !activeId) return;
    logs = "";
    logSource = new EventSource(logStreamURL(base, activeId));
    logSource.onmessage = (ev) => {
      let chunk: string;
      try {
        chunk = JSON.parse(ev.data);
      } catch {
        return;
      }
      if (chunk === LOG_RESET) {
        logs = "";
        return;
      }
      logs += chunk;
      queueMicrotask(() => logEl?.scrollTo({ top: logEl.scrollHeight }));
    };
  }

  function stopLogStream(): void {
    if (logSource) {
      logSource.close();
      logSource = null;
    }
  }

  async function act(fn: () => Promise<unknown>, busyMsg: string, okMsg: string): Promise<void> {
    busy = true;
    try {
      await fn();
      toastOk("Done", okMsg);
    } catch (e) {
      toastError(busyMsg, String(e));
    } finally {
      busy = false;
      await refresh();
    }
  }

  async function onAutostart(e: Event): Promise<void> {
    const on = (e.target as HTMLInputElement).checked;
    const id = activeId;
    updateRouter(id, { autostart: on });
    try {
      await run(setAutostart(base, id, on));
      toastOk("Saved", on ? "Auto-start enabled." : "Auto-start disabled.");
    } catch (err) {
      updateRouter(id, { autostart: !on });
      toastError("Save failed", String(err));
    }
  }

  async function onExternal(e: Event): Promise<void> {
    const on = (e.target as HTMLInputElement).checked;
    const id = activeId;
    updateRouter(id, { external: on });
    try {
      await run(setExternal(base, id, on));
      toastOk(
        "Saved",
        on
          ? "External API access enabled — remote callers now need this router's API key."
          : "External API access disabled — the API is local-only.",
      );
    } catch (err) {
      updateRouter(id, { external: !on });
      toastError("Save failed", String(err));
    }
  }

  // ── request stream connect/disconnect ──
  function startStream(): void {
    if (reqSource || !activeId) return;
    streamState = "connecting";
    reqSource = new EventSource(reqStreamURL(base, activeId));
    reqSource.onopen = () => {
      streamState = "live";
    };
    reqSource.onmessage = (ev) => {
      let e: Omit<ReqRow, "id">;
      try {
        e = JSON.parse(ev.data);
      } catch {
        return;
      }
      rows = [...rows, { ...e, id: nextId++ }];
      if (follow) queueMicrotask(() => listEl?.scrollTo({ top: listEl.scrollHeight }));
    };
    reqSource.onerror = () => {
      streamState = "error";
    };
  }

  function stopStream(): void {
    if (reqSource) {
      reqSource.close();
      reqSource = null;
    }
    streamState = "off";
  }

  function clearRows(): void {
    rows = [];
  }

  function setTab(t: Tab): void {
    tab = t;
    if (t === "req") startStream();
    else stopStream();
    if (t === "set") startLogStream();
    else stopLogStream();
  }

  // ── initial load + status polling + stream teardown ──
  $effect(() => {
    void loadRouters();
    const s = setInterval(() => void refreshAll(), 5000);
    const onUnload = () => {
      stopStream();
      stopLogStream();
    };
    window.addEventListener("beforeunload", onUnload);
    return () => {
      clearInterval(s);
      window.removeEventListener("beforeunload", onUnload);
      stopStream();
      stopLogStream();
    };
  });

  function badgeFor(st: Status): { text: string; cls: string } {
    switch (st.state) {
      case "running":
        return { text: "Running", cls: "text-green-600 dark:text-green-300" };
      case "checking":
        return { text: "Checking…", cls: "text-amber-600 dark:text-amber-400" };
      case "starting":
        return { text: "Starting…", cls: "text-amber-600 dark:text-amber-400" };
      case "not-installed":
        return { text: "Not installed", cls: "text-black-800 dark:text-black-600" };
      default:
        return { text: "Stopped", cls: "text-black-800 dark:text-black-600" };
    }
  }

  function dotFor(st: Status | undefined): string {
    switch (st?.state) {
      case "running":
        return "bg-green-500";
      case "starting":
      case "checking":
        return "bg-amber-400";
      default:
        return "bg-black-400 dark:bg-navy-500";
    }
  }

  const badge = $derived(badgeFor(status));
  const frameSrc = $derived(status.state === "running" && activeId ? dashboardURL(activeId) : "");
</script>

<div class="flex h-full flex-col">
  <ToastHost />

  <!-- Header -->
  <div class="flex flex-col gap-3 px-4 pt-4 pb-3 shrink-0 sm:flex-row sm:items-start sm:justify-between sm:gap-4 sm:px-6 sm:pt-6 sm:pb-4">
    <div class="flex min-w-0 items-center gap-3">
      <div class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-green-700 dark:text-green-300" aria-hidden="true">
        <svg viewBox="0 0 16 16" class="h-6 w-6" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="4" cy="4" r="2"></circle>
          <circle cx="4" cy="12" r="2"></circle>
          <path d="M13 4v3a2 2 0 0 1-2 2H6M6 6l-2 2 2 2" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
      </div>
      <div class="min-w-0">
        <h1 class="text-lg font-semibold text-black-900 dark:text-white-100 sm:text-[1.375rem]">AI Router</h1>
        <p class="mt-0.5 hidden text-sm text-black-800 dark:text-black-600 sm:block">
          {active?.blurb ?? "Install, run, and switch between embedded AI-router dashboards — all proxied here, no extra exposed port."}
        </p>
      </div>
    </div>
    <div class="flex flex-shrink-0 flex-wrap items-center gap-2">
      <span class={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium bg-white-300 dark:bg-navy-600 ${badge.cls}`}>
        <span class="h-1.5 w-1.5 rounded-full bg-current"></span>
        {badge.text}{#if status.version}&nbsp;· v{status.version}{/if}
      </span>
      <div class="flex rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-0.5">
        {#each [["dash", "Dashboard"], ["req", "Requests"], ["set", "Settings"]] as [t, label] (t)}
          <button
            type="button"
            onclick={() => setTab(t as Tab)}
            class={`rounded-md px-3 py-1.5 text-[0.8125rem] font-medium transition-colors ${
              tab === t
                ? "bg-green-200 dark:bg-green-800 text-green-700 dark:text-green-300"
                : "text-black-800 dark:text-black-600"
            }`}
          >{label}</button>
        {/each}
      </div>
    </div>
  </div>

  <!-- Router switcher -->
  {#if routers.length > 1}
    <div class="flex flex-wrap items-center gap-2 px-4 pb-3 shrink-0 sm:px-6">
      {#each routers as r (r.id)}
        <button
          type="button"
          onclick={() => switchTo(r.id)}
          class={`inline-flex items-center gap-2 rounded-lg border px-3 py-1.5 text-[0.8125rem] font-medium transition-colors ${
            r.id === activeId
              ? "border-green-400 bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300"
              : "border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 text-black-800 dark:text-black-600 hover:border-black-700"
          }`}
        >
          <span class={`h-1.5 w-1.5 rounded-full ${dotFor(statuses[r.id])}`}></span>
          <svg viewBox="0 0 16 16" class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="1.5">{@html r.icon}</svg>
          {r.name}
        </button>
      {/each}
    </div>
  {/if}

  <!-- Dashboard -->
  {#if tab === "dash"}
    <div class="relative min-h-0 flex-1 bg-black">
      {#if frameSrc}
        <iframe src={frameSrc} class="h-full w-full border-0" title={`${active?.name ?? "Router"} Dashboard`}></iframe>
      {:else}
        <div class="absolute inset-0 flex flex-col items-center justify-center gap-3 text-black-700">
          <svg class="h-10 w-10 opacity-30" fill="none" stroke="currentColor" stroke-width="1.5" viewBox="0 0 24 24">
            <circle cx="6" cy="6" r="3"></circle>
            <circle cx="6" cy="18" r="3"></circle>
            <path d="M20 4v5a3 3 0 0 1-3 3H9M9 6h11"></path>
          </svg>
          <p class="text-sm opacity-50">
            {#if !loaded}Loading routers…
            {:else if routers.length === 0}No AI routers available. The feature may be disabled.
            {:else if status.state === "not-installed"}{active?.name ?? "This router"} is not installed. Open Settings to install it.
            {:else if status.state === "starting"}Starting {active?.name ?? "router"}…
            {:else}Stopped. Open Settings and click Start to launch the dashboard.{/if}
          </p>
        </div>
      {/if}
    </div>
  {/if}

  <!-- Requests -->
  {#if tab === "req"}
    <div bind:this={listEl} class="min-h-0 flex-1 overflow-y-auto">
      <div class="mx-auto w-full max-w-5xl px-6 py-6 space-y-4">
        <div class="flex items-center justify-between gap-3">
          <div>
            <div class="flex items-center gap-2">
              <h2 class="text-sm font-medium text-black-900 dark:text-white-100">API Requests</h2>
              <span class={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[0.6875rem] font-medium ${
                streamState === "live"
                  ? "bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300"
                  : streamState === "error"
                    ? "bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-400"
                    : "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600"
              }`}>
                <span class="h-1.5 w-1.5 rounded-full bg-current"></span>
                {streamState === "live" ? "listening" : streamState === "connecting" ? "connecting…" : streamState === "error" ? "reconnecting…" : "off"}
              </span>
            </div>
            <p class="mt-0.5 text-xs text-black-700 dark:text-black-600">
              Live stream of calls proxied through <span class="font-mono">/airouter/{activeId}/v1</span>, captured only while this tab is open. Full bodies are held in this browser and nothing is stored on the server — close the tab and they're gone.
            </p>
          </div>
          <div class="flex items-center gap-3">
            <label class="flex items-center gap-1.5 text-xs text-black-700 dark:text-black-600 cursor-pointer">
              <input type="checkbox" bind:checked={follow} class="h-3.5 w-3.5 rounded border-white-400 dark:border-navy-600 text-green-500" />
              Auto-scroll
            </label>
            <button type="button" onclick={clearRows} class="rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2.5 py-1 text-xs font-medium text-black-800 dark:text-black-600 hover:border-black-700">Clear view</button>
          </div>
        </div>

        {#if rows.length === 0}
          <div class="rounded-xl border border-dashed border-white-300 dark:border-navy-600 px-6 py-12 text-center text-sm text-black-700 dark:text-black-600">
            No requests yet. Calls to <span class="font-mono">/airouter/{activeId}/v1</span> will appear here.
          </div>
        {:else}
          <div class="space-y-2">
            {#each rows as row (row.id)}
              <RequestRow {row} onAnalyze={openAnalysis} />
            {/each}
          </div>
        {/if}
      </div>
    </div>
  {/if}

  <!-- Settings -->
  {#if tab === "set"}
    <div class="min-h-0 flex-1 overflow-y-auto">
      <div class="mx-auto w-full max-w-3xl px-6 py-6 pb-8 space-y-5">
        <!-- Controls -->
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <span class="text-sm font-medium text-black-900 dark:text-white-100">{active?.name ?? "Router"}{#if status.version}&nbsp;<span class="text-xs font-normal text-black-700">v{status.version}</span>{/if}</span>
            <div class="flex flex-wrap items-center gap-2">
              {#if status.state === "not-installed"}
                <button type="button" disabled={busy} onclick={() => act(() => run(install(base, activeId)), "Install failed", `${active?.name ?? "Router"} installed.`)} class="rounded-lg bg-green-500 px-4 py-2 text-[0.8125rem] font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">Install</button>
              {:else}
                {#if status.state === "stopped"}
                  <button type="button" disabled={busy} onclick={() => act(() => run(start(base, activeId)), "Start failed", `${active?.name ?? "Router"} started.`)} class="rounded-lg bg-green-500 px-4 py-2 text-[0.8125rem] font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">Start</button>
                {/if}
                {#if status.state === "running" || status.state === "starting"}
                  <button type="button" disabled={busy} onclick={() => act(() => run(stop(base, activeId)), "Stop failed", `${active?.name ?? "Router"} stopped.`)} class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-2 text-[0.8125rem] font-medium text-black-900 dark:text-white-100 hover:border-black-700 disabled:opacity-50">Stop</button>
                {/if}
                {#if status.state === "running"}
                  <button type="button" disabled={busy} onclick={() => act(() => run(restart(base, activeId)), "Restart failed", `${active?.name ?? "Router"} restarted.`)} class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-2 text-[0.8125rem] font-medium text-black-900 dark:text-white-100 hover:border-black-700 disabled:opacity-50">Restart</button>
                {/if}
                <button type="button" disabled={busy} onclick={() => act(() => run(install(base, activeId)), "Update failed", `${active?.name ?? "Router"} updated.`)} class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-2 text-[0.8125rem] font-medium text-black-900 dark:text-white-100 hover:border-black-700 disabled:opacity-50">Update</button>
              {/if}
            </div>
          </div>
        </div>

        <!-- Auto-start -->
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5">
          <label class="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" checked={autostart} onchange={onAutostart} class="mt-0.5 h-4 w-4 rounded border-white-400 dark:border-navy-600 text-green-500" />
            <span>
              <span class="block text-sm font-medium text-black-900 dark:text-white-100">Auto-start on boot</span>
              <span class="block mt-0.5 text-xs text-black-700 dark:text-black-600">When enabled, wick launches {active?.name ?? "this router"} automatically each time the server starts.</span>
            </span>
          </label>
        </div>

        <!-- External API access -->
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5">
          <label class="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" checked={external} onchange={onExternal} class="mt-0.5 h-4 w-4 rounded border-white-400 dark:border-navy-600 text-green-500" />
            <span>
              <span class="block text-sm font-medium text-black-900 dark:text-white-100">Allow external API access</span>
              <span class="block mt-0.5 text-xs text-black-700 dark:text-black-600">Off (default): the <span class="font-mono">/airouter/{activeId}/v1</span> API answers only local spawns on this machine; off-machine callers (tunnel / public URL) get 403. On: remote callers reach {active?.name ?? "the router"} with their real address, so it enforces its own API key — local spawns still need no key.</span>
            </span>
          </label>
        </div>

        <!-- Logs (live SSE tail) -->
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 overflow-hidden">
          <div class="flex items-center gap-2 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 px-4 py-2.5">
            <span class="text-sm font-medium text-black-900 dark:text-white-100">Logs</span>
            <span class={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[0.6875rem] font-medium ${
              logSource ? "bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300" : "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600"
            }`}>
              <span class="h-1.5 w-1.5 rounded-full bg-current"></span>
              {logSource ? "tailing" : "off"}
            </span>
          </div>
          <pre bind:this={logEl} class="m-0 max-h-80 min-h-40 overflow-auto bg-black px-4 py-3 text-[0.75rem] leading-relaxed text-green-300 whitespace-pre-wrap break-words select-text">{logs || "(no output yet)"}</pre>
        </div>
      </div>
    </div>
  {/if}
</div>

<!-- Fullscreen analysis: pretty-printed JSON in the shared code editor -->
<Modal open={analysisOpen} title={analysisTitle} size="xl" onClose={() => (analysisOpen = false)}>
  <div class="h-[70vh]">
    <CodeEditor value={analysisText} onChange={() => {}} language="json" readonly={true} autofocus={true} />
  </div>
</Modal>
