<script lang="ts">
  /* Add/remove key-value header rows with a per-row "secret" toggle.
     Controlled: the parent owns `rows`; every edit/add/remove calls
     onChange with the full array. Mirrors the legacy renderHeaders +
     add-row handlers in custom_mcp_form.js. Secret rows mask the value
     input and encrypt at rest server-side. */
  import type { McpHeaderRow } from "$lib/types.js";

  type Props = {
    rows: McpHeaderRow[];
    onChange: (rows: McpHeaderRow[]) => void;
    addLabel?: string;
    defaultSecret?: boolean;
  };
  let { rows, onChange, addLabel = "+ Add row", defaultSecret = false }: Props = $props();

  const inputClass =
    "w-full rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1.5 font-mono text-xs text-black-900 dark:text-white-100 outline-none focus:border-green-500";

  function setRow(index: number, patch: Partial<McpHeaderRow>) {
    onChange((rows ?? []).map((r, i) => (i === index ? { ...r, ...patch } : r)));
  }

  function addRow() {
    onChange([...(rows ?? []), { key: "", value: "", secret: defaultSecret }]);
  }

  function removeRow(index: number) {
    onChange((rows ?? []).filter((_, i) => i !== index));
  }
</script>

<div class="space-y-2">
  {#if (rows ?? []).length === 0}
    <p class="text-[11px] text-black-700 dark:text-black-600">No rows yet.</p>
  {/if}
  {#each rows ?? [] as r, index (index)}
    <div class="grid grid-cols-12 items-center gap-2 {r.secret ? 'rounded-lg bg-cau-100 dark:bg-cau-100/10 p-1' : ''}">
      <input
        class="{inputClass} col-span-4"
        aria-label="Header name"
        placeholder="X-API-Key"
        value={r.key ?? ""}
        oninput={(e) => setRow(index, { key: (e.target as HTMLInputElement).value })}
      />
      <input
        class="{inputClass} col-span-5"
        aria-label="Header value"
        type={r.secret ? "password" : "text"}
        placeholder="value"
        value={r.value ?? ""}
        oninput={(e) => setRow(index, { value: (e.target as HTMLInputElement).value })}
      />
      <label class="col-span-2 flex cursor-pointer items-center gap-1 text-[11px] text-black-800 dark:text-black-600">
        <input
          type="checkbox"
          class="accent-green-500"
          aria-label="Secret"
          checked={!!r.secret}
          onchange={(e) => setRow(index, { secret: (e.target as HTMLInputElement).checked })}
        />
        Sec
      </label>
      <button
        type="button"
        class="col-span-1 text-xs text-black-700 hover:text-neg-400"
        aria-label="Remove header"
        onclick={() => removeRow(index)}
      >✕</button>
    </div>
  {/each}
  <button
    type="button"
    class="rounded-lg border border-white-400 dark:border-navy-600 px-2.5 py-1 text-[11px] font-medium text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600"
    onclick={addRow}
  >{addLabel}</button>
</div>
