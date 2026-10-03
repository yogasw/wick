<script lang="ts">
  /* Team settings drawer: every per-user Team option, one tab per group
     (teamSettingsTabs.ts). Saves itself like an agent's Settings: toggles
     at once, typed fields ~800ms after the last keystroke or on blur, and
     only the settings that differ from what the server last confirmed —
     whichever tab they live on. The drawer owns the draft and the saving;
     a tab's view only edits the draft. */
  import { onMount, type Component } from "svelte";
  import DrawerHeader from "./DrawerHeader.svelte";
  import TeamSettingsGeneral from "./TeamSettingsGeneral.svelte";
  import {
    getTeamSettings, saveTeamSettings, runApi, TEAM_SETTING_KEYS,
    type TeamSettings, type TeamSettingValues, type TeamSettingsWrite,
  } from "../api/team.js";
  import { TEAM_SETTINGS_TABS, invalidReason, isTextKey, type TeamSettingsTab } from "../teamSettingsTabs.js";
  import { setIdleAnimations } from "@wick-fe/common-avatar";

  type Props = {
    base: string;
    tab: TeamSettingsTab;
    onTab: (tab: TeamSettingsTab) => void;
    onClose: () => void;
  };
  let { base, tab, onTab, onClose }: Props = $props();

  type TabView = Component<{ draft: TeamSettingValues; saved: TeamSettings }, {}, "draft">;
  /** Each tab's view. Record over TeamSettingsTab: a tab added to the
      registry without a view here does not compile. */
  const VIEWS: Record<TeamSettingsTab, TabView> = { general: TeamSettingsGeneral };

  let saved = $state<TeamSettings | null>(null);
  let draft = $state<TeamSettingValues | null>(null);
  let loadError = $state("");
  let error = $state("");
  let status = $state<"idle" | "saving" | "saved" | "error">("idle");
  let saving = false;
  let failedKey = $state("");

  const pick = (s: TeamSettings): TeamSettingValues =>
    Object.fromEntries(TEAM_SETTING_KEYS.map((k) => [k, s[k]])) as TeamSettingValues;

  onMount(async () => {
    try {
      const s = await runApi(getTeamSettings(base));
      saved = s;
      draft = pick(s);
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  });

  // What the server confirmed drives the page's avatars at once.
  $effect(() => {
    if (saved) setIdleAnimations(saved.idle_animations !== false);
  });

  const invalid = $derived(saved && draft ? invalidReason(draft, saved) : "");

  const patch = $derived.by((): TeamSettingsWrite => {
    const p: Record<string, unknown> = {};
    if (!saved || !draft) return p;
    for (const k of TEAM_SETTING_KEYS) if (draft[k] !== saved[k]) p[k] = draft[k];
    return p as TeamSettingsWrite;
  });
  const dirty = $derived(Object.keys(patch).length > 0);

  let timer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const key = JSON.stringify(patch);
    if (!dirty || invalid || key === failedKey) return;
    clearTimeout(timer);
    timer = setTimeout(() => void save(), Object.keys(patch).some(isTextKey) ? 800 : 0);
    return () => clearTimeout(timer);
  });
  /** flush sends a pending text edit now (field blur). */
  function flush() {
    if (!dirty || invalid) return;
    clearTimeout(timer);
    void save();
  }
  function retry() {
    failedKey = "";
    flush();
  }

  async function save() {
    if (!dirty || invalid || saving) return;
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

  const View = $derived(VIEWS[tab]);
</script>

<DrawerHeader title="Team settings" subtitle="Applies to every agent in your Team" bordered={false} {onClose} />

<div class="flex shrink-0 gap-1 overflow-x-auto border-b border-white-300 px-6 pb-3 dark:border-navy-600" role="tablist">
  {#each TEAM_SETTINGS_TABS as t (t.id)}
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
  {:else if !saved || !draft}
    <p class="text-sm text-black-800 dark:text-black-600">Loading…</p>
  {:else}
    <View bind:draft {saved} />
  {/if}
  {#if error}<p class="text-sm text-neg-400">{error}</p>{/if}
</div>

<div class="flex items-center justify-end gap-2 border-t border-white-300 px-6 py-4 dark:border-navy-600">
  <span class="mr-auto flex items-center gap-2 text-xs" aria-live="polite" data-testid="autosave-status">
    {#if invalid}
      <span class="text-neg-400">Not saved — {invalid}</span>
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
