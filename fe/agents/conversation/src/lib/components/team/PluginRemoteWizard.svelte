<script lang="ts">
  /* + Agent › Remote agent › Plugin: an agent served by a service plugin
     that declares remote_source. One step: pick the plugin, name it. */
  import { onMount } from "svelte";
  import RemoteSourcePicker from "./RemoteSourcePicker.svelte";
  import { createPluginRemote, listPluginSources, runApi, type AgentItem, type PluginSource } from "../../api/team.js";
  import { HANDLE_RE } from "../../agentForm.js";

  type RemoteType = "local" | "remote" | "slack" | "plugin";
  type Props = {
    base: string;
    taken: string[];
    onClose: () => void;
    onCreated: (a: AgentItem) => void;
    onType?: (t: RemoteType) => void;
  };
  let { base, taken, onClose, onCreated, onType }: Props = $props();

  let sources = $state<PluginSource[] | null>(null);
  let loadError = $state("");
  let pluginKey = $state("");
  let name = $state("");
  let handle = $state("");
  let error = $state("");
  let saving = $state(false);

  onMount(async () => {
    try {
      sources = await runApi(listPluginSources(base));
      if (sources.length === 1) pluginKey = sources[0].key;
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
      sources = [];
    }
  });

  const handleWhy = $derived.by(() => {
    const h = handle.trim();
    if (!h) return "";
    if (!HANDLE_RE.test(h)) return "Lowercase letters, numbers and dashes.";
    if (taken.includes(h)) return "That handle is taken.";
    return "";
  });

  async function submit() {
    if (!pluginKey || handleWhy || saving) return;
    saving = true;
    error = "";
    try {
      const a = await runApi(createPluginRemote(base, {
        plugin_key: pluginKey,
        ...(name.trim() ? { name: name.trim() } : {}),
        ...(handle.trim() ? { handle: handle.trim() } : {}),
      }));
      onCreated(a);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      saving = false;
    }
  }

  const input =
    "w-full rounded-lg border border-white-300 bg-white-100 px-3 py-2 text-sm text-black-900 focus:border-green-500 focus:outline-none dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";
  const label = "mb-1 block text-xs font-medium text-black-800 dark:text-black-600";
  const primary =
    "rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50";
</script>

<div class="flex items-center gap-2 px-6 pt-5">
  <h2 class="flex-1 text-[17px] font-semibold text-black-900 dark:text-white-100">New agent</h2>
  <button
    type="button"
    class="flex h-8 w-8 items-center justify-center rounded-lg text-black-800 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-600"
    aria-label="Close"
    onclick={onClose}
  >
    <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M4 4l8 8M12 4l-8 8"></path></svg>
  </button>
</div>

{#if onType}
  <div class="px-6 pt-3">
    <div class="inline-flex rounded-lg border border-white-300 p-0.5 dark:border-navy-600" role="group" aria-label="Agent type">
      <button type="button" class="rounded-md px-3 py-1 text-xs text-black-800 dark:text-black-600" aria-pressed="false" onclick={() => onType?.("local")}>Wick agent</button>
      <button type="button" class="rounded-md bg-green-500 px-3 py-1 text-xs text-white-100" aria-pressed="true">Remote agent</button>
    </div>
  </div>
  <RemoteSourcePicker value="plugin" onSource={(s) => { if (s === "a2a") onType?.("remote"); else if (s === "slack") onType?.("slack"); }} />
{/if}

<div class="min-h-0 flex-1 space-y-4 overflow-y-auto px-6 pt-4 pb-2" data-testid="plugin-remote-wizard">
  <div>
    <p class={label} id="prw-plugin">Service plugin</p>
    {#if sources === null}
      <p class="text-sm text-black-800 dark:text-black-600">Loading…</p>
    {:else if sources.length === 0}
      <p class="rounded-lg border border-white-300 px-3 py-2 text-sm text-black-800 dark:border-navy-600 dark:text-black-600" data-testid="plugin-none">
        {loadError || "No service plugin offers a remote agent source. An admin installs one under Plugins."}
      </p>
    {:else}
      <div class="space-y-2" role="radiogroup" aria-labelledby="prw-plugin">
        {#each sources as s (s.key)}
          <button
            type="button"
            role="radio"
            aria-checked={pluginKey === s.key}
            data-testid="plugin-source-{s.key}"
            class="flex w-full items-start gap-3 rounded-xl border px-3 py-2 text-left text-sm {pluginKey === s.key
              ? 'border-green-500'
              : 'border-white-300 hover:bg-white-200 dark:border-navy-600 dark:hover:bg-navy-600'}"
            onclick={() => (pluginKey = s.key)}
          >
            <span class="flex-1">
              <span class="block font-medium text-black-900 dark:text-white-100">{s.name || s.key} <span class="font-mono text-[11px] text-black-800 dark:text-black-600">v{s.version}</span></span>
              {#if s.description}<span class="block text-xs text-black-800 dark:text-black-600">{s.description}</span>{/if}
            </span>
            <span class="rounded-full px-2 py-0.5 text-[10px] font-medium uppercase tracking-wider {s.state === 'running' ? 'bg-green-100 text-green-700' : 'bg-white-300 text-black-800 dark:bg-navy-600 dark:text-black-600'}">{s.state}</span>
          </button>
        {/each}
      </div>
    {/if}
  </div>
  <div class="grid grid-cols-2 gap-3">
    <div>
      <label class={label} for="prw-name">Name</label>
      <input id="prw-name" class={input} bind:value={name} placeholder="Optional — the plugin's name" />
    </div>
    <div>
      <label class={label} for="prw-handle">Handle</label>
      <input id="prw-handle" class="{input} font-mono" bind:value={handle} placeholder="auto" />
      {#if handleWhy}<p class="mt-1 text-xs text-red-500">{handleWhy}</p>{/if}
    </div>
  </div>
  <p class="text-xs text-black-800 dark:text-black-600">Turns go to the plugin process on this host; it decides where they travel next.</p>
  {#if error}<p class="text-sm text-red-500" role="alert">{error}</p>{/if}
</div>

<div class="flex items-center justify-end gap-2 px-6 py-4">
  <button type="button" class={primary} disabled={!pluginKey || !!handleWhy || saving} onclick={submit}>{saving ? "Creating…" : "Create agent"}</button>
</div>
