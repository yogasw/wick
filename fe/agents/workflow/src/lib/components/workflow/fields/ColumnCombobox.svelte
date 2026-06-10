<script lang="ts">
  // Column-name combobox — typeahead input with an absolute-positioned
  // dropdown that shows every matching column (filtered by current
  // input). Replaces the native <datalist> in datatable inspectors
  // because the browser default only renders one suggestion offset to
  // the side and looks broken in dark mode. Same UX as v1's
  // makeColCombobox in inspector.js.

  type Props = {
    value: string;
    columns: string[];
    placeholder?: string;
    onChange: (next: string) => void;
  };
  let { value, columns, placeholder = "column", onChange }: Props = $props();

  let inputEl: HTMLInputElement | undefined = $state();
  let open = $state(false);
  let highlight = $state(-1);

  const filtered = $derived.by(() => {
    const q = (value ?? "").trim().toLowerCase();
    if (!q) return columns;
    return columns.filter((c) => c.toLowerCase().includes(q));
  });

  function commit(next: string) {
    onChange(next);
    open = false;
    highlight = -1;
  }

  function onInput(e: Event) {
    const v = (e.target as HTMLInputElement).value;
    onChange(v);
    open = true;
    highlight = -1;
  }

  function onFocus() {
    if (columns.length > 0) open = true;
  }

  function onBlur() {
    // Delay so a click on a dropdown item lands before we hide it.
    setTimeout(() => (open = false), 120);
  }

  function onKeydown(e: KeyboardEvent) {
    if (!open && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
      open = true;
      return;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      highlight = Math.min(highlight + 1, filtered.length - 1);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      highlight = Math.max(highlight - 1, 0);
    } else if (e.key === "Enter" && highlight >= 0) {
      e.preventDefault();
      commit(filtered[highlight]);
    } else if (e.key === "Escape") {
      open = false;
      highlight = -1;
    }
  }
</script>

<div class="relative w-full min-w-0">
  <input
    bind:this={inputEl}
    type="text"
    class="w-full rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 font-mono text-[12px]"
    {placeholder}
    {value}
    oninput={onInput}
    onfocus={onFocus}
    onblur={onBlur}
    onkeydown={onKeydown}
    autocomplete="off"
  />
  {#if open && filtered.length > 0}
    <div
      class="absolute z-50 left-0 right-0 mt-1 max-h-48 overflow-y-auto rounded border border-slate-200 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg"
    >
      {#each filtered as name, i}
        <button
          type="button"
          class="block w-full px-3 py-1.5 text-left font-mono text-[12px] hover:bg-emerald-50 dark:hover:bg-navy-600 dark:bg-navy-700"
          class:bg-emerald-50={highlight === i}
          class:bg-navy-700={highlight === i}
          onmousedown={(e) => { e.preventDefault(); commit(name); }}
        >{name}</button>
      {/each}
    </div>
  {/if}
</div>
