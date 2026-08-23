<script lang="ts">
  /* The rail's "More" button and the panel behind it.

     The panel lists the HIDDEN panels — the ones the button is announcing —
     and each row carries what you can do to one: open it, put it back in the
     strip (the eye), or move it in the order (the arrows, or a drag).

     Two earlier shapes were wrong. Listing every panel here contradicted the
     badge: it said 4 while the list showed 9, so "More" meant nothing. And
     putting the reordering behind a separate "Arrange" mode meant the list
     you were looking at was not the list you could act on.

     Folding a VISIBLE panel is not in here, because it does not belong here:
     it is a drag from the strip onto this button. */

  type Tab = { id: string; label: string; icon: string };

  type Props = {
    /* Tabs that did not fit — what the button is for. */
    overflow: Tab[];
    /* Every tab, in the current order — what reordering acts on. */
    all: Tab[];
    /* Combined badge count across hidden tabs. Used to colour the button,
       not to number it: loud tabs are promoted into the strip, so this is
       almost always 0 and would make the badge read "nothing here" while
       hiding several tabs. */
    hiddenCount: number;
    /* True when any hidden tab is actively working. */
    hiddenBusy: boolean;
    activeId: string | null;
    countFor: (id: string) => number;
    onSelect: (id: string) => void;
    onMove: (id: string, delta: number) => void;
    /* Drop a tab at an absolute position in the full order. Order only —
       whether a panel is hidden is its own choice. */
    onReorder: (id: string, to: number) => void;
    /* Put a hidden tab back in the strip. */
    onToggleHidden: (id: string) => void;
    /* True while a strip tab is being dragged, so this button can offer
       itself as the place to drop it. */
    dragging?: boolean;
    /* A strip tab dropped onto the button or the hidden list — fold it. */
    onDropHere?: (id: string) => void;
    /* A hidden tab being dragged OUT of this panel, so the strip can light
       up as a drop target. null on drag end. */
    onDragOut?: (id: string | null) => void;
  };

  let {
    overflow,
    all,
    hiddenCount,
    hiddenBusy,
    activeId,
    countFor,
    onSelect,
    onMove,
    onReorder,
    onToggleHidden,
    dragging = false,
    onDropHere,
    onDragOut,
  }: Props = $props();

  /* Lit while a strip tab hovers the button, so the drop target is visible
     before the pointer is released. */
  let dropHover = $state(false);
  /* Same, for the hidden list below. */
  let listHover = $state(false);

  let open = $state(false);

  function pick(id: string) {
    open = false;
    onSelect(id);
  }

  /* ── drag to reorder ── */

  let dragId = $state<string | null>(null);
  /* Where the dragged row would land — drawn as a line, because a list that
     only highlights the row under the cursor cannot say whether the drop
     goes above or below it. */
  let dropAt = $state<number | null>(null);

  /* The row is both a reorder handle inside this list and something that can
     be dragged out onto the strip, so it carries the strip's payload: the
     strip accepts it without needing to know where the drag began. */
  function dragStart(e: DragEvent, id: string) {
    dragId = id;
    e.dataTransfer?.setData("text/plain", "railtab:" + id);
    if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
    onDragOut?.(id);
  }

  /* The drop index is the row's own position when the pointer is in its top
     half, and the one after it in the bottom half — so a row can be dropped
     at either side of every neighbour, including past the last one. */
  function dragOverRow(e: DragEvent, index: number) {
    if (dragId === null) return;
    e.preventDefault();
    const box = (e.currentTarget as HTMLElement).getBoundingClientRect();
    dropAt = e.clientY - box.top > box.height / 2 ? index + 1 : index;
  }

  /* The rows shown here are a subset of the order, so a drop position has to
     be translated: landing "before row 2 of this list" means landing before
     that row's place in the FULL order. Without the translation, dragging
     inside a 4-row list would move panels to positions 0-4 of a 9-panel
     rail. */
  function drop() {
    const id = dragId;
    const at = dropAt;
    dragId = null;
    dropAt = null;
    if (id === null || at === null) return;
    const from = all.findIndex((t) => t.id === id);
    if (from < 0) return;
    const anchor = overflow[at];
    // Past the last row: the end of the order.
    const to = anchor ? all.findIndex((t) => t.id === anchor.id) : all.length - 1;
    if (to < 0) return;
    onReorder(id, to > from ? to - 1 : to);
  }

  function dragEnd() {
    dragId = null;
    dropAt = null;
    onDragOut?.(null);
  }

</script>

<div class="relative">
  <button
    type="button"
    title="More panels"
    aria-label="More panels"
    aria-expanded={open}
    onclick={() => { open = !open; }}
    ondragover={(e) => { if (dragging) { e.preventDefault(); dropHover = true; } }}
    ondragleave={() => { dropHover = false; }}
    ondrop={(e) => {
      e.preventDefault();
      dropHover = false;
      const id = e.dataTransfer?.getData("text/plain") ?? "";
      if (id.startsWith("railtab:")) onDropHere?.(id.slice("railtab:".length));
    }}
    class={[
      // rounded-bl-xl on the button itself: the strip cannot clip its
      // children (this panel opens out of it), so the bottom corner has to
      // be carried by whatever sits last — which is always this button.
      "group inline-flex w-full flex-col items-center justify-center gap-1 rounded-bl-xl border-t border-white-300 px-1.5 py-2.5 transition-colors hover:bg-white-200 dark:border-navy-600 dark:hover:bg-navy-800",
      dropHover ? "ring-2 ring-inset ring-green-500" : "",
    ].join(" ")}
  >
    <span class="relative inline-flex h-4 w-4 items-center justify-center">
      <svg viewBox="0 0 16 16" class="h-4 w-4 text-black-700 dark:text-black-600" fill="currentColor" aria-hidden="true">
        <circle cx="3" cy="8" r="1.4"></circle>
        <circle cx="8" cy="8" r="1.4"></circle>
        <circle cx="13" cy="8" r="1.4"></circle>
      </svg>
      <!-- The badge counts HIDDEN TABS, which is the question this button
           answers: how much of the rail is folded away. It used to sum those
           tabs' own badges, and since a tab with a badge gets promoted into
           the strip, that sum was ~always 0 — the button looked empty while
           holding four tabs. A hidden badge still shows, as colour. -->
      {#if overflow.length > 0}
        <span
          data-testid="rail-more-count"
          aria-label={`${overflow.length} tabs hidden`}
          class={[
            "absolute -right-1.5 -top-1.5 inline-flex h-3.5 min-w-3.5 items-center justify-center rounded-full px-0.5 text-[9px] font-semibold",
            hiddenCount > 0 || hiddenBusy
              ? "bg-green-500 text-white-100"
              : "bg-white-300 text-black-800 dark:bg-navy-600 dark:text-black-600",
          ].join(" ")}
        >{overflow.length > 99 ? "99+" : overflow.length}</span>
      {:else if hiddenBusy}
        <span class="absolute -right-1 -top-1 h-2 w-2 rounded-full bg-green-500" aria-label="Working"></span>
      {/if}
    </span>
    <span class="text-[9px] leading-none text-black-700 [writing-mode:vertical-rl] dark:text-black-600">More</span>
  </button>

  {#if open}
    <!-- Anchored to the rail's left edge: the rail is pinned to the right
         side of the viewport, so the panel has to open inward.

         Anchored to this button's BOTTOM edge and grown upward, because the
         button is the last thing in the rail and the rail is centred: there
         is always room above it and rarely any below. It used to open
         downward from `top-0`, which ran the list off the bottom of the
         screen and left the last rows unreachable. The height cap is a
         backstop for a very long list on a short window. -->
    <div
      class="absolute bottom-0 right-full z-30 mr-1 flex max-h-[80vh] w-60 flex-col rounded-xl border border-white-300 bg-white-100 p-2 shadow-lg dark:border-navy-600 dark:bg-navy-700"
    >
      <div class="shrink-0 px-1 pb-1">
        <span class="text-[11px] font-semibold text-black-900 dark:text-white-100">
          Hidden panels
        </span>
        <p class="mt-0.5 text-[10px] leading-relaxed text-black-700 dark:text-black-600">
          Click to open. The eye puts one back in the strip; drag a strip panel
          here to hide it. Saved to your profile.
        </p>
      </div>

      <!-- Only the HIDDEN panels. Listing every panel here contradicted the
           button it hangs off: the badge said 4 and the list showed 9, so
           "More" stopped meaning anything. Folding a visible panel is the
           drag onto this button instead — the gesture already existed and
           needed no list of its own. -->
      <ul
        class={[
          // min-h-0 so the scroll happens HERE rather than the flex item
          // refusing to shrink and pushing the panel past its own cap.
          "flex min-h-0 flex-1 flex-col overflow-y-auto rounded",
          dragging && listHover ? "ring-2 ring-inset ring-green-500" : "",
        ].join(" ")}
        ondragover={(e) => {
          if (dragId !== null) { e.preventDefault(); return; }
          if (dragging) { e.preventDefault(); listHover = true; }
        }}
        ondragleave={() => { listHover = false; }}
        ondrop={(e) => {
          e.preventDefault();
          if (dragId !== null) { drop(); return; }
          listHover = false;
          const raw = e.dataTransfer?.getData("text/plain") ?? "";
          if (raw.startsWith("railtab:")) onDropHere?.(raw.slice("railtab:".length));
        }}
      >
        {#each overflow as t, i (t.id)}
          {@const n = countFor(t.id)}
          <li
            draggable="true"
            data-testid={"rail-row-" + t.id}
            ondragstart={(e) => dragStart(e, t.id)}
            ondragover={(e) => dragOverRow(e, i)}
            ondragend={dragEnd}
            class={[
              "group/row flex cursor-grab items-center gap-1 rounded pr-1 transition-colors active:cursor-grabbing",
              dragId === t.id ? "opacity-40" : "",
              activeId === t.id ? "bg-green-50 dark:bg-green-900/20" : "hover:bg-white-200 dark:hover:bg-navy-800",
              dropAt === i ? "border-t-2 border-green-500" : "",
              dropAt === i + 1 ? "border-b-2 border-green-500" : "",
            ].join(" ")}
          >
            <button
              type="button"
              data-testid={"rail-more-" + t.id}
              onclick={() => pick(t.id)}
              class="flex min-w-0 flex-1 items-center gap-2 px-2 py-1.5 text-left"
            >
              <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-black-800 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                {@html t.icon}
              </svg>
              <span class="min-w-0 flex-1 truncate text-xs text-black-900 dark:text-white-100">
                {t.label}
              </span>
              {#if n > 0}
                <span class="shrink-0 rounded-full bg-green-500 px-1.5 text-[9px] font-semibold text-white-100">
                  {n > 99 ? "99+" : n}
                </span>
              {/if}
            </button>

            <!-- Arrows move the panel in the FULL order, not within this
                 slice: the order is one list and a hidden panel's position is
                 where it lands when it comes back. Bounds come from `all` for
                 the same reason. They sit before the eye and appear on hover
                 — the eye is on every row and holds the right edge, so that
                 column stays a straight line. -->
            <span class="flex shrink-0 opacity-0 transition-opacity focus-within:opacity-100 group-hover/row:opacity-100">
              <button
                type="button"
                aria-label={"Move " + t.label + " up"}
                disabled={all[0]?.id === t.id}
                onclick={() => onMove(t.id, -1)}
                class="flex h-6 w-5 items-center justify-center rounded text-black-700 transition-colors hover:bg-white-300 disabled:opacity-25 dark:text-black-600 dark:hover:bg-navy-600"
              >
                <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M8 12V4M4.5 7.5L8 4l3.5 3.5" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              </button>
              <button
                type="button"
                aria-label={"Move " + t.label + " down"}
                disabled={all.at(-1)?.id === t.id}
                onclick={() => onMove(t.id, 1)}
                class="flex h-6 w-5 items-center justify-center rounded text-black-700 transition-colors hover:bg-white-300 disabled:opacity-25 dark:text-black-600 dark:hover:bg-navy-600"
              >
                <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M8 4v8M4.5 8.5L8 12l3.5-3.5" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              </button>
            </span>

            <!-- Every row here is hidden, so the eye has one meaning: put it
                 back. The dragging works too, but a list of hidden things
                 needs a way out that does not require a pointer. -->
            <button
              type="button"
              aria-label={"Show " + t.label + " in the strip"}
              title="Show in the strip"
              data-testid={"rail-unfold-" + t.id}
              onclick={() => onToggleHidden(t.id)}
              class="flex h-6 w-6 shrink-0 items-center justify-center rounded text-black-700 transition-colors hover:bg-white-300 dark:text-black-600 dark:hover:bg-navy-600"
            >
              <svg viewBox="0 0 16 16" class="h-3.5 w-3.5" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                <path d="M2 8s2.5-4 6-4 6 4 6 4-2.5 4-6 4-6-4-6-4z" stroke-linejoin="round"></path>
                <circle cx="8" cy="8" r="1.5"></circle>
              </svg>
            </button>
          </li>
        {/each}
      </ul>
    </div>

    <!-- Click-away. Behind the panel, over everything else. -->
    <button
      type="button"
      tabindex="-1"
      aria-label="Close"
      onclick={() => { open = false; }}
      class="fixed inset-0 z-20 cursor-default"
    ></button>
  {/if}
</div>
