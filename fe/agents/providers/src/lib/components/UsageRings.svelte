<script lang="ts">
  /* Two nested arcs summarizing an account's rate-limit usage at a
     glance: inner = rolling 5-hour session window, outer = rolling
     7-day window. Sized for a card badge, so it carries no text of its
     own — the percentages live in the accessible label and the card
     spells out the numbers beside it. */
  import { pickWindows, ringDash, ringColor } from "$lib/usagerings.js";
  import { usageLabel, type UsageWindow } from "$lib/logintty.js";

  type Props = { windows: UsageWindow[]; size?: number };
  let { windows, size = 32 }: Props = $props();

  let rings = $derived(pickWindows(windows));
  /* Outer arc sits just inside the box; inner is one track width in.
     Stroke 3px keeps both arcs legible at 32px without touching. */
  const stroke = 3;
  let outerR = $derived(size / 2 - stroke / 2 - 1);
  let innerR = $derived(outerR - stroke - 2);

  let label = $derived.by(() => {
    const parts: string[] = [];
    if (rings.inner) parts.push(`${usageLabel("five_hour")} ${Math.round(rings.inner.utilization)}%`);
    if (rings.outer) parts.push(`${usageLabel(rings.outer.key)} ${Math.round(rings.outer.utilization)}%`);
    return parts.length > 0 ? `Usage: ${parts.join(", ")}` : "";
  });
</script>

{#if rings.inner || rings.outer}
  <svg
    role="img"
    aria-label={label}
    width={size}
    height={size}
    viewBox={`0 0 ${size} ${size}`}
    class="shrink-0"
  >
    <!-- Rotated so both arcs start at 12 o'clock and fill clockwise. -->
    <g transform={`rotate(-90 ${size / 2} ${size / 2})`} fill="none" stroke-linecap="round">
      {#if rings.outer}
        {@const g = ringDash(rings.outer.utilization, outerR)}
        <circle
          cx={size / 2}
          cy={size / 2}
          r={outerR}
          stroke-width={stroke}
          class="text-white-300 dark:text-navy-600"
          stroke="currentColor"
        />
        <circle
          data-ring="outer"
          cx={size / 2}
          cy={size / 2}
          r={outerR}
          stroke-width={stroke}
          stroke-dasharray={`${g.dash} ${g.circumference}`}
          class={ringColor(rings.outer.utilization)}
          stroke="currentColor"
        />
      {/if}
      {#if rings.inner}
        {@const g = ringDash(rings.inner.utilization, innerR)}
        <circle
          cx={size / 2}
          cy={size / 2}
          r={innerR}
          stroke-width={stroke}
          class="text-white-300 dark:text-navy-600"
          stroke="currentColor"
        />
        <circle
          data-ring="inner"
          cx={size / 2}
          cy={size / 2}
          r={innerR}
          stroke-width={stroke}
          stroke-dasharray={`${g.dash} ${g.circumference}`}
          class={ringColor(rings.inner.utilization)}
          stroke="currentColor"
        />
      {/if}
    </g>
  </svg>
{/if}
