<script lang="ts">
  /* Remote tab of a plugin remote agent: the per-agent config its service
     plugin declares, saved with one button through PATCH …/plugin-remote.
     Secrets are write-only — the form starts blank and a blank save keeps
     the stored value. */
  import { onMount } from "svelte";
  import PluginRemoteConfigFields from "./PluginRemoteConfigFields.svelte";
  import { getPluginRemote, updatePluginRemote, runApi, type AgentItem, type PluginRemoteInfo } from "../../api/team.js";
  import { missingPluginConfig, pluginConfigBody } from "../../remoteAgent.js";

  type Props = { base: string; agent: AgentItem };
  let { base, agent }: Props = $props();

  let info = $state<PluginRemoteInfo | null>(null);
  let loadError = $state("");
  let values = $state<Record<string, string>>({});
  let saved = $state<Record<string, string>>({});
  let busy = $state(false);
  let error = $state("");
  let note = $state("");

  function take(r: PluginRemoteInfo) {
    info = r;
    const v: Record<string, string> = {};
    for (const f of r.configs) v[f.key] = f.is_secret ? "" : f.value;
    values = v;
    saved = { ...v };
  }

  onMount(() => {
    runApi(getPluginRemote(base, agent.id))
      .then(take)
      .catch((e) => { loadError = e instanceof Error ? e.message : String(e); });
  });

  const missing = $derived(missingPluginConfig(info?.configs, values));
  const dirty = $derived(Object.keys(values).some((k) => (values[k] ?? "") !== (saved[k] ?? "")));

  async function save() {
    if (!info || busy || missing.length) return;
    busy = true;
    error = "";
    note = "";
    try {
      take(await runApi(updatePluginRemote(base, agent.id, pluginConfigBody(info.configs, values))));
      note = "Saved.";
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      busy = false;
    }
  }

  const primary =
    "rounded-lg bg-green-500 px-4 py-2 text-sm font-medium text-white-100 hover:bg-green-600 disabled:opacity-50";
</script>

{#if !info}
  <p class="text-sm {loadError ? 'text-neg-400' : 'text-black-800 dark:text-black-600'}">{loadError || "Loading…"}</p>
{:else}
  <div class="space-y-4" data-testid="plugin-remote-settings">
    <div>
      <p class="text-sm font-semibold text-black-900 dark:text-white-100">Remote plugin</p>
      <p class="mt-1 text-xs text-black-800 dark:text-black-600">Plugin: <span class="font-mono">{info.plugin_key}</span></p>
    </div>
    {#if info.configs.length === 0}
      <p class="text-xs text-black-800 dark:text-black-600">This plugin has no per-agent settings.</p>
    {:else}
      <PluginRemoteConfigFields fields={info.configs} bind:values idPrefix="prp-cfg" />
      <button type="button" class={primary} disabled={!dirty || missing.length > 0 || busy} data-testid="prp-save" onclick={save}>{busy ? "Saving…" : "Save"}</button>
    {/if}
  </div>
{/if}
{#if error}<p class="mt-2 text-sm text-neg-400">{error}</p>{/if}
{#if note && !error}<p class="mt-2 text-xs text-green-600 dark:text-green-400">{note}</p>{/if}
