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
    onclick: () => void;
    danger?: boolean;
    disabled?: boolean;
  };
  type Props = {
    items: Item[];
    ariaLabel?: string;
    /* Menu width in px (Tailwind w-* isn't available on the fixed layer). */
    width?: number;
  };
  let { items, ariaLabel = "Actions", width = 176 }: Props = $props();

  let myId = $state(0);
  let triggerEl = $state<HTMLButtonElement | null>(null);
  let pos = $state<{ top: number; left: number } | null>(null);

  const isOpen = $derived(myId !== 0 && currentOwner === myId);

  function place() {
    if (!triggerEl) return;
    const r = triggerEl.getBoundingClientRect();
    // Right-align the menu under the trigger; clamp into the viewport.
    let left = r.right - width;
    if (left < 8) left = 8;
    if (left + width > window.innerWidth - 8) left = window.innerWidth - 8 - width;
    pos = { top: r.bottom + 4, left };
  }

  function toggle(e: MouseEvent) {
    e.stopPropagation();
    if (isOpen) {
      releaseOpen();
      return;
    }
    myId = claimOpen();
    place();
  }

  function run(item: Item) {
    if (item.disabled) return;
    releaseOpen();
    item.onclick();
  }

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
      if (ev.key === "Escape") releaseOpen();
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
</script>

<button
  bind:this={triggerEl}
  type="button"
  aria-label={ariaLabel}
  aria-haspopup="menu"
  aria-expanded={isOpen}
  class="flex h-8 w-8 items-center justify-center rounded-lg text-black-700 hover:bg-white-200 dark:text-black-600 dark:hover:bg-navy-700"
  onclick={toggle}
>
  <svg class="h-5 w-5" fill="currentColor" viewBox="0 0 20 20" aria-hidden="true"><path d="M10 6a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3Zm0 5.5a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3Zm0 5.5a1.5 1.5 0 1 1 0-3 1.5 1.5 0 0 1 0 3Z"/></svg>
</button>

{#if isOpen && pos}
  <div
    bind:this={menuEl}
    role="menu"
    style="position:fixed; top:{pos.top}px; left:{pos.left}px; width:{width}px; z-index:9999;"
    class="overflow-hidden rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-700 py-1 shadow-lg"
  >
    {#each items as item (item.label)}
      <button
        type="button"
        role="menuitem"
        disabled={item.disabled}
        class="block w-full px-3 py-2 text-left text-sm hover:bg-white-200 disabled:opacity-50 dark:hover:bg-navy-800 {item.danger ? 'text-neg-400 hover:bg-neg-100' : 'text-black-800 dark:text-black-600'}"
        onclick={() => run(item)}
      >{item.label}</button>
    {/each}
  </div>
{/if}
