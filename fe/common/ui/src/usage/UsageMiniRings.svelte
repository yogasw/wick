<script lang="ts">
  /* Usage at a glance for one picker row: a small 5h ring and 7d ring
     (green < 80, amber 80–99, red at the limit) plus ✦N saved resets.
     "—" when the account was never checked. Takes a UsageGlance from the
     shared usage store, so the row never fetches on its own. */
  import SavedResetsChip from "./SavedResetsChip.svelte";
  import { ringTone, fmtResetDateTime } from "./base/index.js";
  import type { GlanceWindow, UsageGlance } from "./base/index.js";

  type Props = { glance: UsageGlance | null | undefined; size?: number };
  let { glance, size = 18 }: Props = $props();

  const LABELS: Record<string, string> = { five_hour: "5h", seven_day: "7d" };
  let rings = $derived(
    (["five_hour", "seven_day"] as const)
      .map((k) => glance?.windows.find((w) => w.key === k))
      .filter((w): w is GlanceWindow => !!w),
  );
  const toneClass = { ok: "text-green-500", warn: "text-cau-400", full: "text-neg-400" } as const;
  const stroke = 2.5;
  let r = $derived(size / 2 - stroke / 2 - 0.5);
  let circ = $derived(2 * Math.PI * r);

  function tip(w: GlanceWindow): string {
    const at = w.resetsAt ? ` · resets ${fmtResetDateTime(w.resetsAt)}` : "";
    return `${LABELS[w.key] ?? w.key} ${Math.round(w.utilization)}%${at}`;
  }
  let title = $derived(rings.map(tip).join("\n"));
</script>

{#if glance?.supported}
  <span class="inline-flex shrink-0 items-center gap-1.5" data-testid="usage-mini-rings" {title}>
    {#if rings.length === 0}
      <span class="text-xs text-black-700 dark:text-black-600" title="Not checked yet">—</span>
    {:else}
      {#each rings as w (w.key)}
        {@const pct = Math.min(100, Math.max(0, w.utilization || 0))}
        <span class="relative inline-flex items-center justify-center" style="width:{size}px;height:{size}px" aria-label={tip(w)}>
          <svg width={size} height={size} viewBox={`0 0 ${size} ${size}`} class="absolute inset-0">
            <g transform={`rotate(-90 ${size / 2} ${size / 2})`} fill="none" stroke-linecap="round">
              <circle cx={size / 2} cy={size / 2} {r} stroke-width={stroke} stroke="currentColor" class="text-white-300 dark:text-navy-600" />
              <circle
                cx={size / 2}
                cy={size / 2}
                {r}
                stroke-width={stroke}
                stroke="currentColor"
                stroke-dasharray={`${(pct / 100) * circ} ${circ}`}
                class={toneClass[ringTone(w.utilization)]}
              />
            </g>
          </svg>
          <span class="relative text-[7px] font-bold text-black-800 dark:text-black-600">{LABELS[w.key]}</span>
        </span>
      {/each}
    {/if}
    <SavedResetsChip resets={glance.savedResets} />
  </span>
{/if}
