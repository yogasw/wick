<script lang="ts" module>
  let seq = 0;
</script>

<script lang="ts">
  // Themed dropdown: a button trigger + a listbox popover drawn by us, not
  // the OS. The native <select> popup ignored the theme (blue OS
  // highlight), could grow wider than its field and paint over the panel
  // beside it, and had no room for a description line.
  //
  // The popover is portalled to <body> and positioned `fixed` from the
  // trigger's rect, so a parent's overflow/transform never clips it; it is
  // as wide as the trigger (min 12rem, never wider than the viewport),
  // flips upward when there is no room below, and follows scroll/resize.
  //
  // Props are a superset of the old <select> wrapper, so existing callers
  // keep working unchanged.
  import { tick, onDestroy } from "svelte";
  import type { SelectOption } from "./select-types.js";

  type Props = {
    value: string;
    options: SelectOption[];
    onChange: (v: string) => void;
    placeholder?: string;
    disabled?: boolean;
    class?: string;
    size?: "sm" | "md";
    /** "boxed" (default) is the bordered field; "minimal" is a borderless
        text+chevron control for toolbars. */
    variant?: "boxed" | "minimal";
    /** id of the trigger, for <label for>. */
    id?: string;
    /** When set, a hidden input carries the value for native form posts. */
    name?: string;
    /** Filter box at the top of the list. Default: on when > 8 options. */
    searchable?: boolean;
    /** Accessible name when no <label for> points at the trigger. */
    ariaLabel?: string;
  };

  let {
    value,
    options,
    onChange,
    placeholder,
    disabled = false,
    class: extraClass = "",
    size = "md",
    variant = "boxed",
    id,
    name,
    searchable,
    ariaLabel,
  }: Props = $props();

  type Norm = { label: string; value: string; description: string; badge: string; disabled: boolean; placeholder: boolean };

  const uid = `wick-select-${++seq}`;
  const triggerId = $derived(id ?? `${uid}-trigger`);
  const listId = `${uid}-list`;

  const items = $derived.by<Norm[]>(() => {
    const out: Norm[] = [];
    if (placeholder) {
      out.push({ label: placeholder, value: "", description: "", badge: "", disabled: false, placeholder: true });
    }
    for (const o of options) {
      if (typeof o === "string") {
        out.push({ label: o, value: o, description: "", badge: "", disabled: false, placeholder: false });
      } else {
        out.push({
          label: o.label,
          value: o.value,
          description: o.description ?? "",
          badge: o.badge ?? "",
          disabled: o.disabled ?? false,
          placeholder: false,
        });
      }
    }
    return out;
  });

  const isSearchable = $derived(searchable ?? options.length > 8);
  let open = $state(false);
  let query = $state("");
  let active = $state(-1);
  let trigger = $state<HTMLButtonElement | null>(null);
  let popover = $state<HTMLDivElement | null>(null);
  let searchEl = $state<HTMLInputElement | null>(null);
  let pos = $state({ left: 0, width: 0, top: 0 as number | null, bottom: null as number | null, maxHeight: 288 });

  const visible = $derived.by(() => {
    const q = query.trim().toLowerCase();
    if (!q) return items;
    return items.filter(
      (it) => it.label.toLowerCase().includes(q) || it.description.toLowerCase().includes(q) || it.value.toLowerCase().includes(q),
    );
  });

  const selected = $derived(items.find((it) => it.value === value && !(it.placeholder && value !== "")) ?? null);
  const showPlaceholder = $derived(!selected || selected.placeholder);

  const sizes = { sm: "px-2 py-1 text-xs", md: "px-3 py-2 text-sm" };
  const variants = {
    boxed:
      "rounded-lg border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-800 focus:border-green-500 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800",
    minimal:
      "rounded-md border-0 bg-transparent font-medium hover:bg-white-200 dark:hover:bg-navy-700 focus:ring-2 focus:ring-green-200 dark:focus:ring-green-800",
  };

  // Viewport math: width follows the trigger (min 12rem = 192px), clamped
  // to the viewport with an 8px gutter; flip up when below is too short.
  function place(): void {
    if (!trigger) return;
    const r = trigger.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const gutter = 8;
    const width = Math.min(Math.max(r.width, 192), vw - gutter * 2);
    const left = Math.min(Math.max(r.left, gutter), vw - gutter - width);
    const below = vh - r.bottom - gutter;
    const above = r.top - gutter;
    const want = 288;
    if (below < Math.min(want, 160) && above > below) {
      pos = { left, width, top: null, bottom: vh - r.top + 4, maxHeight: Math.min(want, above - 4) };
    } else {
      pos = { left, width, top: r.bottom + 4, bottom: null, maxHeight: Math.max(96, Math.min(want, below - 4)) };
    }
  }

  function firstEnabled(from: number, dir: 1 | -1): number {
    const n = visible.length;
    for (let i = 0, j = from; i < n; i++, j += dir) {
      if (j < 0 || j >= n) break;
      if (!visible[j].disabled) return j;
    }
    return -1;
  }

  async function openList(): Promise<void> {
    if (disabled || open) return;
    query = "";
    open = true;
    place();
    const cur = visible.findIndex((it) => it.value === value);
    active = cur >= 0 && !visible[cur].disabled ? cur : firstEnabled(0, 1);
    await tick();
    place();
    scrollActive();
    if (isSearchable) searchEl?.focus();
  }

  function closeList(refocus: boolean): void {
    if (!open) return;
    open = false;
    query = "";
    if (refocus) trigger?.focus();
  }

  function choose(i: number): void {
    const it = visible[i];
    if (!it || it.disabled) return;
    closeList(true);
    if (it.value !== value) onChange(it.value);
  }

  function move(delta: 1 | -1): void {
    const start = active < 0 ? (delta === 1 ? 0 : visible.length - 1) : active + delta;
    const next = firstEnabled(start, delta);
    if (next >= 0) active = next;
    scrollActive();
  }

  function scrollActive(): void {
    void tick().then(() => {
      const el = popover?.querySelector<HTMLElement>(`[data-index="${active}"]`);
      el?.scrollIntoView?.({ block: "nearest" });
    });
  }

  // Type-ahead: letters typed within 600ms form a prefix; jump to the next
  // enabled option starting with it.
  let typed = "";
  let typedAt = 0;
  function typeAhead(ch: string): void {
    const now = Date.now();
    typed = now - typedAt > 600 ? ch : typed + ch;
    typedAt = now;
    const q = typed.toLowerCase();
    const n = visible.length;
    const from = typed.length === 1 ? active + 1 : Math.max(active, 0);
    for (let k = 0; k < n; k++) {
      const i = (from + k) % n;
      if (!visible[i].disabled && visible[i].label.toLowerCase().startsWith(q)) {
        active = i;
        scrollActive();
        return;
      }
    }
  }

  function onTriggerKey(e: KeyboardEvent): void {
    if (disabled) return;
    if (!open) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        void openList();
      }
      return;
    }
    onListKey(e);
  }

  function onListKey(e: KeyboardEvent): void {
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        move(1);
        return;
      case "ArrowUp":
        e.preventDefault();
        move(-1);
        return;
      case "Home":
        e.preventDefault();
        active = firstEnabled(0, 1);
        scrollActive();
        return;
      case "End":
        e.preventDefault();
        active = firstEnabled(visible.length - 1, -1);
        scrollActive();
        return;
      case "Enter":
        e.preventDefault();
        if (active >= 0) choose(active);
        return;
      case " ":
        if (e.target === searchEl) return; // a space in the filter is text
        e.preventDefault();
        if (active >= 0) choose(active);
        return;
      case "Escape":
        e.preventDefault();
        e.stopPropagation(); // don't also close an enclosing modal
        closeList(true);
        return;
      case "Tab":
        closeList(false);
        return;
    }
    if (e.target !== searchEl && e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      typeAhead(e.key);
    }
  }

  function onSearchInput(): void {
    active = firstEnabled(0, 1);
  }

  // Outside click + scroll/resize tracking, only while open.
  function onDocPointer(e: PointerEvent | MouseEvent): void {
    const t = e.target as Node | null;
    if (!t) return;
    if (trigger?.contains(t) || popover?.contains(t)) return;
    closeList(false);
  }
  function onViewport(): void {
    if (open) place();
  }

  $effect(() => {
    if (!open) return;
    document.addEventListener("mousedown", onDocPointer, true);
    window.addEventListener("scroll", onViewport, true);
    window.addEventListener("resize", onViewport);
    return () => {
      document.removeEventListener("mousedown", onDocPointer, true);
      window.removeEventListener("scroll", onViewport, true);
      window.removeEventListener("resize", onViewport);
    };
  });

  // Move the popover node to <body> so no ancestor overflow/transform can
  // clip or offset it.
  function portal(node: HTMLElement) {
    document.body.appendChild(node);
    return {
      destroy() {
        node.remove();
      },
    };
  }

  onDestroy(() => {
    open = false;
  });
</script>

<div class="relative {extraClass}" data-testid="wick-select">
  {#if name}
    <input type="hidden" {name} {value} />
  {/if}
  <button
    bind:this={trigger}
    id={triggerId}
    type="button"
    data-testid="wick-select-trigger"
    class="flex w-full items-center gap-2 text-left text-black-900 dark:text-white-100 outline-none transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed {sizes[size]} {variants[variant]}"
    aria-haspopup="listbox"
    aria-expanded={open}
    aria-controls={open ? listId : undefined}
    aria-label={ariaLabel}
    aria-activedescendant={open && !isSearchable && active >= 0 ? `${uid}-opt-${active}` : undefined}
    {disabled}
    onclick={() => (open ? closeList(false) : void openList())}
    onkeydown={onTriggerKey}
  >
    <span class="min-w-0 flex-1 truncate {showPlaceholder ? 'text-black-700 dark:text-black-600' : ''}">
      {selected ? selected.label : (placeholder ?? value)}
    </span>
    {#if selected?.badge}
      <span class="shrink-0 rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[11px] font-medium text-black-800 dark:text-black-600">{selected.badge}</span>
    {/if}
    <svg class="shrink-0 text-black-700 dark:text-black-600 transition-transform {open ? 'rotate-180' : ''}" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <polyline points="6 9 12 15 18 9" />
    </svg>
  </button>
</div>

{#if open}
  <div
    use:portal
    bind:this={popover}
    data-testid="wick-select-popover"
    class="fixed z-[1000] flex flex-col overflow-hidden rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 shadow-lg"
    style="left:{pos.left}px;width:{pos.width}px;{pos.top !== null ? `top:${pos.top}px;` : ''}{pos.bottom !== null ? `bottom:${pos.bottom}px;` : ''}max-height:{pos.maxHeight}px"
  >
    {#if isSearchable}
      <div class="border-b border-white-300 dark:border-navy-600 p-2">
        <input
          bind:this={searchEl}
          bind:value={query}
          oninput={onSearchInput}
          onkeydown={onListKey}
          type="text"
          data-testid="wick-select-search"
          placeholder="Search…"
          aria-controls={listId}
          aria-activedescendant={active >= 0 ? `${uid}-opt-${active}` : undefined}
          class="w-full rounded-md border border-white-400 dark:border-navy-600 bg-white-100 dark:bg-navy-700 px-2 py-1 text-sm text-black-900 dark:text-white-100 outline-none focus:border-green-500"
        />
      </div>
    {/if}
    <ul
      id={listId}
      role="listbox"
      tabindex="-1"
      data-testid="wick-select-listbox"
      aria-labelledby={triggerId}
      class="min-h-0 flex-1 overflow-y-auto py-1"
    >
      {#each visible as it, i (it.value + ":" + i)}
        {@const isSel = it.value === value && !(it.placeholder && value !== "")}
        <li
          id="{uid}-opt-{i}"
          role="option"
          data-index={i}
          data-value={it.value}
          aria-selected={isSel}
          aria-disabled={it.disabled || undefined}
          class="mx-1 flex cursor-pointer items-start gap-2 rounded-md px-2 py-2 text-sm
                 {it.disabled ? 'cursor-not-allowed opacity-50' : ''}
                 {i === active && !it.disabled ? 'bg-white-200 dark:bg-navy-600' : ''}"
          onmouseenter={() => { if (!it.disabled) active = i; }}
          onmousedown={(e) => e.preventDefault()}
          onclick={() => choose(i)}
        >
          <span class="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center text-green-500">
            {#if isSel}
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="20 6 9 17 4 12" /></svg>
            {/if}
          </span>
          <span class="min-w-0 flex-1">
            <span class="block truncate font-medium {it.placeholder ? 'text-black-700 dark:text-black-600' : 'text-black-900 dark:text-white-100'}">{it.label}</span>
            {#if it.description}
              <span class="mt-0.5 block text-xs text-black-800 dark:text-black-600 line-clamp-2">{it.description}</span>
            {/if}
          </span>
          {#if it.badge}
            <span class="mt-0.5 shrink-0 rounded bg-white-300 dark:bg-navy-600 px-1.5 py-0.5 text-[11px] font-medium text-black-800 dark:text-black-600">{it.badge}</span>
          {/if}
        </li>
      {:else}
        <li class="px-3 py-2 text-xs text-black-700 dark:text-black-600" role="presentation">No matches</li>
      {/each}
    </ul>
  </div>
{/if}
