<script lang="ts">
  import { ToastHost, Modal, CodeEditor } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { WickClientLayer } from "@wick-fe/common-api";
  import { Effect } from "effect";
  import {
    fetchStatus,
    install,
    start,
    stop,
    restart,
    setAutostart,
    setExternal,
    reqStreamURL,
    logStreamURL,
    LOG_RESET,
  } from "$lib/api.js";
  import type { Status, ReqRow } from "$lib/types.js";
  import { prettyJSON } from "$lib/json.js";
  import RequestRow from "./lib/RequestRow.svelte";

  // Run an api Effect against the real HTTP client layer. api.ts exposes
  // Effects (no layer provided) so tests can swap in a mock layer; the SPA
  // provides WickClientLayer here at the edge.
  const run = <T>(eff: Effect.Effect<T, unknown, never>): Promise<T> =>
    Effect.runPromise(eff.pipe(Effect.provide(WickClientLayer)) as Effect.Effect<T, unknown, never>);

  const app = document.getElementById("app");
  const base: string = (app?.dataset.base ?? "").replace(/\/$/, "");
  const initialAutostart = app?.dataset.autostart === "true";
  const initialExternal = app?.dataset.external === "true";

  type Tab = "dash" | "req" | "set";
  let tab = $state<Tab>("dash");

  let status = $state<Status>({ installed: false, version: "", running: false, state: "stopped" });
  let logs = $state("");
  let logSource = $state<EventSource | null>(null);
  let logEl: HTMLPreElement | undefined = $state();
  let autostart = $state(initialAutostart);
  let external = $state(initialExternal);
  let busy = $state(false);

  // ── request stream (SSE) ──
  // Bodies are captured server-side ONLY while this tab is connected. The
  // full request/response bodies live only here; closing the tab drops the
  // subscriber and the server stops capturing.
  let rows = $state<ReqRow[]>([]);
  let follow = $state(true);
  let reqSource: EventSource | null = null;
  let streamState = $state<"off" | "connecting" | "live" | "error">("off");
  let nextId = 0;
  let listEl: HTMLDivElement | undefined = $state();

  // ── analysis modal (fullscreen JSON) ──
  let analysisOpen = $state(false);
  let analysisTitle = $state("");
  let analysisText = $state("");

  function openAnalysis(title: string, raw: string): void {
    analysisTitle = title;
    analysisText = prettyJSON(raw);
    analysisOpen = true;
  }

  // ── status / lifecycle ──
  async function refresh(): Promise<void> {
    try {
      status = await run(fetchStatus(base));
    } catch (e) {
      toastError("Status error", String(e));
    }
  }

  // ── log stream (SSE) ──
  // The Settings tab tails the 9router process output live: a snapshot on
  // connect, then incremental chunks. A reset sentinel (on process restart)
  // clears the accumulated view.
  function startLogStream(): void {
    if (logSource) return;
    logs = "";
    logSource = new EventSource(logStreamURL(base));
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
    autostart = on;
    try {
      await run(setAutostart(base, on));
      toastOk("Saved", on ? "Auto-start enabled." : "Auto-start disabled.");
    } catch (err) {
      autostart = !on;
      toastError("Save failed", String(err));
    }
  }

  async function onExternal(e: Event): Promise<void> {
    const on = (e.target as HTMLInputElement).checked;
    external = on;
    try {
      await run(setExternal(base, on));
      toastOk(
        "Saved",
        on
          ? "External API access enabled — remote callers now need a 9router API key."
          : "External API access disabled — the API is local-only.",
      );
    } catch (err) {
      external = !on;
      toastError("Save failed", String(err));
    }
  }

  // ── request stream connect/disconnect ──
  function startStream(): void {
    if (reqSource) return;
    streamState = "connecting";
    reqSource = new EventSource(reqStreamURL(base));
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
      // EventSource auto-reconnects; reflect the transient failure so the
      // badge doesn't falsely claim "live" while the connection is down.
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
    // Connect each tab's live stream only while it's visible; disconnect the
    // others so the server drops those subscribers.
    if (t === "req") startStream();
    else stopStream();
    if (t === "set") startLogStream();
    else stopLogStream();
  }

  // ── status polling (always) + stream teardown on unload ──
  $effect(() => {
    void refresh();
    const s = setInterval(() => void refresh(), 5000);
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

  const badge = $derived.by(() => {
    switch (status.state) {
      case "running":
        return { text: "Running", cls: "text-green-600 dark:text-green-300" };
      case "starting":
        return { text: "Starting…", cls: "text-amber-600 dark:text-amber-400" };
      case "not-installed":
        return { text: "Not installed", cls: "text-black-800 dark:text-black-600" };
      default:
        return { text: "Stopped", cls: "text-black-800 dark:text-black-600" };
    }
  });

  const frameSrc = $derived(status.state === "running" ? "/9router/" : "");
</script>

<div class="flex h-full flex-col">
  <ToastHost />

  <!-- Header -->
  <div class="flex items-start justify-between gap-4 px-6 pt-6 pb-4 shrink-0">
    <div class="flex items-center gap-3">
      <div class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-green-700 dark:text-green-300" aria-hidden="true">
        <svg viewBox="0 0 16 16" class="h-6 w-6" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="4" cy="4" r="2"></circle>
          <circle cx="4" cy="12" r="2"></circle>
          <path d="M13 4v3a2 2 0 0 1-2 2H6M6 6l-2 2 2 2" stroke-linecap="round" stroke-linejoin="round"></path>
        </svg>
      </div>
      <div>
        <h1 class="text-[1.375rem] font-semibold text-black-900 dark:text-white-100">9router</h1>
        <p class="mt-0.5 text-sm text-black-800 dark:text-black-600">Install, run, and manage the 9router dashboard — embedded here, no extra exposed port.</p>
      </div>
    </div>
    <div class="flex flex-shrink-0 items-center gap-2">
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

  <!-- Dashboard -->
  {#if tab === "dash"}
    <div class="relative min-h-0 flex-1 bg-black">
      {#if frameSrc}
        <iframe src={frameSrc} class="h-full w-full border-0" title="9router Dashboard"></iframe>
      {:else}
        <div class="absolute inset-0 flex flex-col items-center justify-center gap-3 text-black-700">
          <svg class="h-10 w-10 opacity-30" fill="none" stroke="currentColor" stroke-width="1.5" viewBox="0 0 24 24">
            <circle cx="6" cy="6" r="3"></circle>
            <circle cx="6" cy="18" r="3"></circle>
            <path d="M20 4v5a3 3 0 0 1-3 3H9M9 6h11"></path>
          </svg>
          <p class="text-sm opacity-50">
            {#if status.state === "not-installed"}9router is not installed. Open Settings to install it.
            {:else if status.state === "starting"}Starting 9router…
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
              Live stream of calls proxied through <span class="font-mono">/9router/v1</span>, captured only while this tab is open. Full bodies are held in this browser and nothing is stored on the server — close the tab and they're gone.
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
            No requests yet. Calls to <span class="font-mono">/9router/v1</span> will appear here.
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
            <span class="text-sm font-medium text-black-900 dark:text-white-100">Package{#if status.version}&nbsp;<span class="text-xs font-normal text-black-700">v{status.version}</span>{/if}</span>
            <div class="flex flex-wrap items-center gap-2">
              {#if status.state === "not-installed"}
                <button type="button" disabled={busy} onclick={() => act(() => run(install(base)), "Install failed", "9router installed.")} class="rounded-lg bg-green-500 px-4 py-2 text-[0.8125rem] font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">Install</button>
              {:else}
                {#if status.state === "stopped"}
                  <button type="button" disabled={busy} onclick={() => act(() => run(start(base)), "Start failed", "9router started.")} class="rounded-lg bg-green-500 px-4 py-2 text-[0.8125rem] font-medium text-white-100 hover:bg-green-600 disabled:opacity-50">Start</button>
                {/if}
                {#if status.state === "running" || status.state === "starting"}
                  <button type="button" disabled={busy} onclick={() => act(() => run(stop(base)), "Stop failed", "9router stopped.")} class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-2 text-[0.8125rem] font-medium text-black-900 dark:text-white-100 hover:border-black-700 disabled:opacity-50">Stop</button>
                {/if}
                {#if status.state === "running"}
                  <button type="button" disabled={busy} onclick={() => act(() => run(restart(base)), "Restart failed", "9router restarted.")} class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-2 text-[0.8125rem] font-medium text-black-900 dark:text-white-100 hover:border-black-700 disabled:opacity-50">Restart</button>
                {/if}
                <button type="button" disabled={busy} onclick={() => act(() => run(install(base)), "Update failed", "9router updated.")} class="rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-4 py-2 text-[0.8125rem] font-medium text-black-900 dark:text-white-100 hover:border-black-700 disabled:opacity-50">Update</button>
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
              <span class="block mt-0.5 text-xs text-black-700 dark:text-black-600">When enabled, wick launches 9router automatically each time the server starts.</span>
            </span>
          </label>
        </div>

        <!-- External API access -->
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5">
          <label class="flex items-start gap-3 cursor-pointer">
            <input type="checkbox" checked={external} onchange={onExternal} class="mt-0.5 h-4 w-4 rounded border-white-400 dark:border-navy-600 text-green-500" />
            <span>
              <span class="block text-sm font-medium text-black-900 dark:text-white-100">Allow external API access</span>
              <span class="block mt-0.5 text-xs text-black-700 dark:text-black-600">Off (default): the <span class="font-mono">/9router/v1</span> API answers only local spawns on this machine; off-machine callers (tunnel / public URL) get 403. On: remote callers reach 9router with their real address, so 9router enforces its own API key — local spawns still need no key.</span>
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
