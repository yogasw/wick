<script lang="ts">
  /* ProgressBar — a thin progress bar with an optional label line above.
     pct is 0–100; any negative pct means indeterminate (pulsing third).
     Shared by the plugin update in the manager and the wick-managed
     provider binary downloads. */
  type Props = {
    pct: number;
    label?: string;
    class?: string;
    testid?: string;
  };
  let { pct, label = "", class: klass = "", testid }: Props = $props();

  const indeterminate = $derived(pct < 0);
  const width = $derived(Math.max(0, Math.min(100, pct)));
</script>

<div class={klass} data-testid={testid}>
  {#if label}
    <div class="flex items-center justify-between text-[11px] font-medium text-black-800 dark:text-black-600">
      <span>{label}</span>
    </div>
  {/if}
  <div
    class="{label ? 'mt-1' : ''} h-1.5 w-full overflow-hidden rounded-full bg-white-300 dark:bg-navy-600"
    role="progressbar"
    aria-valuemin={0}
    aria-valuemax={100}
    aria-valuenow={indeterminate ? undefined : Math.round(width)}
    aria-label={label || undefined}
  >
    {#if indeterminate}
      <div class="h-full w-1/3 animate-pulse rounded-full bg-green-500"></div>
    {:else}
      <div class="h-full rounded-full bg-green-500 transition-all duration-200" style={`width:${width}%`}></div>
    {/if}
  </div>
</div>
