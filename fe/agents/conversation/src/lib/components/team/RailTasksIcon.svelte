<script lang="ts">
  /* The Tasks rail tab's icon with its marks, as the mockup's rail strip:
     the green count of active tasks (green even when one needs input — the
     amber dot says that), an amber dot top-left while a task waits for the
     person, and the spinning ring while a teammate works on one. The badge
     moves out to -6px when the ring is drawn, so the two do not overlap. */
  let { icon, count, needsInput, working }: { icon: string; count: number; needsInput: number; working: boolean } = $props();
</script>

<span class="relative inline-flex h-4 w-4 items-center justify-center">
  {#if working}
    <span
      data-testid="rail-ring-tasks"
      class="absolute inset-[-3px] rounded-full border-2 border-green-500 border-t-transparent animate-spin"
      aria-hidden="true"
    ></span>
  {/if}
  <svg
    viewBox="0 0 16 16"
    class="h-4 w-4 text-green-500"
    fill="none"
    stroke="currentColor"
    stroke-width="1.5"
  >
    {@html icon}
  </svg>
  {#if needsInput > 0}
    <!-- The 2px shadow is the strip's own fill, so the dot reads as cut out
         of the icon on both themes. -->
    <span
      data-testid="rail-dot-tasks"
      class="absolute -left-1.5 -top-1.5 h-[7px] w-[7px] rounded-full bg-amber-500 shadow-[0_0_0_2px_theme(colors.white.100)] dark:shadow-[0_0_0_2px_theme(colors.navy.700)]"
      aria-hidden="true"
    ></span>
  {/if}
  {#if count > 0}
    <span
      data-testid="rail-badge-tasks"
      class="absolute {working ? '-top-1.5 -right-1.5' : '-top-1 -right-1'} inline-flex h-3.5 min-w-3.5 items-center justify-center rounded-full bg-green-500 px-0.5 text-[9px] font-semibold text-white-100"
    >{count > 99 ? "99+" : count}</span>
  {/if}
</span>
