<script lang="ts">
  /* One config/input field card: key, type, default, description, optional
     dropdown options, and Required/Secret toggles. Mirrors the legacy
     custom_review.js fieldRow(). Parent owns the field object; every edit
     calls onChange so the live preview re-serializes. */
  import { TextInput, Select } from "@wick-fe/common-ui";
  import { WIDGETS } from "./draft.js";
  import type { DraftField } from "$lib/types.js";

  type Props = {
    field: DraftField;
    onChange: () => void;
    onRemove: () => void;
  };
  let { field, onChange, onRemove }: Props = $props();

  function set<K extends keyof DraftField>(k: K, v: DraftField[K]) {
    field[k] = v;
    onChange();
  }

  function setWidget(v: string) {
    field.widget = v;
    if (v === "secret") field.secret = true;
    onChange();
  }

  let secretBg = $derived(field.secret || field.widget === "secret");
</script>

<div class="rounded-xl border border-white-300 dark:border-navy-600 p-3 {secretBg ? 'bg-cau-100/60 dark:bg-cau-100/10' : 'bg-white-100 dark:bg-navy-700'}">
  <div class="flex items-start gap-2">
    <div class="grid min-w-0 flex-1 grid-cols-1 gap-3 sm:grid-cols-2">
      <div class="min-w-0">
        <span class="mb-1 block text-[11px] font-medium text-black-800 dark:text-black-600">Key</span>
        <TextInput value={field.key} onChange={(v) => set("key", v)} placeholder="field_key" ariaLabel="Field key" class="font-mono" />
      </div>
      <div class="min-w-0">
        <span class="mb-1 block text-[11px] font-medium text-black-800 dark:text-black-600">Type</span>
        <Select value={field.widget} options={WIDGETS} onChange={setWidget} />
      </div>
      <div class="min-w-0">
        <span class="mb-1 block text-[11px] font-medium text-black-800 dark:text-black-600">Default</span>
        <TextInput
          value={field.default}
          onChange={(v) => set("default", v)}
          type={field.widget === "secret" ? "password" : "text"}
          placeholder="default value"
          ariaLabel="Field default"
        />
      </div>
      <div class="min-w-0">
        <span class="mb-1 block text-[11px] font-medium text-black-800 dark:text-black-600">Description</span>
        <TextInput value={field.desc} onChange={(v) => set("desc", v)} placeholder="what this field is for" ariaLabel="Field description" />
      </div>
    </div>
    <button
      type="button"
      class="flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-lg text-black-700 transition-colors hover:bg-neg-100 hover:text-neg-400 dark:text-black-600"
      title="Remove field"
      aria-label="Remove field"
      onclick={onRemove}
    >
      <svg class="h-4 w-4" fill="none" stroke="currentColor" stroke-width="2" viewBox="0 0 24 24"><path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2m3 0v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" stroke-linecap="round" stroke-linejoin="round"/><path d="M10 11v6M14 11v6" stroke-linecap="round"/></svg>
    </button>
  </div>

  {#if field.widget === "dropdown"}
    <div class="mt-3">
      <span class="mb-1 block text-[11px] font-medium text-black-800 dark:text-black-600">Options</span>
      <TextInput value={field.options} onChange={(v) => set("options", v)} placeholder="options: a|b|c" ariaLabel="Field options" class="font-mono" />
    </div>
  {/if}

  <div class="mt-3 flex flex-wrap items-center gap-x-6 gap-y-2 border-t border-white-300 pt-3 dark:border-navy-600">
    <label class="flex cursor-pointer select-none items-center gap-2">
      <input type="checkbox" class="accent-green-500" checked={field.required} onchange={(e) => set("required", (e.target as HTMLInputElement).checked)} aria-label="Required" />
      <span class="text-xs font-medium text-black-800 dark:text-black-600">Required</span>
    </label>
    <label class="flex cursor-pointer select-none items-center gap-2">
      <input type="checkbox" class="accent-green-500" checked={field.secret} onchange={(e) => set("secret", (e.target as HTMLInputElement).checked)} aria-label="Secret" />
      <span class="text-xs font-medium text-black-800 dark:text-black-600">Secret</span>
    </label>
  </div>
</div>
