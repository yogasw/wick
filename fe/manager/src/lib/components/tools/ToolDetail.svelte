<script lang="ts">
  /* Per-tool config editor, ported from tool_detail.templ. No schedule, no
     runs — just the reusable ConfigsForm scoped to the tool key, with a
     tool-scoped save injected (POSTs /manager/api/tools/{key}/configs/…). */
  import { getTool, setToolConfig } from "$lib/api.js";
  import type { ToolDetail } from "$lib/types.js";
  import ConfigsForm from "../fields/ConfigsForm.svelte";

  type Props = { toolKey: string };
  let { toolKey }: Props = $props();

  let data = $state<ToolDetail | null>(null);
  let loading = $state(true);
  let error = $state("");

  async function load(): Promise<void> {
    loading = true;
    error = "";
    try {
      data = await getTool(toolKey);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  async function saveConfig(key: string, value: string): Promise<void> {
    await setToolConfig(toolKey, key, value);
  }

  $effect(() => { load(); });
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if error}
  <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
{:else if data}
  <div class="space-y-6">
    <div class="flex items-center gap-3">
      <div class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-lg font-semibold text-green-700 dark:text-green-300">{data.icon}</div>
      <div>
        <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">{data.name}</h1>
        {#if data.description}
          <p class="mt-0.5 text-sm text-black-800 dark:text-black-600">{data.description}</p>
        {/if}
      </div>
    </div>

    <section>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Settings</h2>
      <p class="mt-1 text-sm text-black-800 dark:text-black-600">Runtime variables for this tool instance. Handlers read values via <code class="font-mono text-xs">c.Cfg(...)</code>.</p>
      <ConfigsForm fields={data.fields ?? []} canConfigure={data.can_configure} save={saveConfig} />
    </section>
  </div>
{/if}
