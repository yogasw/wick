<script lang="ts">
  /* Blob-mascot avatar (vendored blobmascot core, see ./blob/README.md).
     Two renderers, picked by blobAnimates():
       - still: one cached PNG frame (stillUrl) in an <img>. Mentions,
         cards and picker grids use this, so those lists never own a
         canvas or an animation loop.
       - live: a canvas stepped by the shared ticker (one rAF for every
         avatar on the page), subscribed only while the canvas is on
         screen. The roster and the chat header both use it, so an agent
         moves the same way in each. The eyes follow the pointer from BLOB_GAZE_MIN up.
     prefers-reduced-motion and `still` always get the still frame.
     A live avatar left idle fidgets now and then (blob/motion/fidget.ts):
     only while on screen, the tab shown and Idle animations on; a hover
     wakes one that dozed off. */
  import { onMount, untrack } from "svelte";
  import {
    blobAnimates, blobColor, blobFollowsGaze, createRuntime, drawFrame, gazeFromOffset,
    normalizeBlobExpression, normalizeBlobShape, snapshotOf, stillUrl, type BlobState, type Gaze,
  } from "./blob.js";
  import { subscribe, pointer, pointerActive, prefersReducedMotion } from "./ticker.js";
  import { createFidget, type FidgetPose } from "./blob/motion/fidget";
  import { idleAnimationsOn } from "./idle.js";

  type Props = {
    shape?: string;
    expression?: string;
    color?: string;
    size?: number;
    pose?: BlobState;
    /** Animate (header, empty state, settings preview). Off = still PNG. */
    live?: boolean;
    /** Waiting on something: fidget often (blob/motion/fidget.ts). */
    restless?: boolean;
    still?: boolean;
    hatching?: boolean;
    title?: string;
  };
  let { shape, expression, color, size = 40, pose = "idle", live = false, restless = false, still = false, hatching = false, title }: Props = $props();

  const WINK_MS = 450;
  const reduced = prefersReducedMotion();
  const dpr = typeof window === "undefined" ? 1 : Math.min(2, window.devicePixelRatio || 1);

  const look = $derived({ shape: normalizeBlobShape(shape), expression: normalizeBlobExpression(expression), color: blobColor(color) });
  const px = $derived(Math.max(8, Math.round(size * dpr)));
  const animate = $derived(blobAnimates({ live, still, reduced }));

  let winking = $state(false);
  let winkTimer: ReturnType<typeof setTimeout> | undefined;
  const shown = $derived<BlobState>(winking ? "wink" : pose);
  function onClick() {
    if (!animate || pose === "sleep") return;
    winking = true;
    clearTimeout(winkTimer);
    winkTimer = setTimeout(() => (winking = false), WINK_MS);
  }
  onMount(() => () => clearTimeout(winkTimer));

  const src = $derived(animate ? "" : stillUrl(look, shown, px));

  // One schedule per avatar, kept across scroll-aways so its idle clock
  // (and drowsiness) survives them.
  const now = () => (typeof performance === "undefined" ? 0 : performance.now() / 1000);
  const fidget = createFidget({ now: now() });
  onMount(() => () => fidget.stop());
  function onHover() {
    fidget.wake(now());
  }

  let canvas: HTMLCanvasElement | undefined = $state();
  let onScreen = $state(true);

  // Pause the loop while scrolled away; no IntersectionObserver = always on.
  $effect(() => {
    if (!animate || !canvas || typeof IntersectionObserver !== "function") return;
    const io = new IntersectionObserver((es) => (onScreen = es.some((e) => e.isIntersecting)));
    io.observe(canvas);
    return () => io.disconnect();
  });

  $effect(() => {
    if (!animate || !canvas || !onScreen) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    const el = canvas;
    // Read the look through a closure so a picker change lerps in the
    // running runtime instead of restarting the loop.
    const current = () => ({ look, shown, px });
    const runtime = untrack(() => createRuntime(snapshotOf(look, shown)));
    let last = -1;
    const draw = (t: number) => {
      const c = current();
      const dt = last < 0 ? 1 : Math.min(0.05, Math.max(0, t - last));
      last = t;
      let gaze: Gaze = { yaw: 0, pitch: 0 };
      const toPointer = () => {
        const r = el.getBoundingClientRect();
        return gazeFromOffset(pointer.x - (r.left + r.width / 2), pointer.y - (r.top + r.height / 2), window.innerWidth, window.innerHeight);
      };
      if (blobFollowsGaze(size, c.shown) && pointerActive(performance.now())) gaze = toPointer();
      let fp: FidgetPose | null = fidget.step({
        time: t,
        size,
        busy: c.shown !== "idle",
        enabled: idleAnimationsOn(),
        hidden: document.visibilityState === "hidden",
        restless,
      });
      // A peek at the cursor: wherever the pointer last was, if it ever moved.
      if (fp?.gaze === "cursor") fp = { ...fp, gaze: pointer.at > 0 ? toPointer() : { yaw: 20, pitch: 0 } };
      drawFrame(ctx, runtime.step(snapshotOf(c.look, c.shown, gaze), dt, t, fp), c.px, t);
    };
    draw(performance.now() / 1000);
    const off = subscribe(draw);
    return () => {
      off();
      // Scrolled away or turned still: a running fidget gives its slot back.
      fidget.stop();
    };
  });
</script>

<!-- The click is a reaction (a wink), not an action. -->
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
{#if animate}
  <span
    class="blob-avatar inline-block shrink-0"
    class:hatch={hatching}
    style:width="{size}px"
    style:height="{size}px"
    role="img"
    aria-label={title ?? "avatar"}
    title={title}
    data-kind="blob"
    data-mode="live"
    data-state={shown}
    onclick={onClick}
    onpointerenter={onHover}
  ><canvas bind:this={canvas} width={px} height={px} class="block" style:width="{size}px" style:height="{size}px" aria-hidden="true"></canvas></span>
{:else if src}
  <img
    {src}
    width={size}
    height={size}
    class="blob-avatar shrink-0"
    style:opacity={pose === "sleep" ? 0.55 : 1}
    alt={title ?? "avatar"}
    title={title}
    draggable="false"
    data-kind="blob"
    data-mode="still"
    data-state={shown}
  />
{:else}
  <!-- No canvas 2D (SSR / tests): a dot in the agent's color. -->
  <span
    class="blob-avatar inline-block shrink-0 rounded-full"
    style:width="{size * 0.84}px"
    style:height="{size * 0.84}px"
    style:margin="{size * 0.08}px"
    style:background-color={look.color}
    role="img"
    aria-label={title ?? "avatar"}
    data-kind="blob"
    data-mode="dot"
    data-state={shown}
  ></span>
{/if}

<style>
  /* A near-black blob would vanish on the dark theme: a faint rim keeps
     its outline. */
  :global(.dark) .blob-avatar {
    filter: drop-shadow(0 0 1px rgba(255, 255, 255, 0.35));
  }
  .blob-avatar.hatch {
    animation: blob-hatch 0.6s ease-out;
  }
  @keyframes blob-hatch {
    0% { transform: scale(0.4) rotate(-10deg); }
    60% { transform: scale(1.15) rotate(4deg); }
    100% { transform: none; }
  }
</style>
