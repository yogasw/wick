<script lang="ts">
  /* Agent avatar: a coloured blob (circle / squircle / triangle / diamond)
     with two small eyes. Still when idle; while the agent works the outline
     wobbles and the eyes drift, which is the roster's "typing…" without a
     second indicator. One rAF loop per WORKING avatar only — an idle
     roster costs nothing — and none at all under prefers-reduced-motion. */
  import { onDestroy } from "svelte";
  import { blobPath, normalizeShape } from "../avatarShape.js";

  type Props = {
    shape?: string;
    color?: string;
    size?: number;
    working?: boolean;
    /** Greyed and eyes closed: a disabled agent. */
    asleep?: boolean;
    title?: string;
  };
  let { shape = "circle", color = "#6366f1", size = 40, working = false, asleep = false, title }: Props = $props();

  const s = $derived(normalizeShape(shape));
  let t = $state(0);
  let raf = 0;

  const reduced =
    typeof window !== "undefined" && typeof window.matchMedia === "function"
      ? window.matchMedia("(prefers-reduced-motion: reduce)").matches
      : true;

  function tick(ms: number) {
    t = ms / 1000;
    raf = requestAnimationFrame(tick);
  }

  $effect(() => {
    if (working && !asleep && !reduced && typeof requestAnimationFrame === "function") {
      raf = requestAnimationFrame(tick);
      return () => cancelAnimationFrame(raf);
    }
    t = 0;
  });
  onDestroy(() => {
    if (raf) cancelAnimationFrame(raf);
  });

  const animate = $derived(working && !asleep && !reduced);
  const path = $derived(blobPath(s, 50, 52, 44, t, animate));
  // Eyes sit a touch lower on the triangle, whose mass is at the bottom.
  const eyeY = $derived(s === "triangle" ? 60 : 48);
  const gaze = $derived(animate ? Math.sin(t * 1.3) * 4 : 0);
  const fill = $derived(/^#[0-9a-f]{3,8}$/i.test(color) ? color : "#6366f1");
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 100 100"
  class="shrink-0"
  style:opacity={asleep ? 0.45 : 1}
  role="img"
  aria-label={title ?? "avatar"}
>
  {#if title}<title>{title}</title>{/if}
  <path d={path} fill={fill} />
  {#if asleep}
    <rect x={30 + gaze} y={eyeY} width="14" height="3" rx="1.5" fill="white" />
    <rect x={56 + gaze} y={eyeY} width="14" height="3" rx="1.5" fill="white" />
  {:else}
    <rect x={34 + gaze} y={eyeY - 8} width="8" height="16" rx="4" fill="white" />
    <rect x={58 + gaze} y={eyeY - 8} width="8" height="16" rx="4" fill="white" />
  {/if}
</svg>
