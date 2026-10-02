<script lang="ts" module>
  /* Module-scoped open token: only one KebabMenu is open at a time across
     the whole app. Opening one bumps the token, which every other instance
     watches to close itself — no parent coordination needed. */
  let currentOwner = $state(0);
  let seq = 0;
  function claimOpen(): number {
    seq += 1;
    currentOwner = seq;
    return seq;
  }
  function releaseOpen() {
    currentOwner = 0;
  }
</script>

<script lang="ts">
  /* Reusable 3-dot (⋮) actions menu. The popup is rendered position:fixed,
     anchored to the trigger via getBoundingClientRect, so it escapes any
     parent overflow/stacking context and always paints above sibling rows —
     the bug a plain absolute z-index can't reliably win. Opening one menu
     closes every other (module open-token). Closes on outside click, Escape,
     scroll, or resize. Items drive the rows; a `danger` item renders red. */
  type Item = {
    label: string;
    onclick?: () => void;
    danger?: boolean;
    disabled?: boolean;
    /* A second line under the label, monospace and truncated. It is what
       makes "Copy path" answerable without clicking: the row shows the value
       it would copy, so nothing has to be copied to find out what it was. */
    detail?: string;
    /* A plain second line under the label, for what the row is about rather
       than a value (detail is for values: monospace, elided by the caller). */
    hint?: string;
    /* Draw a rule above this row, to set apart a group such as the
       destructive actions at the bottom. */
    divider?: boolean;
    /* Rows to swap the popup over to, with a back row at the top. Deliberately
       NOT a second floating layer: the panel this lives in is ~300px wide, so
       a flyout would open off-screen half the time. */
    submenu?: Item[];
    /* Keep the popup open after the click. For rows you may want twice (copy
       one flavour of a path, then another) and for rows that report a result
       in place. */
    keepOpen?: boolean;
    /* Shown in place of the label for ~1.2s after the click. */
    doneLabel?: string;
  };
  type Props = {
    items: Item[];
    ariaLabel?: string;
    /* Menu width in px (Tailwind w-* isn't available on the fixed layer). */
    width?: number;
    /* Trigger size. "md" (default, 32px) suits list rows and card headers.
       "sm" (24px) is for dense toolbars whose other controls are ~16px —
       a 32px trigger there is the tallest flex child and silently makes the
       whole bar 50% taller. */
    size?: "sm" | "md";
  };
  let { items, ariaLabel = "Actions", width = 176, size = "md" }: Props = $props();

  const triggerSize = $derived(size === "sm" ? "h-6 w-6" : "h-8 w-8");
  const iconSize = $derived(size === "sm" ? "h-4 w-4" : "h-5 w-5");

  let myId = $state(0);
  let triggerEl = $state<HTMLButtonElement | null>(null);
  let pos = $state<{ top: number; left: number } | null>(null);
  /* Which submenu the popup is showing, null = the top level. Reset on close
     so reopening never resumes somewhere the user did not leave it. */
  let subOpen = $state<Item | null>(null);
  let doneAt = $state<string>("");

  const isOpen = $derived(myId !== 0 && currentOwner === myId);
  const rows = $derived(subOpen?.submenu ?? items);

  /* Estimated row height (px) used to decide flip direction before the menu
     has rendered. Matches the py-2 text-sm rows below. */
  const ROW_H = 36;
  const GAP = 4;
  const PAD = 8;

  function place() {
    if (!triggerEl) return;
    const r = triggerEl.getBoundingClientRect();
    // Right-align the menu under the trigger; clamp into the viewport.
    let left = r.right - width;
    if (left < PAD) left = PAD;
    if (left + width > window.innerWidth - PAD) left = window.innerWidth - PAD - width;

    // Prefer below the trigger, but flip above when it would overflow the
    // viewport bottom — otherwise the last rows get clipped / sit under the
    // following list row. Use the rendered height once available, else estimate.
    const menuH = menuEl?.offsetHeight ?? rows.length * ROW_H + PAD;
    const below = r.bottom + GAP;
    const fitsBelow = below + menuH <= window.innerHeight - PAD;
    let top = fitsBelow ? below : r.top - GAP - menuH;
    // If it fits neither way, clamp to the viewport so it stays fully on-screen.
    if (top < PAD) top = PAD;
    if (top + menuH > window.innerHeight - PAD) {
      top = Math.max(PAD, window.innerHeight - PAD - menuH);
    }
    pos = { top, left };
  }

  function toggle(e: MouseEvent) {
    e.stopPropagation();
    if (isOpen) {
      releaseOpen();
      return;
    }
    subOpen = null;
    doneAt = "";
    myId = claimOpen();
    place();
  }

  let doneTimer: ReturnType<typeof setTimeout> | undefined;

  function run(item: Item) {
    if (item.disabled) return;
    if (item.submenu) {
      subOpen = item;
      doneAt = "";
      // The submenu is usually taller than the row it replaced, so the flip
      // decision has to be made again or the last rows fall off-screen.
      queueMicrotask(place);
      return;
    }
    if (!item.keepOpen) releaseOpen();
    item.onclick?.();
    if (item.keepOpen && item.doneLabel) {
      doneAt = item.label;
      clearTimeout(doneTimer);
      doneTimer = setTimeout(() => (doneAt = ""), 1200);
    }
  }

  /* Leaving a submenu is Back, not close: the whole point of staying open is
     picking a different flavour after seeing the first one. */
  function back() {
    subOpen = null;
    doneAt = "";
    queueMicrotask(place);
  }

  /* The first place() in toggle() runs before the popup exists, so it uses
     the estimated height to pick a flip direction. Once menuEl mounts, place
     again with its real measured height to correct any estimate drift. */
  $effect(() => {
    if (isOpen && menuEl) place();
  });

  /* Outside-click / Escape / reposition wiring, only while open. */
  $effect(() => {
    if (!isOpen) return;
    const onDown = (ev: MouseEvent) => {
      const t = ev.target as Node;
      if (triggerEl?.contains(t)) return;
      if (menuEl?.contains(t)) return;
      releaseOpen();
    };
    const onKey = (ev: KeyboardEvent) => {
      if (ev.key !== "Escape") return;
      if (subOpen) back();
      else releaseOpen();
    };
    const reposition = () => place();
    window.addEventListener("mousedown", onDown, true);
    window.addEventListener("keydown", onKey);
    window.addEventListener("scroll", reposition, true);
    window.addEventListener("resize", reposition);
    return () => {
      window.removeEventListener("mousedown", onDown, true);
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("scroll", reposition, true);
      window.removeEventListener("resize", reposition);
    };
  });

  let menuEl = $state<HTMLDivElement | null>(null);

  /* Move the popup to <body> so it escapes each list row's stacking context.
     A row is `position:relative`, so a fixed child painted inside it can still
     be covered by the *next* sibling row (whose own stacking context paints
     later) — z-index alone can't win across sibling contexts. Portaling to the
     body takes the popup out of every row context entirely. */
  function portal(node: HTMLElement) {
    document.body.appendChild(node);
    return {
      destroy() {
        node.remove();
      },
    };
  }
</script>

<button
  bind:this={triggerEl}
  type="button"
  aria-label={ariaLabel}
  aria-haspopup="menu"
  aria-expanded={isOpen}
  class="flex {triggerSize} items-center justify-center rounded-lg text-black-700 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-700"
  onclick={toggle}
>
  <svg class={iconSize} fill="currentColor" viewBox="0 0 20 20" aria-hidden="true"><path d="M10 6a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3Zm0 5.5a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3Zm0 5.5a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3Z"/></svg>
</button>

{#if isOpen && pos}
  <div
    bind:this={menuEl}
    use:portal
    role="menu"
    style="position:fixed; top:{pos.top}px; left:{pos.left}px; width:{width}px; z-index:9999;"
    class="overflow-hidden rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 py-1 shadow-lg"
  >
    {#if subOpen}
      <button
        type="button"
        class="flex w-full items-center gap-1.5 border-b border-white-300 px-3 py-2 text-left text-xs font-medium text-black-700 hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-800"
        onclick={back}
      >
        <svg viewBox="0 0 16 16" class="h-3 w-3 shrink-0" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M10 4L6 8l4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
        {subOpen.label}
      </button>
    {/if}
    {#each rows as item (item.label)}
      <button
        type="button"
        role="menuitem"
        disabled={item.disabled}
        class="block w-full px-3 py-2 text-left text-sm hover:bg-white-200 disabled:opacity-50 dark:hover:bg-navy-800 {item.danger ? 'text-neg-400 hover:bg-neg-100' : 'text-black-800 dark:text-black-600'} {item.divider ? 'mt-1 border-t border-white-300 pt-3 dark:border-navy-600' : ''}"
        onclick={() => run(item)}
      >
        <span class="flex items-center gap-1.5">
          <span class="truncate">{doneAt === item.label ? (item.doneLabel ?? item.label) : item.label}</span>
          {#if doneAt === item.label}
            <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-pos-400" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 8.5l3.5 3.5L13 5" stroke-linecap="round" stroke-linejoin="round"/></svg>
          {:else if item.submenu}
            <svg viewBox="0 0 16 16" class="ml-auto h-3 w-3 shrink-0 opacity-60" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round"/></svg>
          {/if}
        </span>
        {#if item.hint}
          <span class="mt-0.5 block truncate text-xs text-black-700 dark:text-black-600">{item.hint}</span>
        {/if}
        {#if item.detail}
          <!-- Elided by the caller, not by CSS: a path's useful end is its
               TAIL, and the usual trick for that (direction:rtl) reorders the
               leading slash of a unix path to the wrong side. -->
          <span class="mt-0.5 block truncate font-mono text-[10px] text-black-700 dark:text-black-600" title={item.detail}>{item.detail}</span>
        {/if}
      </button>
    {/each}
  </div>
{/if}
