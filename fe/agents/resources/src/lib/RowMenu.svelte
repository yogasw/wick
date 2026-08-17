<script lang="ts">
  // The ⋯ menu at the end of a process row: copy its command, or end it.
  //
  // Kept behind a menu rather than shown as buttons because ending a
  // process is destructive and a row is a small target — a stray click on
  // a table that refreshes every 10 seconds should not kill anything.
  //
  // The panel is portalled to <body>. Inside the table it was clipped by
  // two ancestors at once: the card's `overflow-hidden` (which gives the
  // rounded corners) and the table's `overflow-x-auto` (which lets a wide
  // table scroll). Neither can be dropped, and no CSS rule applied to the
  // menu can escape a containing block — so the element leaves it.

  import { portal } from "$lib/portal.js";

  interface Props {
    // cmd is offered for copying; empty hides that entry rather than
    // showing one that copies nothing.
    cmd?: string;
    // label names the target in the confirmation, so the operator reads
    // what they are ending rather than a pid alone.
    label: string;
    // count > 1 means this ends a whole group.
    count?: number;
    // Set when the server would refuse this row (this wick server, pid 1).
    // The reason replaces the End entry instead of hiding it: a missing
    // option leaves the operator wondering where it went, while a stated
    // reason answers the question before it is asked.
    protectedReason?: string;
    onKill: () => void;
  }

  let { cmd = "", label, count = 1, protectedReason = "", onKill }: Props = $props();

  let open = $state(false);
  let confirming = $state(false);
  let copied = $state(false);
  let anchor = $state({ x: 0, y: 0 });
  let trigger = $state<HTMLButtonElement | null>(null);

  function toggle(): void {
    if (open) {
      close();
      return;
    }
    // Anchor to the trigger's viewport position. Read at open time rather
    // than on every render: the row can move under a refresh, and a menu
    // that drifts mid-click is worse than one that closes.
    const r = trigger?.getBoundingClientRect();
    if (r) anchor = { x: r.right, y: r.bottom + 4 };
    open = true;
    confirming = false;
  }

  function close(): void {
    open = false;
    confirming = false;
  }

  async function copyCmd(): Promise<void> {
    try {
      await navigator.clipboard.writeText(cmd);
      copied = true;
      setTimeout(() => {
        copied = false;
        close();
      }, 900);
    } catch {
      // Refused on an insecure origin or without permission. The command
      // is selectable in the expanded view, so this is not worth an error.
      close();
    }
  }

  function confirmKill(): void {
    onKill();
    close();
  }
</script>

<svelte:window
  onkeydown={(e) => {
    if (e.key === "Escape") close();
  }}
  onscroll={() => close()}
/>

<div class="flex justify-end">
  <button
    bind:this={trigger}
    type="button"
    class="rounded px-1.5 py-0.5 text-black-600 transition-colors hover:bg-white-300 hover:text-black-900 dark:text-black-700 dark:hover:bg-navy-600 dark:hover:text-white-100"
    aria-label="Actions for {label}"
    aria-expanded={open}
    onclick={toggle}
  >
    ⋯
  </button>
</div>

{#if open}
  <!-- Click-away. An overlay rather than a document listener: it cannot
       outlive this component, which matters on a table whose rows are
       created and destroyed on every refresh.
       Portalled for the same reason as the panel — `fixed inset-0` is
       still clipped to the card when its ancestor has `overflow-hidden`,
       which would leave most of the page unclickable-away. -->
  <button
    use:portal={{ x: 0, y: 0, fill: true }}
    type="button"
    class="cursor-default"
    aria-label="Close menu"
    onclick={close}
  ></button>

  <div
    use:portal={anchor}
    class="min-w-44 rounded-lg border border-white-300 bg-white-100 py-1 shadow-lg dark:border-navy-600 dark:bg-navy-800"
  >
    {#if cmd}
      <button
        type="button"
        class="block w-full px-3 py-1.5 text-left text-xs text-black-900 hover:bg-white-200 dark:text-white-100 dark:hover:bg-navy-700"
        onclick={() => void copyCmd()}
      >
        {copied ? "Copied" : "Copy command"}
      </button>
    {/if}

    {#if protectedReason}
      <p class="px-3 py-1.5 text-[11px] leading-snug text-black-700 dark:text-black-600">
        {protectedReason}
      </p>
    {:else if !confirming}
      <button
        type="button"
        class="block w-full px-3 py-1.5 text-left text-xs text-red-600 hover:bg-white-200 dark:text-red-400 dark:hover:bg-navy-700"
        onclick={() => (confirming = true)}
      >
        {count > 1 ? `End all ${count} processes` : "End process"}
      </button>
    {:else}
      <!-- Two steps on purpose. The first click states exactly what is
           about to end; the second does it. -->
      <div class="px-3 py-1.5">
        <p class="text-xs text-black-900 dark:text-white-100">
          End {count > 1 ? `${count} × ` : ""}<span class="font-medium">{label}</span>?
        </p>
        <p class="mt-0.5 text-[10px] text-black-700 dark:text-black-600">
          Unsaved work in it may be lost.
        </p>
        <div class="mt-1.5 flex gap-2">
          <button
            type="button"
            class="rounded bg-red-600 px-2 py-0.5 text-[11px] text-white-100 hover:bg-red-700"
            onclick={confirmKill}
          >
            End
          </button>
          <button
            type="button"
            class="rounded border border-white-300 px-2 py-0.5 text-[11px] text-black-700 hover:bg-white-200 dark:border-navy-600 dark:text-black-600 dark:hover:bg-navy-700"
            onclick={() => (confirming = false)}
          >
            Cancel
          </button>
        </div>
      </div>
    {/if}
  </div>
{/if}
