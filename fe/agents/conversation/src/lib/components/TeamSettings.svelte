<script lang="ts">
  /* Team settings drawer: what applies to every agent in the signed-in
     user's Team. Saves itself like an agent's Settings: the toggle at
     once, the prompt ~800ms after the last keystroke or on blur, only the
     fields that differ from what the server last confirmed. Tabs are laid
     out like the agent drawer's so more can join "General" later. */
  import { onMount } from "svelte";
  import { Toggle } from "@wick-fe/common-ui";
  import DrawerHeader from "./DrawerHeader.svelte";
  import { getTeamSettings, saveTeamSettings, runApi, type TeamSettings, type TeamSettingsWrite } from "../api/team.js";
  import type { TeamSettingsTab } from "../agentsRouter.js";

  type Props = {
    base: string;
    tab: TeamSettingsTab;
    onTab: (tab: TeamSettingsTab) => void;
    onClose: () => void;
  };
  let { base, tab, onTab, onClose }: Props = $props();

  const TABS: { id: TeamSettingsTab; label: string }[] = [{ id: "general", label: "General" }];

  let saved = $state<TeamSettings | null>(null);
  let prompt = $state("");
  let openTeam = $state(true);
  let loadError = $state("");
  let error = $state("");
  let status = $state<"idle" | "saving" | "saved" | "error">("idle");
  let saving = false;
  let failedKey = $state("");

  onMount(async () => {
    try {
      const s = await runApi(getTeamSettings(base));
      saved = s;
      prompt = s.prompt;
      openTeam = s.open_team;
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  });

  const limit = $derived(saved?.max_prompt_bytes ?? 16384);
  const bytes = $derived(new TextEncoder().encode(prompt).length);
  const tooLong = $derived(bytes > limit);

  const patch = $derived.by((): TeamSettingsWrite => {
    const p: TeamSettingsWrite = {};
    if (!saved) return p;
    if (prompt !== saved.prompt) p.prompt = prompt;
    if (openTeam !== saved.open_team) p.open_team = openTeam;
    return p;
  });
  const dirty = $derived(Object.keys(patch).length > 0);

  let timer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const key = JSON.stringify(patch);
    if (!dirty || tooLong || key === failedKey) return;
    clearTimeout(timer);
    timer = setTimeout(() => void save(), "prompt" in patch ? 800 : 0);
    return () => clearTimeout(timer);
  });
  /** flush sends a pending prompt edit now (field blur). */
  function flush() {
    if (!dirty || tooLong) return;
    clearTimeout(timer);
    void save();
  }
  function retry() {
    failedKey = "";
    flush();
  }

  async function save() {
    if (!dirty || tooLong || saving) return;
    const sent = $state.snapshot(patch) as TeamSettingsWrite;
    saving = true;
    status = "saving";
    error = "";
    try {
      saved = await runApi(saveTeamSettings(base, sent));
      failedKey = "";
      status = "saved";
    } catch (e) {
      failedKey = JSON.stringify(sent);
      error = e instanceof Error ? e.message : String(e);
      status = "error";
    } finally {
      saving = false;
    }
  }

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
</script>

<DrawerHeader title="Team settings" subtitle="Applies to every agent in your Team" bordered={false} {onClose} />

<div class="flex shrink-0 gap-1 overflow-x-auto border-b border-white-300 px-6 pb-3 dark:border-navy-600" role="tablist">
  {#each TABS as t (t.id)}
    <button
      type="button"
      role="tab"
      aria-selected={tab === t.id}
      class="whitespace-nowrap rounded-full px-3 py-1.5 text-xs font-medium {tab === t.id
        ? 'bg-black-900 text-white-100 dark:bg-white-200 dark:text-navy-700'
        : 'text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600'}"
      onclick={() => onTab(t.id)}
    >{t.label}</button>
  {/each}
</div>

<div class="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 py-4" onfocusout={flush}>
  {#if loadError}
    <p class="text-sm text-neg-400">{loadError}</p>
  {:else if !saved}
    <p class="text-sm text-black-800 dark:text-black-600">Loading…</p>
  {:else if tab === "general"}
    <div>
      <label class={label} for="ts-prompt">Team prompt</label>
      <textarea
        id="ts-prompt"
        class={input}
        rows="10"
        placeholder="e.g. Answer in Indonesian. Always link the ticket you worked on."
        bind:value={prompt}
      ></textarea>
      <div class="mt-1 flex flex-wrap items-start justify-between gap-2">
        <p class="text-xs text-black-800 dark:text-black-600">
          Markdown. Every agent in your Team reads it, after the operator prompt and before its own persona.
        </p>
        <span class="shrink-0 text-xs {tooLong ? 'text-neg-400' : 'text-black-700'}" data-testid="team-prompt-size">
          {(bytes / 1024).toFixed(1)} / {Math.round(limit / 1024)} KB
        </span>
      </div>
    </div>
    <div>
      <Toggle checked={openTeam} onChange={(v) => (openTeam = v)} label="Open Team when I open Agents" />
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">
        On: the Agents home opens Team. The "Agents" link at the bottom of the roster still takes you to the classic page.
      </p>
    </div>
    {#if saved.operator_prompt_href}
      <div class="border-t border-white-300 pt-4 dark:border-navy-600">
        <a href={saved.operator_prompt_href} class="text-sm font-medium text-green-600 hover:underline dark:text-green-400" data-testid="operator-prompt-link">
          Operator prompt (all users) →
        </a>
        <p class="mt-1 text-xs text-black-800 dark:text-black-600">Admin only: the Team agents system prompt every user's agents get.</p>
      </div>
    {/if}
  {/if}
  {#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
</div>

<div class="flex items-center justify-end gap-2 border-t border-white-300 px-6 py-4 dark:border-navy-600">
  <span class="mr-auto flex items-center gap-2 text-xs" aria-live="polite" data-testid="autosave-status">
    {#if tooLong}
      <span class="text-neg-400">Not saved — the prompt is over {Math.round(limit / 1024)} KB</span>
    {:else if status === "saving" || (dirty && JSON.stringify(patch) !== failedKey)}
      <span class="text-black-800 dark:text-black-600">Saving…</span>
    {:else if status === "error"}
      <span class="text-neg-400">Not saved</span><span aria-hidden="true" class="text-black-700">·</span>
      <button type="button" class="font-medium text-green-600 hover:underline" onclick={retry}>Retry</button>
    {:else if status === "saved"}
      <span class="text-black-800 dark:text-black-600">Saved ✓</span>
    {:else}
      <span class="text-black-700 dark:text-black-700">All changes saved</span>
    {/if}
  </span>
  <button type="button" class="rounded-lg px-4 py-2 text-sm text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600" onclick={onClose}>Close</button>
</div>
