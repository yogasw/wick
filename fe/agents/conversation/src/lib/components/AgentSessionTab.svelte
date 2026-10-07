<!--
  Settings › Session: how the agent's main chat is kept short (compact
  policy, autosaved), Compact now, the summarise-threads flag, and the
  Slack DM option shown read-only (it is set in Connections).
-->
<script lang="ts">
  import { onMount } from "svelte";
  import { Toggle } from "@wick-fe/common-ui";
  import { toastOk } from "@wick-fe/common-stores";
  import { getAgentSession, updateAgentSession, compactAgentMain, runApi, type AgentSessionPolicy } from "../api/team.js";
  import { MIN_IDLE_HOURS, MAX_IDLE_HOURS, clampIdleHours } from "../sessionPolicy.js";

  type Props = { base: string; agentId: string; onOpenConnections?: () => void };
  let { base, agentId, onOpenConnections }: Props = $props();

  let pol = $state<AgentSessionPolicy | null>(null);
  let error = $state("");
  let saveState = $state<"" | "saving" | "saved" | "error">("");
  let compacting = $state(false);
  let compactQueued = $state(false);
  let hoursTimer: ReturnType<typeof setTimeout> | undefined;

  onMount(async () => {
    try {
      pol = await runApi(getAgentSession(base, agentId));
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    }
  });

  async function save(body: Parameters<typeof updateAgentSession>[2]) {
    saveState = "saving";
    error = "";
    try {
      pol = await runApi(updateAgentSession(base, agentId, body));
      saveState = "saved";
    } catch (e) {
      saveState = "error";
      error = e instanceof Error ? e.message : String(e);
    }
  }

  function setCompact(mode: AgentSessionPolicy["compact"]) {
    if (!pol || pol.compact === mode) return;
    pol.compact = mode;
    void save({ compact: mode });
  }
  /** Hours autosave a beat after typing stops, clamped to 1–168. */
  function queueHours() {
    clearTimeout(hoursTimer);
    hoursTimer = setTimeout(() => {
      if (!pol) return;
      pol.idle_hours = clampIdleHours(pol.idle_hours);
      void save({ idle_hours: pol.idle_hours });
    }, 600);
  }

  async function compactNow() {
    compacting = true;
    error = "";
    try {
      await runApi(compactAgentMain(base, agentId));
      compactQueued = true;
      toastOk("Compact queued");
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      compacting = false;
    }
  }

  const muted = "text-xs text-black-800 dark:text-black-600";
  const radio = "flex items-start gap-2 text-sm text-black-900 dark:text-white-100";
</script>

<div data-testid="session-tab" class="space-y-4">
  <div>
    <p class="text-sm font-semibold text-black-900 dark:text-white-100">Session</p>
    <p class="mt-1 {muted}">How your main chat with this agent stays short over time.</p>
  </div>

  {#if pol}
    <fieldset class="space-y-2">
      <legend class="mb-1 text-xs font-medium text-black-800 dark:text-black-600">Compact policy</legend>
      <label class={radio}>
        <input type="radio" name="compact" class="mt-1" checked={pol.compact === "auto"} onchange={() => setCompact("auto")} />
        <span>Auto (provider default)<span class="block {muted}">The provider compacts when its context fills up.</span></span>
      </label>
      <label class={radio}>
        <input type="radio" name="compact" class="mt-1" checked={pol.compact === "idle"} onchange={() => setCompact("idle")} />
        <span class="flex flex-wrap items-center gap-2">
          Compact when idle for
          <input
            type="number"
            min={MIN_IDLE_HOURS}
            max={MAX_IDLE_HOURS}
            class="w-20 rounded-lg border border-white-300 bg-white-100 px-2 py-1 text-sm text-black-900 focus:border-green-500 focus:outline-none disabled:opacity-50 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
            aria-label="Idle hours"
            disabled={pol.compact !== "idle"}
            bind:value={pol.idle_hours}
            oninput={queueHours}
          />
          hours
          <span class="block w-full {muted}">Once per idle stretch, so the next conversation starts on a short context.</span>
        </span>
      </label>
    </fieldset>

    <div class="flex items-center gap-3">
      <button
        type="button"
        class="shrink-0 rounded-lg border border-white-300 px-3 py-1.5 text-xs font-medium text-black-900 hover:bg-white-200 disabled:opacity-50 dark:border-navy-600 dark:text-white-100 dark:hover:bg-navy-700"
        disabled={!pol.main_session_id || compacting}
        title={pol.main_session_id ? "Send /compact to your main chat" : "Open the agent's chat first"}
        onclick={compactNow}
        data-testid="compact-now"
      >Compact now</button>
      {#if compactQueued}<span class={muted} data-testid="compact-status">Queued</span>{/if}
    </div>

    <div class="flex items-start justify-between gap-3">
      <div>
        <p class="text-sm text-black-900 dark:text-white-100">Summarise Slack/A2A threads into the agent's memory when idle</p>
        <p class="mt-0.5 {muted}">Saved now; the summarising itself is coming soon.</p>
      </div>
      <Toggle checked={pol.summarise_threads} onChange={(v) => { if (pol) { pol.summarise_threads = v; void save({ summarise_threads: v }); } }} label="Summarise threads" />
    </div>

    <div class="rounded-lg bg-white-200 px-3 py-2 dark:bg-navy-600" data-testid="session-dm">
      <p class="text-sm text-black-900 dark:text-white-100">
        Slack DMs: {pol.slack_connected ? (pol.slack_dm_main_chat ? "continue the main chat" : "a new chat per thread") : "Slack is not connected"}
      </p>
      <p class="mt-0.5 {muted}">
        Set in Connections.
        {#if onOpenConnections}<button type="button" class="font-medium text-green-600 hover:underline" onclick={onOpenConnections}>Open Connections</button>{/if}
      </p>
    </div>

    <p class={muted} aria-live="polite" data-testid="session-save">{saveState === "saving" ? "Saving…" : saveState === "saved" ? "Saved ✓" : saveState === "error" ? "Not saved" : ""}</p>
  {:else if !error}
    <p class={muted}>Loading…</p>
  {/if}
  {#if error}<p class="text-xs text-neg-400" data-testid="session-error">{error}</p>{/if}
</div>
