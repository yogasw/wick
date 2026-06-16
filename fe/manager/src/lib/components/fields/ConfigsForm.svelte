<script lang="ts">
  /* Reusable connector config block: a simple-fields grid + kvlist blocks,
     with visible_when toggling, a missing-required badge, a stored-secret
     badge, env-override badges, and per-key auto-save.

     Auto-save mirrors the legacy configs.templ: immediate controls (select,
     checkbox, color, date, datetime) and kvlist removals save on change, while
     free-text fields debounce ~800ms. Each POST hits the per-key endpoint via
     setConnectorConfig; success/failure surface as a per-field status + toast. */
  import { untrack } from "svelte";
  import type { ConfigField } from "$lib/types.js";
  import { setConnectorConfig } from "$lib/api.js";
  import { toastError } from "@wick-fe/common-stores";
  import { isVisible } from "./options.js";
  import FieldWidget from "./FieldWidget.svelte";
  import KvListField from "./KvListField.svelte";

  type SaveState = "" | "saving" | "saved" | "error";
  type Props = {
    connectorKey?: string;
    connectorId?: string;
    fields: ConfigField[];
    canConfigure: boolean;
    save?: (key: string, value: string) => Promise<void>;
  };
  let { connectorKey = "", connectorId = "", fields, canConfigure, save }: Props = $props();

  /* Default persist hits the per-key connector config endpoint; job/tool
     detail pages inject their own save targeting /manager/api/{jobs,tools}. */
  function persistFn(key: string, value: string): Promise<void> {
    return save ? save(key, value) : setConnectorConfig(connectorKey, connectorId, key, value);
  }

  const DEBOUNCE_MS = 800;
  const IMMEDIATE = new Set(["dropdown", "checkbox", "bool", "boolean", "color", "date", "datetime"]);

  let values = $state<Record<string, string>>(untrack(() => initValues(fields)));
  let savedHasValue = $state<Record<string, boolean>>(untrack(() => initHasValue(fields)));
  let saveStatus = $state<Record<string, SaveState>>({});
  const timers: Record<string, ReturnType<typeof setTimeout>> = {};

  function initValues(list: ConfigField[]): Record<string, string> {
    const out: Record<string, string> = {};
    for (const f of list) {
      out[f.key] = f.is_secret ? "" : f.value;
    }
    return out;
  }

  function initHasValue(list: ConfigField[]): Record<string, boolean> {
    const out: Record<string, boolean> = {};
    for (const f of list) {
      out[f.key] = f.has_value;
    }
    return out;
  }

  let simpleFields = $derived(fields.filter((f) => f.type !== "kvlist" && f.type !== "picker"));
  let kvFields = $derived(fields.filter((f) => f.type === "kvlist"));

  function visible(field: ConfigField): boolean {
    return isVisible(field.visible_when, values);
  }

  function missing(field: ConfigField): boolean {
    return field.required && (savedHasValue[field.key] ? false : (values[field.key] ?? "").trim() === "");
  }

  async function persist(key: string, value: string) {
    saveStatus = { ...saveStatus, [key]: "saving" };
    try {
      await persistFn(key, value);
      saveStatus = { ...saveStatus, [key]: "saved" };
      if (value.trim() !== "") savedHasValue = { ...savedHasValue, [key]: true };
      setTimeout(() => {
        if (saveStatus[key] === "saved") saveStatus = { ...saveStatus, [key]: "" };
      }, 2000);
    } catch (e) {
      saveStatus = { ...saveStatus, [key]: "error" };
      toastError("Save failed", `${key}: ${e instanceof Error ? e.message : String(e)}`);
    }
  }

  function onFieldChange(field: ConfigField, value: string) {
    values = { ...values, [field.key]: value };
    if (timers[field.key]) clearTimeout(timers[field.key]);
    if (IMMEDIATE.has(field.type)) {
      persist(field.key, value);
    } else {
      timers[field.key] = setTimeout(() => persist(field.key, value), DEBOUNCE_MS);
    }
  }

  function onKvChange(field: ConfigField, json: string) {
    values = { ...values, [field.key]: json };
    if (timers[field.key]) clearTimeout(timers[field.key]);
    timers[field.key] = setTimeout(() => persist(field.key, json), DEBOUNCE_MS);
  }

  function onKvCommit(field: ConfigField) {
    if (timers[field.key]) clearTimeout(timers[field.key]);
    persist(field.key, values[field.key] ?? "");
  }

  function statusText(s: SaveState): string {
    return s === "saving" ? "saving…" : s === "saved" ? "✓ saved" : s === "error" ? "✗ failed" : "";
  }
  function statusClass(s: SaveState): string {
    return s === "saved"
      ? "text-pos-400"
      : s === "error"
        ? "text-neg-400"
        : "text-black-700 dark:text-black-600";
  }
</script>

{#if fields.length === 0}
  <p class="mt-4 text-sm text-black-700 dark:text-black-600">No configuration fields.</p>
{:else}
  <div class="mt-4 flex flex-col gap-3">
    {#if simpleFields.length > 0}
      <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
        <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
          <h3 class="text-sm font-semibold text-black-900 dark:text-white-100">Configuration</h3>
        </div>
        <div class="p-5">
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-5">
            {#each simpleFields as field (field.key)}
              {#if visible(field)}
                <div class={field.type === "textarea" ? "col-span-full" : ""}>
                  <div class="flex items-center gap-2 mb-1.5">
                    <span class="font-mono text-xs font-semibold text-black-900 dark:text-white-100">{field.key}</span>
                    {#if missing(field)}
                      <span class="rounded bg-neg-100 px-1.5 py-0.5 text-[10px] font-semibold text-neg-400">missing</span>
                    {:else if field.required}
                      <span class="text-[10px] font-semibold text-neg-400">*</span>
                    {/if}
                    {#if field.is_secret && savedHasValue[field.key]}
                      <span class="rounded-full bg-pos-100 px-1.5 py-0.5 text-[10px] font-semibold text-pos-400">stored</span>
                    {/if}
                    {#if field.env_override}
                      <span class="rounded-full bg-prog-100 px-1.5 py-0.5 text-[10px] font-semibold text-prog-400" title="Overridden by environment variable">env: {field.env_override}</span>
                    {/if}
                    <span class="ml-auto text-[10px] transition-all {statusClass(saveStatus[field.key] ?? '')}">{statusText(saveStatus[field.key] ?? "")}</span>
                  </div>
                  <FieldWidget
                    {field}
                    value={values[field.key] ?? ""}
                    disabled={!canConfigure || field.env_override !== ""}
                    onChange={(v) => onFieldChange(field, v)}
                  />
                  {#if field.env_override}
                    <p class="mt-1.5 text-[11px] text-prog-400 leading-relaxed">Overridden by <span class="font-mono">{field.env_override}</span> environment variable. Unset it and restart to edit from here.</p>
                  {:else if field.description}
                    <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600 leading-relaxed whitespace-pre-line">{field.description}</p>
                  {/if}
                </div>
              {/if}
            {/each}
          </div>
        </div>
      </div>
    {/if}

    {#each kvFields as field (field.key)}
      {#if visible(field)}
        <div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 shadow-sm overflow-hidden">
          <div class="px-5 py-3 border-b border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800">
            <div class="flex items-center gap-2">
              <span class="font-mono text-sm font-semibold text-black-900 dark:text-white-100">{field.key}</span>
              {#if missing(field)}
                <span class="rounded bg-neg-100 px-1.5 py-0.5 text-[10px] font-semibold text-neg-400">missing</span>
              {/if}
              <span class="ml-auto text-[10px] transition-all {statusClass(saveStatus[field.key] ?? '')}">{statusText(saveStatus[field.key] ?? "")}</span>
            </div>
            {#if field.description}
              <p class="mt-0.5 text-xs text-black-700 dark:text-black-600 whitespace-pre-line">{field.description}</p>
            {/if}
          </div>
          <div class="p-5">
            <KvListField
              {field}
              onSave={(json) => onKvChange(field, json)}
              onCommit={() => onKvCommit(field)}
            />
          </div>
        </div>
      {/if}
    {/each}
  </div>
{/if}
