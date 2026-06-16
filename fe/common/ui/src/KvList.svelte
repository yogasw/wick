<script lang="ts">
  /* Generic, controlled key-value / multi-column row editor. Parent owns
     `rows`; every edit/add/remove calls onChange with the full rows array
     (callers filter empty rows on serialize).

     Two override levels:
     - `cell` snippet replaces a single cell's value editor (columns stay laid
       out side by side). Used by providers.
     - `row` snippet replaces the WHOLE row body — the caller renders its own
       layout (e.g. workflow's key input above a full-width ArgField) and uses
       the supplied `remove`/`set`. KvList still owns iteration, the row
       container, the empty state, and the add button.

     Responsive: cell-mode rows stack vertically on mobile (each cell full
     width with a small column label) and lay out as inline columns on >=sm.
     row-mode is whatever the caller's snippet renders (stacked container).
     Storage shape + styling mirror the templ KVList (design-system). */
  import type { Snippet } from "svelte";

  type Row = Record<string, string>;
  type CellArgs = {
    row: Row;
    index: number;
    col: string;
    value: string;
    set: (v: string) => void;
  };
  type RowArgs = {
    row: Row;
    index: number;
    remove: () => void;
    set: (col: string, value: string) => void;
  };

  type Props = {
    columns: string[];
    rows: Row[];
    onChange: (rows: Row[]) => void;
    label?: string;
    helper?: string;
    placeholders?: Record<string, string>;
    addLabel?: string;
    emptyText?: string;
    showHeader?: boolean;
    showAdd?: boolean;
    onCommit?: () => void;
    cell?: Snippet<[CellArgs]>;
    row?: Snippet<[RowArgs]>;
  };

  let {
    columns,
    rows,
    onChange,
    label,
    helper,
    placeholders,
    addLabel = "+ Add row",
    emptyText,
    showHeader,
    showAdd = true,
    onCommit,
    cell,
    row,
  }: Props = $props();

  const showColLabels = $derived(showHeader ?? (columns.length > 1));

  const inputClass =
    "w-full rounded border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 px-2 py-1 text-xs font-mono text-black-900 dark:text-white-100 outline-none focus:border-green-500 focus:ring-1 focus:ring-green-200 dark:focus:ring-green-800";

  function setCell(index: number, col: string, value: string) {
    onChange((rows ?? []).map((r, i) => (i === index ? { ...r, [col]: value } : r)));
  }

  function addRow() {
    const blank: Row = {};
    for (const c of columns) {
      blank[c] = "";
    }
    onChange([...(rows ?? []), blank]);
  }

  function removeRow(index: number) {
    onChange((rows ?? []).filter((_, i) => i !== index));
    onCommit?.();
  }
</script>

<div class="space-y-2">
  {#if label}
    <span class="text-xs font-medium">{label}</span>
  {/if}
  {#if helper}
    <span class="block text-[11px] text-black-700 dark:text-black-600">{helper}</span>
  {/if}
  {#if (rows ?? []).length === 0 && emptyText}
    <div class="rounded-lg border border-white-300 dark:border-navy-600 px-4 py-5 text-center text-xs text-black-700 dark:text-black-600">{emptyText}</div>
  {/if}
  {#each rows ?? [] as r, index (index)}
    {#if row}
      <div class="space-y-1 rounded-lg border border-white-300 dark:border-navy-600 p-2">
        {@render row({ row: r, index, remove: () => removeRow(index), set: (col, value) => setCell(index, col, value) })}
      </div>
    {:else}
      <div class="rounded-lg border border-white-300 dark:border-navy-600 p-2">
        <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
          {#each columns as col}
            <div class="min-w-0 flex-1 space-y-1">
              {#if showColLabels}
                <span class="block text-[10px] font-medium capitalize text-black-700 dark:text-black-600">{col}</span>
              {/if}
              {#if cell}
                {@render cell({ row: r, index, col, value: r[col] ?? "", set: (v) => setCell(index, col, v) })}
              {:else}
                <input
                  type="text"
                  class={inputClass}
                  aria-label={col}
                  placeholder={placeholders?.[col] ?? ""}
                  value={r[col] ?? ""}
                  oninput={(e) => setCell(index, col, (e.target as HTMLInputElement).value)}
                  onblur={() => onCommit?.()}
                />
              {/if}
            </div>
          {/each}
          <button
            type="button"
            class="shrink-0 self-end px-2 text-base leading-none text-black-700 dark:text-black-600 hover:text-neg-400 sm:self-auto"
            aria-label="Remove row"
            onclick={() => removeRow(index)}
          >×</button>
        </div>
      </div>
    {/if}
  {/each}
  {#if showAdd}
    <button
      type="button"
      class="w-full rounded-lg border border-dashed border-white-400 dark:border-navy-600 px-3 py-2 text-xs font-medium text-black-800 dark:text-black-600 hover:border-green-400 hover:text-green-600 sm:w-auto"
      onclick={addRow}
    >{addLabel}</button>
  {/if}
</div>
