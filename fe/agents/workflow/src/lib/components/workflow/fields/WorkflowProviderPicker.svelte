<script lang="ts">
  // Provider (+ model) picker for classify / agent nodes. Wraps the shared
  // ProviderPicker the chat and Team settings use, fed by the workflow
  // catalog — already narrowed server-side to the instances this viewer
  // may choose (provider access tags). See providerPick.ts for the value
  // mapping between the node fields and the picker.
  import { apiGet } from "@wick-fe/common-api";
  import { ProviderPicker, buildProviderOptions, withModelListMeta } from "@wick-fe/common-ui";
  import { catalog } from "$lib/stores/catalog";
  import { catalogProviderList, fromPickValue, toPickValue } from "./providerPick";

  type Props = {
    provider?: string;
    model?: string;
    /** Offer the model level (agent). classify runs the instance default. */
    withModels?: boolean;
    helper?: string;
    onChange: (provider: string, model: string) => void;
  };
  let { provider = "", model = "", withModels = false, helper, onChange }: Props = $props();

  const BASE = "/tools/agents";
  const providers = $derived($catalog?.providers ?? []);
  const value = $derived(toPickValue(providers, provider, withModels ? model : ""));
  const options = $derived(buildProviderOptions(catalogProviderList(providers), value));

  type ModelsResponse = { models?: { id: string; label: string; default: boolean }[] | null };
  function loadModels(optionValue: string, opts?: { entry?: string; refresh?: boolean }) {
    const s = optionValue.indexOf("/");
    const type = s < 0 ? optionValue : optionValue.slice(0, s);
    const name = s < 0 ? optionValue : optionValue.slice(s + 1);
    const q = new URLSearchParams();
    if (opts?.entry) q.set("entry", opts.entry);
    if (opts?.refresh) q.set("refresh", "1");
    const qs = q.toString();
    return apiGet<ModelsResponse>(
      `${BASE}/providers/options/${encodeURIComponent(type)}/${encodeURIComponent(name)}/models${qs ? `?${qs}` : ""}`,
    ).then((r) => withModelListMeta(r.models ?? [], r));
  }

  function pick(v: string) {
    const next = fromPickValue(v);
    onChange(next.provider, withModels ? next.model : "");
  }
</script>

<div class="flex flex-col gap-1">
  <span class="text-xs font-medium">Provider{withModels ? " / model" : ""}</span>
  <div class="flex items-center gap-2">
    <div class="min-w-0 flex-1">
      <ProviderPicker
        {options}
        {value}
        onChange={pick}
        placeholder="(default)"
        loadModels={withModels ? loadModels : undefined}
      />
    </div>
    {#if value}
      <button
        type="button"
        class="shrink-0 rounded border border-white-400 dark:border-navy-600 px-2 py-1 text-[11px] text-black-800 dark:text-black-600 hover:bg-white-200 dark:hover:bg-navy-700"
        onclick={() => onChange("", "")}
        title="Clear — use the engine default provider"
      >
        Use default
      </button>
    {/if}
  </div>
  {#if helper}
    <span class="text-[11px] text-black-700 dark:text-black-600">{helper}</span>
  {/if}
  {#if providers.length === 0}
    <div class="text-[11px] text-amber-600 dark:text-amber-400">
      No providers you can use — set one up in the Providers settings page, or ask an admin for access.
    </div>
  {/if}
</div>
