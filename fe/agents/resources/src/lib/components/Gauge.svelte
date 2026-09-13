<script lang="ts">
  /* A small donut for a "used out of total" card.

     The bar it replaces answered "roughly how full?" only if you measured
     it with your eye, and the numbers beside it ("26.6 GB / 32.4 GB")
     need arithmetic before they mean anything. A ring with the
     percentage in the middle answers both at a glance; the absolute
     values stay next to it because on a 32 GB disk the difference
     between 4 GB and 400 MB free matters more than the percentage does.

     `tone` is passed in rather than computed here: disk pressure is
     graded SERVER-side on percentage AND absolute free together, because
     percentage alone cries wolf (a 328 GB disk at 93% still has 22 GB
     free and nothing is about to fail). */

  type Props = {
    /** 0-100. Out-of-range input is clamped rather than trusted. */
    pct: number;
    /** Tailwind text-* class for the arc, e.g. "text-blue-600". */
    tone?: string;
    size?: number;
    /** Screen-reader description; the visible number is just a number. */
    label?: string;
  };

  let { pct, tone = "text-blue-600", size = 56, label = "" }: Props = $props();

  let value = $derived(Math.min(100, Math.max(0, Number.isFinite(pct) ? pct : 0)));
  const stroke = 6;
  let radius = $derived(size / 2 - stroke / 2);
  let circumference = $derived(2 * Math.PI * radius);
  let dash = $derived((value / 100) * circumference);
</script>

<div class="relative shrink-0" style={`width:${size}px;height:${size}px`}>
  <svg
    width={size}
    height={size}
    viewBox={`0 0 ${size} ${size}`}
    role="img"
    aria-label={label || `${Math.round(value)}% used`}
  >
    <!-- Rotated so the arc starts at 12 o'clock and fills clockwise. -->
    <g transform={`rotate(-90 ${size / 2} ${size / 2})`} fill="none" stroke-linecap="round">
      <circle
        cx={size / 2}
        cy={size / 2}
        r={radius}
        stroke-width={stroke}
        class="text-white-300 dark:text-navy-600"
        stroke="currentColor"
      />
      <circle
        data-testid="gauge-arc"
        cx={size / 2}
        cy={size / 2}
        r={radius}
        stroke-width={stroke}
        stroke-dasharray={`${dash} ${circumference}`}
        class={tone}
        stroke="currentColor"
      />
    </g>
  </svg>
  <span
    class="absolute inset-0 flex items-center justify-center text-xs font-semibold text-black-900 dark:text-white-100"
    aria-hidden="true"
  >{Math.round(value)}%</span>
</div>
