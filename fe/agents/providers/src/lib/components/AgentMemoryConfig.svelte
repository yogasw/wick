<script lang="ts">
  import { toastError, toastOk } from "@wick-fe/common-stores";
  import { Toggle } from "@wick-fe/common-ui";
  import {
    apiAgentMemoryStart,
    apiAgentMemoryTest,
    type AgentMemoryProbe,
  } from "$lib/api.js";
  import type { AgentMemoryChoiceDTO } from "$lib/types.js";

  type Props = {
    base: string;
    type: string;
    // Only the provider types the selected backend can actually wire into a
    // spawn; the BE measures this, the parent gates visibility on it.
    supported: boolean;
    // featureEnabled is the server-wide master switch. Off means no daemon is
    // managed and no project marker is written, so the controls are inert.
    featureEnabled: boolean;
    useAgentMemory: boolean;
    // provider is the selected backend id (bindable).
    provider: string;
    backends: AgentMemoryChoiceDTO[];
    // serverUrl is the per-instance override (bindable). Empty = the daemon
    // wick manages, whose resolved address is effectiveUrl.
    serverUrl: string;
    effectiveUrl?: string;
    authKey: string;
    // authKeyMasked: a token is already stored — the placeholder says that
    // leaving the field blank keeps it.
    authKeyMasked?: boolean;
    capture: boolean;
    // captureSupported: would turning capture on change the spawn at all?
    // False means the switch would do nothing, and captureNote says why.
    captureSupported?: boolean;
    captureNote?: string;
    // What recording COSTS on this provider type when it is wired. Opposite
    // of captureNote: that one explains a switch that would do nothing.
    captureCaveat?: string;
    // configPreview is the effective wiring wick injects at spawn, rendered
    // by the BE (admin-only; a viewer gets an empty string).
    configPreview?: string;
  };
  let {
    base,
    type,
    supported,
    featureEnabled,
    useAgentMemory = $bindable(),
    provider = $bindable(),
    backends,
    serverUrl = $bindable(),
    effectiveUrl = "",
    authKey = $bindable(),
    authKeyMasked = false,
    capture = $bindable(),
    captureSupported = false,
    captureNote = "",
    captureCaveat = "",
    configPreview = "",
  }: Props = $props();

  let showAdvanced = $state(false);
  let probing = $state(false);
  let starting = $state(false);
  // probe holds the LAST preflight result. null = never run, which is why the
  // banner only appears once something was actually checked.
  let probe = $state<AgentMemoryProbe | null>(null);

  const selected = $derived(backends.find((b) => b.ID === provider) ?? backends[0]);
  const backendName = $derived(selected?.Name ?? "the memory backend");
  // A custom server URL points somewhere wick does not manage, so the local
  // daemon probe says nothing about it — see runPreflight.
  const usesManagedDaemon = $derived(serverUrl.trim() === "");

  // Default the backend to the first registered one so every save carries a
  // concrete id instead of relying on the BE's fallback.
  $effect(() => {
    if (provider === "" && backends.length > 0) provider = backends[0].ID;
  });

  // Preflight (PLAN §18.4). Turning the switch on saves a setting that only
  // does something if a daemon answers, so the daemon is checked FIRST and
  // the switch stays off when it doesn't — silently storing a toggle that
  // cannot work is the failure mode this exists to prevent.
  //
  // It only applies to the managed daemon. With a custom server URL the
  // /test endpoint would probe wick's own daemon and report on the wrong
  // machine, so the check is skipped and said out loud rather than faked.
  async function onToggle(next: boolean): Promise<void> {
    if (!next) {
      useAgentMemory = false;
      return;
    }
    if (!usesManagedDaemon) {
      probe = null;
      useAgentMemory = true;
      return;
    }
    probing = true;
    try {
      probe = await apiAgentMemoryTest(base, provider);
      useAgentMemory = probe.ok;
    } finally {
      probing = false;
    }
  }

  // enableAnyway is the escape hatch for a daemon the operator will start
  // later (or one wick cannot see). The warning stays on screen — this
  // records the choice, it does not pretend the check passed.
  function enableAnyway(): void {
    useAgentMemory = true;
  }

  async function startDaemon(): Promise<void> {
    starting = true;
    try {
      await apiAgentMemoryStart(base, provider);
      probe = await apiAgentMemoryTest(base, provider);
      if (probe.ok) {
        toastOk(`${backendName} started`);
        useAgentMemory = true;
      } else {
        toastError(probe.error || "Daemon started but is not answering");
      }
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Start failed");
    } finally {
      starting = false;
    }
  }
</script>

{#if supported}
  <div class="rounded-lg border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4 space-y-3">
    <div class="flex items-center justify-between gap-3">
      <div>
        <p class="text-sm font-medium text-black-900 dark:text-white-100">Use agent memory</p>
        <p id="agentmemory-intent" class="text-[11px] text-black-700 dark:text-black-600">
          Give this provider's spawns an MCP tool for what earlier sessions learned, so context survives a session ending.
        </p>
      </div>
      <Toggle
        id="agentmemory-toggle"
        checked={useAgentMemory}
        onChange={(v) => void onToggle(v)}
        label="Use agent memory"
        describedBy="agentmemory-intent"
        disabled={!featureEnabled || probing}
      />
    </div>

    {#if !featureEnabled}
      <p class="rounded-lg border border-cau-400/30 bg-cau-400/5 px-3 py-2 text-[11px] text-cau-400">
        Agent Memory is switched off for this server, so no daemon is managed and no project marker is written. An admin turns it on in the agents tool settings; until then this control would save a setting that never runs.
      </p>
    {/if}

    {#if probing}
      <p class="text-[11px] text-black-700 dark:text-black-600">Checking {backendName}…</p>
    {:else if probe && !probe.ok}
      <div class="rounded-lg border border-rose-400/30 bg-rose-400/5 px-3 py-2 space-y-2">
        <p class="text-[11px] text-rose-600 dark:text-rose-400">
          {backendName} did not answer{probe.baseUrl ? ` at ${probe.baseUrl}` : ""}, so the switch was left off — turning it on would wire every spawn to a server that isn't there.{probe.error ? ` (${probe.error})` : ""}
        </p>
        <div class="flex items-center gap-2">
          <button
            type="button"
            onclick={startDaemon}
            disabled={starting}
            class="rounded-lg bg-green-500 px-3 py-1 text-[11px] font-medium text-white-100 hover:bg-green-600 disabled:opacity-50"
          >{starting ? "Starting…" : "Start daemon"}</button>
          <button
            type="button"
            onclick={enableAnyway}
            class="rounded-lg border border-white-400 dark:border-navy-600 px-3 py-1 text-[11px] font-medium text-black-800 dark:text-black-600 hover:bg-white-300 dark:hover:bg-navy-700"
          >Turn it on anyway</button>
        </div>
      </div>
    {:else if probe && probe.ok}
      <div class="flex items-center gap-1.5 rounded-lg border border-green-400/30 bg-green-400/5 px-3 py-2 text-[11px] text-green-600 dark:text-green-400">
        <span class="inline-block h-1.5 w-1.5 rounded-full bg-green-500"></span>
        {backendName} answered{probe.version ? ` (v${probe.version})` : ""} at {probe.baseUrl}.
      </div>
    {/if}

    {#if useAgentMemory}
      {#if backends.length > 0}
        <div>
          <div class="mb-1 flex items-center justify-between gap-2">
            <label for="agentmemory-backend" class="block text-xs font-medium text-black-800 dark:text-black-600">Backend</label>
            {#if selected?.GitHubURL}
              <a
                href={selected.GitHubURL}
                target="_blank"
                rel="noreferrer noopener"
                class="inline-flex items-center gap-1 text-[11px] font-medium text-link-400 hover:underline"
              >
                <svg viewBox="0 0 16 16" class="h-3 w-3" fill="currentColor" aria-hidden="true">
                  <path d="M8 0C3.58 0 0 3.58 0 8a8 8 0 0 0 5.47 7.59c.4.07.55-.17.55-.38v-1.33c-2.23.48-2.7-1.07-2.7-1.07-.36-.93-.89-1.18-.89-1.18-.73-.5.05-.49.05-.49.81.06 1.23.83 1.23.83.72 1.23 1.89.87 2.35.67.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.6 7.6 0 0 1 4 0c1.53-1.03 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.28.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48v2.2c0 .21.15.46.55.38A8 8 0 0 0 16 8c0-4.42-3.58-8-8-8Z"></path>
                </svg>
                {selected.Name} on GitHub
              </a>
            {/if}
          </div>
          <select
            id="agentmemory-backend"
            bind:value={provider}
            class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100"
          >
            {#each backends as b (b.ID)}
              <option value={b.ID}>{b.Name}</option>
            {/each}
          </select>
          {#if selected?.Blurb}
            <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">{selected.Blurb}</p>
          {/if}
        </div>
      {/if}

      <div>
        <label for="agentmemory-url" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">
          Server URL
          <span class="font-normal text-black-600 dark:text-black-700">(optional)</span>
        </label>
        <input
          id="agentmemory-url"
          type="text"
          bind:value={serverUrl}
          placeholder={effectiveUrl || "leave empty = the daemon wick manages"}
          class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100"
        />
        <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">
          {#if usesManagedDaemon}
            Empty means the daemon wick starts and manages{effectiveUrl ? `, currently ${effectiveUrl}` : ""}. Point this elsewhere only to reach a server wick does not run.
          {:else}
            This instance talks to a server wick does not manage, so the daemon check above says nothing about it — verify that endpoint yourself.
          {/if}
        </p>
      </div>

      <div>
        <label for="agentmemory-token" class="block text-xs font-medium text-black-800 dark:text-black-600 mb-1">
          Auth token
          <span class="font-normal text-black-600 dark:text-black-700">(optional)</span>
        </label>
        <input
          id="agentmemory-token"
          type="password"
          bind:value={authKey}
          placeholder={authKeyMasked ? "•••••••• (leave empty to keep)" : "leave empty = no bearer token"}
          class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm font-mono text-black-900 dark:text-white-100"
        />
        <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Stored encrypted and never sent back to this page — a blank field keeps whatever is saved.</p>
      </div>

      <!-- Capture is the half people get wrong, so the consequence is spelled
           out rather than left to the word "capture": reading and recording
           are two different wirings with two different costs. -->
      <div class="border-t border-white-300 dark:border-navy-600 pt-3">
        <div class="flex items-center justify-between gap-3">
          <div>
            <p class="text-sm font-medium text-black-900 dark:text-white-100">Record this instance's sessions</p>
            <p id="agentmemory-capture-why" class="text-[11px] text-black-700 dark:text-black-600">
              MCP is how a spawn <strong class="font-medium">remembers</strong>; the capture hooks are how it gets
              <strong class="font-medium">recorded</strong>. With this off, spawns of this instance can read everything in memory,
              but nothing they do is written back — the store only grows from instances that have it on.
            </p>
          </div>
          <Toggle
            id="agentmemory-capture"
            checked={capture}
            onChange={(v) => (capture = v)}
            label="Record this instance's sessions"
            describedBy="agentmemory-capture-why"
            disabled={!captureSupported}
          />
        </div>
        {#if captureSupported}
          <p class="mt-2 text-[11px] text-black-700 dark:text-black-600">
            It is not free: the PreToolUse and PostToolUse hooks fire on <em>every</em> tool call, so each one runs the
            {backendName} binary once. On a spawn that makes hundreds of tool calls that is hundreds of extra processes.
          </p>
          {#if captureCaveat}
            <!-- A consequence of the switch belongs next to the switch. -->
            <p
              class="mt-2 rounded-lg border border-cau-400/30 bg-cau-400/5 px-3 py-2 text-[11px] text-cau-600 dark:text-cau-400"
              data-testid="capture-caveat"
            >
              {captureCaveat}
            </p>
          {/if}
        {:else if captureNote}
          <p class="mt-2 rounded-lg border border-cau-400/30 bg-cau-400/5 px-3 py-2 text-[11px] text-cau-400">{captureNote}</p>
        {/if}
      </div>

      {#if configPreview}
        <div class="border-t border-white-300 dark:border-navy-600 pt-3">
          <button
            type="button"
            onclick={() => (showAdvanced = !showAdvanced)}
            class="flex items-center gap-1.5 text-[11px] font-medium text-black-700 dark:text-black-600 hover:text-black-900 dark:hover:text-white-100"
          >
            <svg viewBox="0 0 16 16" class="h-3 w-3 transition-transform {showAdvanced ? 'rotate-90' : ''}" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"></path>
            </svg>
            Show what wick passes to {type}
          </button>
          {#if showAdvanced}
            <pre class="mt-2 max-h-64 overflow-auto rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-900 px-3 py-2 text-[12px] leading-relaxed font-mono text-black-800 dark:text-black-600 whitespace-pre-wrap">{configPreview}</pre>
            <p class="mt-1 text-[11px] text-black-700 dark:text-black-600">Read-only, and resolved with the toggle on so it shows the effect before you save it.</p>
          {/if}
        </div>
      {/if}
    {/if}
  </div>
{/if}
