<script lang="ts">
  /* A compact picker popup — the target of `/provider` and `/project`. It floats
     directly above the composer (like Claude's model picker), not a centered
     full-screen modal. Anchored to the nearest positioned ancestor via
     `bottom-full`, so the parent must be `relative`. Clicking an option selects
     it; Esc or a click outside closes. */
  type Item = { id: string | null; label: string; hint?: string; current?: boolean };

  type Props = {
    open: boolean;
    title: string;
    items: Item[];
    onSelect: (id: string | null) => void;
    onClose: () => void;
  };

  let { open, title, items, onSelect, onClose }: Props = $props();

  let el: HTMLDivElement | undefined = $state();
  let highlighted = $state(0);

  // Start on the first selectable (non-current) row each time it opens.
  $effect(() => {
    if (!open) return;
    const first = items.findIndex((it) => !it.current);
    highlighted = first === -1 ? 0 : first;
  });

  // Move the highlight to the next/prev selectable row, wrapping around.
  function move(dir: number) {
    const n = items.length;
    if (!n) return;
    let i = highlighted;
    for (let k = 0; k < n; k++) {
      i = (i + dir + n) % n;
      if (!items[i]?.current) { highlighted = i; return; }
    }
  }

  $effect(() => {
    if (!open) return;
    // Capture phase so the composer's textarea (which has focus) doesn't also
    // act on these keys — stopPropagation keeps Enter from sending the message.
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") { e.preventDefault(); e.stopPropagation(); onClose(); return; }
      if (e.key === "ArrowDown") { e.preventDefault(); e.stopPropagation(); move(1); return; }
      if (e.key === "ArrowUp") { e.preventDefault(); e.stopPropagation(); move(-1); return; }
      if (e.key === "Enter" || e.key === "Tab") { e.preventDefault(); e.stopPropagation(); pick(items[highlighted]); return; }
    }
    function onDown(e: MouseEvent) {
      if (el && !el.contains(e.target as Node)) onClose();
    }
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("mousedown", onDown, true);
    return () => {
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("mousedown", onDown, true);
    };
  });

  function pick(item: Item | undefined) {
    if (!item || item.current) return; // already active (button is disabled anyway)
    onSelect(item.id);
    onClose();
  }
</script>

{#if open}
  <div
    bind:this={el}
    data-switch-popup
    class="absolute bottom-full left-0 right-0 mb-2 z-30 rounded-xl border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg overflow-hidden"
  >
    <div class="px-3 py-2 border-b border-white-300 dark:border-navy-600 text-xs font-semibold text-black-900 dark:text-white-100">{title}</div>
    <div class="max-h-64 overflow-y-auto py-1" role="listbox" aria-label={title}>
      {#each items as item, i (item.id ?? "__none__")}
        <button
          type="button"
          role="option"
          aria-selected={i === highlighted}
          disabled={item.current}
          onclick={() => pick(item)}
          onmouseenter={() => { if (!item.current) highlighted = i; }}
          class="flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm transition-colors {item.current
            ? 'text-black-700 dark:text-black-600 cursor-default'
            : i === highlighted
              ? 'bg-green-500/10 text-black-900 dark:text-white-100'
              : 'text-black-900 dark:text-white-100 hover:bg-white-200 dark:hover:bg-navy-700'}"
        >
          <span class="flex items-center gap-2 min-w-0">
            <span class="truncate">{item.label}</span>
            {#if item.hint}
              <span class="shrink-0 text-xs text-black-600 dark:text-black-700">{item.hint}</span>
            {/if}
          </span>
          {#if item.current}
            <span class="shrink-0 text-xs text-green-600 dark:text-green-400">current</span>
          {/if}
        </button>
      {/each}
    </div>
  </div>
{/if}
