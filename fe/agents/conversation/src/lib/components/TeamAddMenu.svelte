<script lang="ts">
  /* The grey ＋ beside the roster's Search (team-sidebar mockup): a ghost
     button as tall as the search box, opening New agent / New group.
     New group stays visible but off, with the reason, while there are
     fewer agents than a group needs. Esc or a press outside closes it;
     Esc gives focus back to the button. */
  import { tick } from "svelte";

  type Props = {
    /** False greys out New group ("Needs 2+ agents"). */
    canGroup: boolean;
    onAgent: () => void;
    onGroup: () => void;
  };
  let { canGroup, onAgent, onGroup }: Props = $props();

  let open = $state(false);
  let root = $state<HTMLDivElement>();
  let button = $state<HTMLButtonElement>();

  async function close(refocus: boolean) {
    open = false;
    if (refocus) {
      await tick();
      button?.focus();
    }
  }
  function onKey(e: KeyboardEvent) {
    if (open && e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      void close(true);
    }
  }
  function onPointer(e: PointerEvent) {
    if (open && root && !root.contains(e.target as Node)) void close(false);
  }

  const item =
    "flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-sm text-black-900 transition-colors dark:text-white-100";
</script>

<svelte:window onkeydowncapture={onKey} onpointerdown={onPointer} />

<div class="relative shrink-0" bind:this={root}>
  <button
    type="button"
    bind:this={button}
    class="flex h-9 w-9 items-center justify-center rounded-xl text-black-700 transition-colors hover:bg-white-300 hover:text-black-900 focus:outline-none focus-visible:ring-2 focus-visible:ring-green-400 dark:text-black-600 dark:hover:bg-navy-600 dark:hover:text-white-100 {open ? 'bg-white-300 text-black-900 dark:bg-navy-600 dark:text-white-100' : ''}"
    title="New agent or group"
    aria-label="New agent or group"
    aria-haspopup="menu"
    aria-expanded={open}
    onclick={() => (open = !open)}
  >
    <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><path d="M8 3v10M3 8h10"></path></svg>
  </button>
  {#if open}
    <div class="absolute right-0 top-full z-50 mt-1.5 w-48 rounded-xl border border-white-300 bg-white-100 p-1 shadow-lg dark:border-navy-600 dark:bg-navy-700" role="menu" aria-label="New">
      <button type="button" role="menuitem" class="{item} hover:bg-white-200 dark:hover:bg-navy-800" onclick={() => { void close(false); onAgent(); }}>
        <svg class="h-4 w-4 shrink-0 text-black-700 dark:text-black-600" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true"><circle cx="8" cy="5.5" r="2.5"></circle><path d="M3 13.5c.6-2.3 2.6-3.5 5-3.5s4.4 1.2 5 3.5"></path></svg>
        New agent
      </button>
      <button
        type="button"
        role="menuitem"
        class="{item} {canGroup ? 'hover:bg-white-200 dark:hover:bg-navy-800' : 'cursor-default opacity-50'}"
        disabled={!canGroup}
        aria-disabled={!canGroup}
        onclick={() => { if (!canGroup) return; void close(false); onGroup(); }}
      >
        <svg class="h-4 w-4 shrink-0 text-black-700 dark:text-black-600" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" aria-hidden="true"><circle cx="6" cy="5.5" r="2"></circle><circle cx="11" cy="6" r="1.6"></circle><path d="M2 13c.5-2 2-3 4-3s3.5 1 4 3M10.5 9.5c1.6 0 2.9.9 3.3 2.7"></path></svg>
        <span class="flex min-w-0 flex-col leading-tight">
          New group
          {#if !canGroup}<span class="text-xs text-black-700 dark:text-black-600">Needs 2+ agents</span>{/if}
        </span>
      </button>
    </div>
  {/if}
</div>
