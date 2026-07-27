<script lang="ts">
  /* ConfigForm — a lean, generic renderer for an entity.Config[] field spec
     (the shape a BE reflects from a `wick:"..."`-tagged struct). It emits one
     onChange(key, value) per edit — the caller decides whether to auto-save or
     batch. Deliberately minimal: only the widgets in use today (toggle switch +
     dropdown, text fallback). Add more branches here as new field types get
     used, rather than pulling in the workflow editor's richer SchemaForm (which
     carries arg-mode / expression / lookup machinery this doesn't need).

     Honours visible_when + hidden + group (grouping is a light heading). Values
     are the string forms the BE stores (bool → "true"/"false"). */
  import Select from "./Select.svelte";
  import { type ConfigField, dropdownOptions, isVisible, isToggle } from "./config-types.js";

  type Props = {
    schema: ConfigField[];
    /** key → current string value. */
    values: Record<string, string>;
    onChange: (key: string, value: string) => void;
  };

  let { schema, values, onChange }: Props = $props();

  const visible = $derived(schema.filter((f) => isVisible(f, values)));

  // Group heading = the part before "|" in the Group tag (a description may
  // follow after "|"). Rows with the same heading render under one label; a
  // heading only prints once, before its first field.
  function heading(f: ConfigField): string {
    if (!f.Group) return "";
    return f.Group.split("|")[0].trim();
  }
  function isFirstOfGroup(f: ConfigField, i: number): boolean {
    const h = heading(f);
    if (!h) return false;
    return i === 0 || heading(visible[i - 1]) !== h;
  }

  function toggle(f: ConfigField) {
    onChange(f.Key, (values[f.Key] === "true") ? "false" : "true");
  }
</script>

<div class="space-y-3">
  {#each visible as f, i (f.Key)}
    {#if isFirstOfGroup(f, i)}
      <p class="pt-1 text-xs font-semibold text-black-900 dark:text-white-100">{heading(f)}</p>
    {/if}

    {#if isToggle(f)}
      <div class="flex items-center justify-between gap-4">
        <div class="min-w-0">
          <p class="text-sm font-medium text-black-900 dark:text-white-100">{f.Description || f.Key}</p>
        </div>
        <button
          type="button"
          role="switch"
          aria-checked={values[f.Key] === "true"}
          aria-label={f.Description || f.Key}
          onclick={() => toggle(f)}
          class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition-colors {values[f.Key] === 'true' ? 'bg-green-500' : 'bg-white-400 dark:bg-navy-600'}"
        >
          <span class="inline-block h-5 w-5 transform rounded-full bg-white-100 shadow transition-transform {values[f.Key] === 'true' ? 'translate-x-5' : 'translate-x-0.5'}"></span>
        </button>
      </div>
    {:else if f.Type === "dropdown"}
      <div>
        {#if f.Description}
          <p class="mb-1.5 text-[11px] font-medium text-black-800 dark:text-black-600">{f.Description}</p>
        {/if}
        <Select
          value={values[f.Key] ?? ""}
          options={dropdownOptions(f)}
          onChange={(v: string) => onChange(f.Key, v)}
        />
      </div>
    {:else}
      <div>
        {#if f.Description}
          <p class="mb-1.5 text-[11px] font-medium text-black-800 dark:text-black-600">{f.Description}</p>
        {/if}
        <input
          type="text"
          value={values[f.Key] ?? ""}
          oninput={(e) => onChange(f.Key, (e.target as HTMLInputElement).value)}
          class="w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-3 py-2 text-sm text-black-900 dark:text-white-100 outline-none focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800 transition-colors"
        />
      </div>
    {/if}
  {/each}
</div>
