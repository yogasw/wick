<script lang="ts">
  import { Button, Select } from "@wick-fe/common-ui";
  import { toastOk, toastError } from "@wick-fe/common-stores";
  import { apiSaveConfigKey } from "$lib/api.js";
  import type { ConfigFieldDTO } from "$lib/types.js";

  /* Compact-when-idle has its own card because it is not admin-only: a
     manager (whoever may reconnect the instance) may tune it too, and
     every key saves on its own endpoint, which the API allows for them. */
  type Props = {
    base: string;
    type: string;
    name: string;
    fields: ConfigFieldDTO[];
    onSaved?: () => void;
  };
  let { base, type, name, fields, onSaved }: Props = $props();

  const LABELS: Record<string, string> = {
    idle_compact_minutes: "Idle before compact (minutes)",
    idle_compact_trigger: "Compact trigger",
    idle_compact_threshold: "Compact threshold (% or k tokens)",
    idle_compact_scope: "Which sessions",
    idle_compact_match: "Session patterns for the scope",
  };

  const PLACEHOLDERS: Record<string, string> = {
    idle_compact_match: "id:rest-\nid:wf_adhoc_\nproject:support\ntitle:/^daily-/",
  };

  let values = $state<Record<string, string>>({});
  let saving = $state(false);
  $effect(() => {
    const next: Record<string, string> = {};
    for (const f of fields) next[f.Key] = f.Value;
    values = next;
  });

  let toggle = $derived(fields.find((f) => f.Key === "idle_compact"));
  let enabled = $derived(values["idle_compact"] === "true");
  // The pattern list means nothing when the scope is all.
  let details = $derived(
    fields.filter((f) => f.Key !== "idle_compact" && !(f.Key === "idle_compact_match" && values["idle_compact_scope"] === "all")),
  );

  async function setEnabled(on: boolean) {
    values["idle_compact"] = on ? "true" : "false";
    try {
      await apiSaveConfigKey(base, type, name, "idle_compact", values["idle_compact"]);
      toastOk(on ? "Compact when idle is on" : "Compact when idle is off");
      onSaved?.();
    } catch (e) {
      values["idle_compact"] = on ? "false" : "true";
      toastError(e instanceof Error ? e.message : "Save failed");
    }
  }

  async function saveDetails() {
    saving = true;
    try {
      for (const f of details) {
        await apiSaveConfigKey(base, type, name, f.Key, values[f.Key] ?? "");
      }
      toastOk("Idle compact saved");
      onSaved?.();
    } catch (e) {
      toastError(e instanceof Error ? e.message : "Save failed");
    } finally {
      saving = false;
    }
  }
</script>

{#if toggle}
  <div data-testid="idle-compact-card" class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 p-5 space-y-4">
    <div class="flex items-start justify-between gap-4">
      <div>
        <p class="text-sm font-semibold text-black-900 dark:text-white-100">Compact when idle</p>
        <p class="mt-1 text-[11px] text-black-700 dark:text-black-600 leading-relaxed">{toggle.Description}</p>
      </div>
      <button
        type="button"
        role="switch"
        aria-label="idle_compact"
        data-testid="idle-compact-toggle"
        aria-checked={enabled}
        onclick={() => setEnabled(!enabled)}
        class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors {enabled ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
      >
        <span class="inline-block h-5 w-5 transform rounded-full bg-white-100 shadow transition-transform {enabled ? 'translate-x-5' : 'translate-x-0.5'}"></span>
      </button>
    </div>
    {#if enabled}
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-5">
        {#each details as f (f.Key)}
          <div class={f.Type === "textarea" ? "sm:col-span-2" : ""}>
            <div class="flex items-center gap-2 mb-1.5">
              <span class="font-mono text-xs font-semibold text-black-900 dark:text-white-100">{f.Key}</span>
              {#if LABELS[f.Key]}<span data-testid={`field-label-${f.Key}`} class="text-xs text-black-700 dark:text-black-600">{LABELS[f.Key]}</span>{/if}
            </div>
            {#if (f.Type === "dropdown" || f.Type === "select") && f.Options}
              <Select
                ariaLabel={f.Key}
                value={values[f.Key] ?? ""}
                options={f.Options.split(f.Type === "dropdown" ? "|" : ",").map((o) => o.trim()).filter(Boolean)}
                onChange={(v) => { values[f.Key] = v; }}
              />
            {:else if f.Type === "textarea"}
              <textarea
                aria-label={f.Key}
                bind:value={values[f.Key]}
                rows="3"
                spellcheck="false"
                placeholder={PLACEHOLDERS[f.Key] ?? ""}
                class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-xs font-mono text-black-900 dark:text-white-100 placeholder:text-black-700 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
              ></textarea>
            {:else}
              <input
                type={f.Type === "number" ? "number" : "text"}
                aria-label={f.Key}
                bind:value={values[f.Key]}
                class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2.5 text-sm font-mono text-black-900 dark:text-white-100 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
              />
            {/if}
            {#if f.Description}
              <p class="mt-1.5 text-[11px] text-black-700 dark:text-black-600 leading-relaxed whitespace-pre-line">{f.Description}</p>
            {/if}
          </div>
        {/each}
      </div>
      <div class="flex justify-end">
        <Button variant="primary" onclick={saveDetails} disabled={saving}>{saving ? "Saving…" : "Save idle compact"}</Button>
      </div>
    {/if}
  </div>
{/if}
