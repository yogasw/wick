<script lang="ts">
  /* kvlist config block. Reuses @wick-fe/common-ui KvList for the row editor;
     columns with col_options render a themed <select> via the cell snippet,
     matching the legacy buildColSelect behaviour. Value is a JSON array of
     column-keyed objects — serialized on every change, empty rows dropped.

     Save timing: onChange persists with a debounce (free-text typing), while
     KvList's onCommit (blur + row removal) persists immediately. */
  import { untrack } from "svelte";
  import { KvList, Select } from "@wick-fe/common-ui";
  import type { ConfigField } from "$lib/types.js";
  import { kvColumns, parseRows, parseColOpts } from "./options.js";

  type Row = Record<string, string>;
  type Props = {
    field: ConfigField;
    onSave: (json: string) => void;
    onCommit: () => void;
  };
  let { field, onSave, onCommit }: Props = $props();

  let columns = $derived(kvColumns(field));
  /* The field value is the persisted JSON; this component is the source of
     truth for in-flight edits. Seed once from the initial prop — the parent
     remounts (route key) on any external reload, so no resync is needed. */
  let rows = $state<Row[]>(untrack(() => parseRows(field.value)));

  function serialize(next: Row[]): string {
    const cleaned = next.filter((r) => Object.values(r).some((v) => (v ?? "").trim() !== ""));
    return JSON.stringify(cleaned);
  }

  function update(next: Row[]) {
    rows = next;
    onSave(serialize(next));
  }

  function selectOpts(col: string): { label: string; value: string }[] {
    return [{ label: "(no restriction)", value: "" }, ...parseColOpts(field.col_options?.[col] ?? "")];
  }
</script>

<KvList
  {columns}
  rows={rows}
  showHeader
  emptyText="No rows yet — click + Add Row to start"
  addLabel="+ Add Row"
  onChange={update}
  onCommit={onCommit}
>
  {#snippet cell({ col, value: cellValue, set })}
    {#if field.col_options?.[col]}
      <Select size="sm" value={cellValue} options={selectOpts(col)} onChange={set} />
    {:else}
      <input
        type="text"
        class="w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1 text-xs font-mono text-black-900 dark:text-white-100 outline-none focus:border-green-500"
        aria-label={col}
        placeholder={col}
        value={cellValue}
        oninput={(e) => set((e.target as HTMLInputElement).value)}
      />
    {/if}
  {/snippet}
</KvList>
