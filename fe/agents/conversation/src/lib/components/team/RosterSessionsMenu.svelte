<script lang="ts">
  /* The count badge on a roster row whose agent runs more than one chat
     other than its main chat, and the small popover it opens: each running
     chat oldest first (spinner, title, who it works for, how long), a click
     opens that chat. Sits beside the row's button, not in it: a button
     cannot hold another. Menu keys: focus lands on the first chat,
     ArrowUp/Down/Home/End move, Esc closes and returns to the badge. */
  import type { WorkingSession } from "../../api/team.js";
  import { elapsedLabel, sessionTitle, triggerLabel } from "../../rosterSessions.js";

  type Props = {
    sessions: WorkingSession[];
    /** Clock for the elapsed time (the roster's minute tick). */
    now: number;
    onopen: (id: string) => void;
  };
  let { sessions, now, onopen }: Props = $props();

  let open = $state(false);
  let root = $state<HTMLElement | null>(null);
  let badge = $state<HTMLButtonElement | null>(null);
  let menu = $state<HTMLElement | null>(null);

  const items = () => [...(menu?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? [])];

  /* Opening moves focus to the first chat, as a menu does. */
  $effect(() => {
    if (open && menu) items()[0]?.focus();
  });

  function close(refocus: boolean) {
    open = false;
    if (refocus) badge?.focus();
  }
  function onWindowClick(e: MouseEvent) {
    if (open && root && !root.contains(e.target as Node)) close(false);
  }
  /* Keys stay inside the popover: Esc closes it alone (stopped here, it
     never reaches the page's own Esc that closes an open panel), the
     arrows walk the chats, Tab leaves and closes. */
  function onKey(e: KeyboardEvent) {
    if (!open) return;
    const list = items();
    const at = list.indexOf(document.activeElement as HTMLButtonElement);
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close(true);
    } else if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const step = e.key === "ArrowDown" ? 1 : -1;
      list[(at + step + list.length) % list.length]?.focus();
    } else if (e.key === "Home" || e.key === "End") {
      e.preventDefault();
      (e.key === "Home" ? list[0] : list[list.length - 1])?.focus();
    } else if (e.key === "Tab") {
      close(false);
    }
  }
</script>

<svelte:window onclick={onWindowClick} />

<!-- svelte-ignore a11y_no_static_element_interactions -->
<span class="absolute bottom-[9px] right-2 z-10" bind:this={root} onkeydown={onKey}>
  <button
    type="button"
    class="grid h-[18px] min-w-[18px] place-items-center rounded-full bg-green-500/15 px-[5px] text-[11px] font-bold leading-none tabular-nums text-green-700 hover:bg-green-500/25 dark:text-green-300"
    aria-label="{sessions.length} chats running"
    aria-haspopup="menu"
    aria-expanded={open}
    title="{sessions.length} chats running"
    data-testid="roster-sessions-count"
    bind:this={badge}
    onclick={() => (open = !open)}>{sessions.length}</button
  >
  {#if open}
    <div
      class="absolute right-0 top-full z-30 mt-1 w-64 rounded-xl border border-white-300 bg-white-100 p-1 shadow-lg dark:border-navy-600 dark:bg-navy-700"
      role="menu"
      aria-label="Running chats"
      data-testid="roster-sessions-menu"
      bind:this={menu}
    >
      <p aria-hidden="true" class="px-2 pb-1 pt-1.5 text-[11px] font-semibold uppercase tracking-wider text-black-800 dark:text-black-600">Running chats</p>
      {#each sessions as s (s.id)}
        <button
          type="button"
          role="menuitem"
          tabindex="-1"
          class="flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left hover:bg-white-300 focus:bg-white-300 focus:outline-none dark:hover:bg-navy-600 dark:focus:bg-navy-600"
          data-testid="roster-sessions-item"
          onclick={() => {
            open = false;
            onopen(s.id);
          }}
        >
          <span class="h-3 w-3 shrink-0 animate-spin rounded-full border-2 border-green-500 border-t-transparent" aria-hidden="true"></span>
          <span class="min-w-0 flex-1">
            <span class="block truncate text-xs font-medium text-black-900 dark:text-white-100">{sessionTitle(s)}</span>
            <span class="block truncate text-[11px] text-black-800 dark:text-black-600">{[triggerLabel(s), elapsedLabel(s.since, now)].filter(Boolean).join(" · ")}</span>
          </span>
        </button>
      {/each}
    </div>
  {/if}
</span>
