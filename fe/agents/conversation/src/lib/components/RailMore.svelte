<script lang="ts">
  /* The rail's overflow control: the tabs that did not fit, plus the
     controls that decide what fits.

     Reordering happens over the FULL tab list, not just the hidden ones —
     moving a tab in here has to be able to promote it into the strip, and a
     list that only knew about hidden tabs could not express that. */
  import { MAX_VISIBLE, MIN_VISIBLE } from "../railPrefs.js";

  type Tab = { id: string; label: string; icon: string };

  type Props = {
    /* Tabs that did not fit — what the button is for. */
    overflow: Tab[];
    /* Every tab, in the current order — what reordering acts on. */
    all: Tab[];
    visible: number;
    /* Combined badge count across hidden tabs, so the button can say that
       something is in there without being opened. */
    hiddenCount: number;
    /* True when any hidden tab is actively working. */
    hiddenBusy: boolean;
    activeId: string | null;
    countFor: (id: string) => number;
    onSelect: (id: string) => void;
    onMove: (id: string, delta: number) => void;
    onVisible: (n: number) => void;
  };

  let {
    overflow,
    all,
    visible,
    hiddenCount,
    hiddenBusy,
    activeId,
    countFor,
    onSelect,
    onMove,
    onVisible,
  }: Props = $props();

  let open = $state(false);
  let arranging = $state(false);

  function pick(id: string) {
    open = false;
    arranging = false;
    onSelect(id);
  }
</script>

<div class="relative">
  <button
    type="button"
    title="More panels"
    aria-label="More panels"
    aria-expanded={open}
    onclick={() => { open = !open; }}
    class="group inline-flex w-full flex-col items-center justify-center gap-1 border-t border-white-300 px-1.5 py-2.5 transition-colors hover:bg-white-200 dark:border-navy-600 dark:hover:bg-navy-800"
  >
    <span class="relative inline-flex h-4 w-4 items-center justify-center">
      <svg viewBox="0 0 16 16" class="h-4 w-4 text-black-700 dark:text-black-600" fill="currentColor" aria-hidden="true">
        <circle cx="3" cy="8" r="1.4"></circle>
        <circle cx="8" cy="8" r="1.4"></circle>
        <circle cx="13" cy="8" r="1.4"></circle>
      </svg>
      <!-- The hidden tabs' badges surface here, or nothing in there is
           worth looking at and the button stays quiet. -->
      {#if hiddenCount > 0}
        <span
          class="absolute -right-1.5 -top-1.5 inline-flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-green-500 px-0.5 text-[9px] font-semibold text-white-100"
        >{hiddenCount > 99 ? "99+" : hiddenCount}</span>
      {:else if hiddenBusy}
        <span class="absolute -right-1 -top-1 h-2 w-2 rounded-full bg-green-500" aria-label="Working"></span>
      {/if}
    </span>
    <span class="text-[9px] leading-none text-black-700 [writing-mode:vertical-rl] dark:text-black-600">More</span>
  </button>

  {#if open}
    <!-- Anchored to the rail's left edge: the rail is pinned to the right
         side of the viewport, so the panel has to open inward. -->
    <div
      class="absolute right-full top-0 z-30 mr-1 w-60 rounded-xl border border-white-300 bg-white-100 p-2 shadow-lg dark:border-navy-600 dark:bg-navy-700"
    >
      <div class="flex items-center justify-between px-1 pb-1">
        <span class="text-[11px] font-semibold text-black-900 dark:text-white-100">
          {arranging ? "Arrange panels" : "More panels"}
        </span>
        <button
          type="button"
          onclick={() => { arranging = !arranging; }}
          class="rounded px-1.5 py-0.5 text-[10px] font-medium text-green-600 transition-colors hover:bg-green-50 dark:text-green-400 dark:hover:bg-green-900/20"
        >{arranging ? "Done" : "Arrange"}</button>
      </div>

      {#if arranging}
        <p class="px-1 pb-2 text-[10px] leading-relaxed text-black-700 dark:text-black-600">
          Order and count are saved to your profile. A panel with a badge is
          always shown, wherever it sits.
        </p>

        <label class="flex items-center gap-2 px-1 pb-2 text-[11px] text-black-800 dark:text-black-600">
          Show
          <input
            type="number"
            min={MIN_VISIBLE}
            max={MAX_VISIBLE}
            value={visible}
            aria-label="Panels shown before More"
            oninput={(e) => onVisible(Number((e.target as HTMLInputElement).value))}
            class="w-14 rounded border border-white-400 bg-white-100 px-1.5 py-0.5 text-[11px] text-black-900 outline-none focus:border-green-500 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100"
          />
          in the strip
        </label>

        <ul class="flex max-h-72 flex-col gap-1 overflow-y-auto">
          {#each all as t, i (t.id)}
            <li class="flex items-center gap-1 rounded px-1 py-0.5">
              <span class="w-4 shrink-0 text-center text-[10px] text-black-600 dark:text-black-700">
                {i + 1}
              </span>
              <svg viewBox="0 0 16 16" class="h-3.5 w-3.5 shrink-0 text-black-700 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                {@html t.icon}
              </svg>
              <span class="min-w-0 flex-1 truncate text-[11px] text-black-900 dark:text-white-100">
                {t.label}
              </span>
              {#if i < visible}
                <span class="shrink-0 rounded bg-white-200 px-1 text-[9px] text-black-700 dark:bg-navy-800 dark:text-black-600">strip</span>
              {/if}
              <button
                type="button"
                aria-label={"Move " + t.label + " up"}
                disabled={i === 0}
                onclick={() => onMove(t.id, -1)}
                class="flex h-5 w-5 shrink-0 items-center justify-center rounded text-black-700 transition-colors hover:bg-white-200 disabled:opacity-30 dark:text-black-600 dark:hover:bg-navy-800"
              >
                <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M4 10l4-4 4 4" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              </button>
              <button
                type="button"
                aria-label={"Move " + t.label + " down"}
                disabled={i === all.length - 1}
                onclick={() => onMove(t.id, 1)}
                class="flex h-5 w-5 shrink-0 items-center justify-center rounded text-black-700 transition-colors hover:bg-white-200 disabled:opacity-30 dark:text-black-600 dark:hover:bg-navy-800"
              >
                <svg viewBox="0 0 16 16" class="h-3 w-3" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                  <path d="M4 6l4 4 4-4" stroke-linecap="round" stroke-linejoin="round"></path>
                </svg>
              </button>
            </li>
          {/each}
        </ul>
      {:else}
        <ul class="flex max-h-72 flex-col gap-0.5 overflow-y-auto">
          {#each overflow as t (t.id)}
            {@const n = countFor(t.id)}
            <li>
              <button
                type="button"
                data-testid={"rail-more-" + t.id}
                onclick={() => pick(t.id)}
                class={[
                  "flex w-full items-center gap-2 rounded px-2 py-1.5 text-left transition-colors",
                  activeId === t.id
                    ? "bg-green-50 dark:bg-green-900/20"
                    : "hover:bg-white-200 dark:hover:bg-navy-800",
                ].join(" ")}
              >
                <svg viewBox="0 0 16 16" class="h-4 w-4 shrink-0 text-black-800 dark:text-black-600" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                  {@html t.icon}
                </svg>
                <span class="min-w-0 flex-1 truncate text-xs text-black-900 dark:text-white-100">{t.label}</span>
                {#if n > 0}
                  <span class="shrink-0 rounded-full bg-green-500 px-1.5 text-[9px] font-semibold text-white-100">
                    {n > 99 ? "99+" : n}
                  </span>
                {/if}
              </button>
            </li>
          {/each}
          {#if overflow.length === 0}
            <li class="px-2 py-3 text-center text-[11px] text-black-700 dark:text-black-600">
              Every panel fits in the strip.
            </li>
          {/if}
        </ul>
      {/if}
    </div>

    <!-- Click-away. Behind the panel, over everything else. -->
    <button
      type="button"
      tabindex="-1"
      aria-label="Close"
      onclick={() => { open = false; arranging = false; }}
      class="fixed inset-0 z-20 cursor-default"
    ></button>
  {/if}
</div>
