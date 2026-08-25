<script lang="ts">
  /* Per-tool config editor, ported from tool_detail.templ. No schedule, no
     runs — just the reusable ConfigsForm scoped to the tool key, with a
     tool-scoped save injected (POSTs /manager/api/tools/{key}/configs/…). */
  import { toastError } from "@wick-fe/common-stores";
  import { getTool, setToolConfig } from "$lib/api.js";
  import type { ToolDetail } from "$lib/types.js";
  import ConfigsForm from "../fields/ConfigsForm.svelte";
  import { setBreadcrumbNames, clearBreadcrumbNames } from "$lib/stores/breadcrumb.js";

  type Props = { toolKey: string };
  let { toolKey }: Props = $props();

  let data = $state<ToolDetail | null>(null);
  let loading = $state(true);
  let error = $state("");
  let missingRequired = $derived((data?.fields ?? []).filter((f) => f.required && !f.has_value && !f.hidden).length);

  async function load(silent = false): Promise<void> {
    if (!silent) loading = true;
    try {
      data = await getTool(toolKey);
      error = "";
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (silent) {
        toastError("Refresh failed", msg);
      } else {
        error = msg;
      }
    } finally {
      if (!silent) loading = false;
    }
  }

  async function saveConfig(key: string, value: string): Promise<void> {
    await setToolConfig(toolKey, key, value);
  }

  let webhooks = $derived(data?.webhooks ?? []);
  let copied = $state("");

  /* Copying the full URL is the common next step after reading this panel —
     it goes straight into the sending system's configuration. */
  async function copyUrl(url: string): Promise<void> {
    try {
      await navigator.clipboard.writeText(url);
      copied = url;
      setTimeout(() => {
        if (copied === url) copied = "";
      }, 2000);
    } catch (e) {
      toastError("Copy failed", e instanceof Error ? e.message : String(e));
    }
  }

  $effect(() => {
    if (data) setBreadcrumbNames({ tool: data.name });
  });

  $effect(() => {
    load();
    return clearBreadcrumbNames;
  });
</script>

{#if loading}
  <div class="px-5 py-12 text-center text-sm text-black-700 dark:text-black-600">Loading…</div>
{:else if error}
  <div class="rounded-lg border border-red-300 dark:border-red-800 bg-red-50 dark:bg-red-900/20 px-4 py-3 text-sm text-red-700 dark:text-red-400">{error}</div>
{:else if data}
  <div class="space-y-6">
    {#if missingRequired > 0}
      <div class="rounded-lg border border-cau-300 bg-cau-100 px-4 py-3 text-cau-400" role="alert">
        <div class="flex items-center gap-3 text-sm">
          <span aria-hidden="true" class="text-base leading-5">⚠️</span>
          <div class="flex-1 text-black-900 dark:text-black-800">
            <span class="font-medium">Setup required —</span>
            <span>{data.name} is missing {missingRequired} required {missingRequired === 1 ? "value" : "values"}.</span>
          </div>
        </div>
      </div>
    {/if}
    <div class="flex items-center gap-3">
      <div class="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-lg bg-green-200 dark:bg-green-800 text-lg font-semibold text-green-700 dark:text-green-300">{data.icon}</div>
      <div>
        <h1 class="text-lg font-semibold text-black-900 dark:text-white-100">{data.name}</h1>
        {#if data.description}
          <p class="mt-0.5 text-sm text-black-800 dark:text-black-600">{data.description}</p>
        {/if}
      </div>
    </div>

    {#if webhooks.length > 0}
      <section>
        <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Webhook endpoints</h2>
        <p class="mt-1 text-sm text-black-800 dark:text-black-600">
          These paths answer <span class="font-medium">without a login</span>, regardless of this tool's
          visibility — declared with <code class="font-mono text-xs">r.WebhookGroup(...)</code> so external
          senders can reach them. Each handler verifies its own requests.
        </p>
        <div class="mt-4 overflow-x-auto rounded-lg border border-white-300 dark:border-navy-600">
          <table class="w-full min-w-[480px] border-collapse text-left">
            <thead class="bg-white-200 dark:bg-navy-800">
              <tr>
                <th class="px-4 py-3 text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Method</th>
                <th class="px-4 py-3 text-xs font-medium uppercase tracking-wide text-black-800 dark:text-black-600">Endpoint</th>
                <th class="px-4 py-3"><span class="sr-only">Copy</span></th>
              </tr>
            </thead>
            <tbody>
              {#each webhooks as wh (wh.method + wh.path)}
                <tr class="border-t border-white-300 dark:border-navy-600">
                  <td class="px-4 py-3 align-top">
                    <span class="rounded-sm bg-white-200 dark:bg-navy-800 px-2 py-1 font-mono text-xs font-medium text-black-900 dark:text-white-100">{wh.method}</span>
                  </td>
                  <td class="px-4 py-3 align-top font-mono text-xs text-black-900 dark:text-white-100 break-all">{wh.url || wh.path}</td>
                  <td class="px-4 py-3 align-top text-right">
                    {#if wh.url}
                      <button
                        type="button"
                        onclick={() => copyUrl(wh.url!)}
                        class="rounded-lg border border-green-500 px-3 py-1 text-xs font-medium text-green-500 transition-colors duration-150 hover:bg-green-200 dark:hover:bg-green-800"
                      >
                        {copied === wh.url ? "Copied" : "Copy"}
                      </button>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      </section>
    {/if}

    <section>
      <h2 class="text-base font-semibold text-black-900 dark:text-white-100">Settings</h2>
      <p class="mt-1 text-sm text-black-800 dark:text-black-600">Runtime variables for this tool instance. Handlers read values via <code class="font-mono text-xs">c.Cfg(...)</code>.</p>
      <ConfigsForm fields={data.fields ?? []} canConfigure={data.can_configure} save={saveConfig} />
    </section>
  </div>
{/if}
