<script lang="ts">
  /* An image avatar: the picture inside a frame cut to its crop (round by
     default, rounded, hexagon or the plain square), placed by the stored
     numbers (image.ts). The ring is the frame's box-shadow; a hexagon's
     clip would cut that off, so it gets an outline drawn under the
     picture instead (3px, half hidden by it: 1.5px shows, as the ring).
     Poses move the frame from outside — the mockup's AvatarFx set: 13
     poses (image.ts IMAGE_POSES), each with its own motion and, for some,
     a mark drawn around the frame (ring, orbit, dots, scan line, cursor,
     ping, badge) — so the picture itself never changes and the clip is
     never touched. sleep (disabled) greys it, egg (hatching) pops it in.
     `animate` off (still, reduced motion) is one frame: every mark stays,
     still, so each pose reads apart from idle without motion. */
  import { CROP_PATHS, cropFrameStyle, imageAvatarPose, imageCrop, imageTransform, safeImageSrc, type AvatarImageView } from "./image.js";

  type Props = {
    image: AvatarImageView; size?: number; pose?: string; animate?: boolean; title?: string; color?: string;
    /** The picture failed to load (deleted, 404): the caller draws classic. */
    onerror?: () => void;
  };
  let { image, size = 40, pose = "idle", animate = false, title, color = "#27b199", onerror }: Props = $props();

  const src = $derived(safeImageSrc(image.src));
  const ring = $derived(/^#[0-9a-f]{3,8}$/i.test(color) ? color : "#27b199");
  const crop = $derived(imageCrop(image.crop));
  /* An unknown or legacy name draws its look-alike (imageAvatarPose). */
  const p = $derived(imageAvatarPose(pose));
</script>

<span
  class="image-avatar relative inline-block shrink-0"
  class:animate
  style:width="{size}px"
  style:height="{size}px"
  style:--av-size="{size}px"
  style:--av-c={ring}
  data-pose={p}
  data-testid="image-avatar"
  role="img"
  aria-label={title ?? "avatar"}
>
  <span class="frame absolute inset-0" data-crop={crop}>
    {#if crop === "hex"}
      <svg class="pointer-events-none absolute inset-0 h-full w-full overflow-visible" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
        <path d={CROP_PATHS.hex} fill="none" stroke={ring} stroke-width="3" vector-effect="non-scaling-stroke" stroke-linejoin="round" />
      </svg>
    {/if}
    <span class="clip absolute inset-0 overflow-hidden bg-white-200 dark:bg-navy-700" style={cropFrameStyle(crop)} style:box-shadow={crop === "hex" ? undefined : `0 0 0 1.5px ${ring}`}>
      {#if src}
        <img {src} alt="" draggable="false" onerror={() => onerror?.()} class="h-full w-full select-none object-contain" style:transform={imageTransform(image)} />
      {/if}
    </span>
  </span>
  {#if p === "orbit"}<span class="fx orbit" data-fx="orbit"></span>{/if}
  {#if p === "thinking"}<span class="fx dots" data-fx="dots"><b></b><b></b><b></b></span>{/if}
  {#if p === "scan"}<span class="fx scan" data-fx="scan"></span>{/if}
  {#if p === "type"}<span class="fx cur" data-fx="cur"></span>{/if}
  {#if p === "send"}<span class="fx send" data-fx="send"></span>{/if}
  {#if p === "wait"}<span class="fx wave" data-fx="wave"></span>{/if}
  {#if p === "alert" || p === "fail"}<span class="fx ring" data-fx="ring"></span>{/if}
  {#if p === "notify"}<span class="fx ping" data-fx="ping"></span>{/if}
  {#if p === "alert" || p === "fail"}
    <span class="fx badge border-white-100 dark:border-navy-800" data-fx="badge" aria-hidden="true">{p === "fail" ? "!" : "?"}</span>
  {/if}
</span>

<style>
  .image-avatar { --av-warn: #f59e0b; }
  .image-avatar[data-pose="fail"] { --av-warn: #ef4444; }
  .image-avatar[data-pose="sleep"] .frame { filter: grayscale(1); opacity: 0.5; }
  .image-avatar[data-pose="wide"] .frame { filter: brightness(1.12); }
  .image-avatar[data-pose="fail"] .frame { filter: drop-shadow(0 0 6px #ef4444) saturate(0.8); }
  .frame { transform-origin: 50% 95%; }
  .image-avatar[data-pose="swirl"] .frame, .image-avatar[data-pose="wide"] .frame, .image-avatar[data-pose="squeeze"] .frame { transform-origin: 50% 50%; }

  .image-avatar.animate[data-pose="idle"] .frame { animation: img-av-idle 3.2s ease-in-out infinite; }
  .image-avatar.animate[data-pose="wide"] .frame { animation: img-av-wide 1.6s ease-in-out infinite; }
  .image-avatar.animate[data-pose="scan"] .frame { animation: img-av-look 1.4s ease-in-out infinite alternate; }
  .image-avatar.animate[data-pose="thinking"] .frame { animation: img-av-tilt 2.4s ease-in-out infinite; }
  .image-avatar.animate[data-pose="type"] .frame { animation: img-av-type 0.6s linear infinite; }
  .image-avatar.animate[data-pose="orbit"] .frame { animation: img-av-wob 1.6s ease-in-out infinite; }
  .image-avatar.animate[data-pose="send"] .frame { animation: img-av-lean 1.1s ease-in-out infinite; }
  .image-avatar.animate[data-pose="wait"] .frame { animation: img-av-breath 2.2s ease-in-out infinite; }
  .image-avatar.animate[data-pose="alert"] .frame { animation: img-av-shake 0.45s ease-in-out infinite; }
  .image-avatar.animate[data-pose="fail"] .frame { animation: img-av-fail 1.6s ease-in-out infinite; }
  .image-avatar.animate[data-pose="notify"] .frame { animation: img-av-hop 1.8s ease-out infinite; }
  .image-avatar.animate[data-pose="swirl"] .frame { animation: img-av-swirl 1.8s ease-in-out infinite; }
  .image-avatar.animate[data-pose="squeeze"] .frame { animation: img-av-squeeze 1.2s ease-in-out infinite; }
  .image-avatar.animate[data-pose="egg"] .frame { animation: img-av-hatch 0.8s ease-out; }

  /* Marks around the frame, sized off the avatar (--av-size). */
  .fx { position: absolute; pointer-events: none; }
  .orbit { inset: -8%; border-radius: 9999px; border: 1.5px dashed color-mix(in srgb, var(--av-c) 45%, transparent); }
  /* The dot rides the dashed line itself (centred on it), not the
     picture's edge: there it sat on the ring and its glow read as a
     clipped smudge beside a small (roster) avatar. */
  .orbit::after {
    content: ""; position: absolute; top: -0.75px; left: 50%;
    width: max(4px, calc(var(--av-size) * 0.08)); aspect-ratio: 1; transform: translate(-50%, -50%);
    border-radius: 9999px; background: var(--av-c); box-shadow: 0 0 3px var(--av-c);
  }
  .dots { top: -6%; right: -4%; display: flex; gap: max(1px, 4%); }
  .dots b { width: max(3px, calc(var(--av-size) * 0.07)); aspect-ratio: 1; border-radius: 9999px; background: var(--av-c); }
  .scan { top: 10%; bottom: 10%; left: 12%; width: max(2px, calc(var(--av-size) * 0.035)); border-radius: 2px; background: linear-gradient(transparent, var(--av-c), transparent); box-shadow: 0 0 6px var(--av-c); }
  .cur { right: 2%; bottom: 8%; width: max(2px, calc(var(--av-size) * 0.07)); height: 22%; border-radius: 1px; background: var(--av-c); }
  .send { top: 44%; left: 44%; width: max(4px, calc(var(--av-size) * 0.11)); aspect-ratio: 1; border-radius: 9999px; background: var(--av-c); box-shadow: 0 0 6px var(--av-c); }
  .wave { inset: 0; border-radius: 9999px; border: max(2px, calc(var(--av-size) * 0.03)) solid var(--av-c); }
  .ring { inset: -8%; border-radius: 9999px; border: max(1.5px, calc(var(--av-size) * 0.03)) solid var(--av-warn); }
  .ping { top: -8%; left: 50%; width: max(5px, calc(var(--av-size) * 0.12)); aspect-ratio: 1; transform: translateX(-50%); border-radius: 9999px; background: #fff; box-shadow: 0 0 0 2px color-mix(in srgb, var(--av-c) 60%, transparent); }
  .badge {
    right: -6%; bottom: -4%; width: 30%; min-width: 10px; aspect-ratio: 1; border-radius: 9999px; border-style: solid; border-width: max(1px, calc(var(--av-size) * 0.03));
    display: grid; place-items: center; background: var(--av-warn); color: #1b1300; font: 800 max(7px, calc(var(--av-size) * 0.17)) / 1 system-ui, sans-serif;
  }
  .image-avatar[data-pose="fail"] .badge { color: #fff; }

  .image-avatar.animate .orbit { animation: img-av-spin 1.6s linear infinite; }
  .image-avatar.animate .dots b { animation: img-av-dot 1.2s infinite; }
  .image-avatar.animate .dots b:nth-child(2) { animation-delay: 0.15s; }
  .image-avatar.animate .dots b:nth-child(3) { animation-delay: 0.3s; }
  .image-avatar.animate .scan { animation: img-av-sweep 1.4s ease-in-out infinite alternate; }
  .image-avatar.animate .cur { animation: img-av-blink 0.8s steps(2, start) infinite; }
  .image-avatar.animate .send { animation: img-av-fly 1.1s ease-out infinite; }
  .image-avatar.animate .wave { animation: img-av-ripple 2.2s ease-out infinite; }
  .image-avatar.animate .ring { animation: img-av-pulse 1.6s ease-out infinite; }

  @media (prefers-reduced-motion: reduce) {
    .image-avatar .frame, .image-avatar .fx, .image-avatar .fx b { animation: none !important; }
  }

  @keyframes img-av-idle { 0%, 100% { transform: translateY(0) scale(1); } 50% { transform: translateY(-4%) scale(1.02); } }
  @keyframes img-av-wide { 0%, 12%, 100% { transform: scale(1); } 28%, 68% { transform: scale(1.14); } }
  @keyframes img-av-look { from { transform: translateX(-8%) rotate(-5deg); } to { transform: translateX(8%) rotate(5deg); } }
  @keyframes img-av-tilt { 0%, 100% { transform: rotate(-5deg); } 50% { transform: rotate(6deg); } }
  @keyframes img-av-type { 0%, 50%, 100% { transform: translateY(0); } 25% { transform: translateY(-5%); } 75% { transform: translateY(-3%) rotate(1.5deg); } }
  @keyframes img-av-wob { 0%, 100% { transform: scale(1, 1); } 30% { transform: scale(1.06, 0.94); } 60% { transform: scale(0.96, 1.05); } }
  @keyframes img-av-lean { 0%, 100% { transform: rotate(0) translate(0, 0); } 35% { transform: rotate(9deg) translate(4%, -4%); } 60% { transform: rotate(-2deg); } }
  @keyframes img-av-breath { 0%, 100% { transform: scale(0.94); opacity: 0.7; } 50% { transform: scale(1); opacity: 1; } }
  @keyframes img-av-shake { 25% { transform: rotate(-5deg); } 75% { transform: rotate(5deg); } }
  @keyframes img-av-fail { 0%, 60%, 100% { transform: translateX(0); } 8% { transform: translateX(-9%); } 16% { transform: translateX(9%); } 24% { transform: translateX(-7%); } 32% { transform: translateX(7%); } 40% { transform: translateX(-4%); } 48% { transform: translateX(0); } }
  @keyframes img-av-hop { 0%, 45%, 100% { transform: translateY(0) scale(1, 1); } 10% { transform: translateY(4%) scale(1.06, 0.92); } 25% { transform: translateY(-14%) scale(0.96, 1.05); } 38% { transform: translateY(0) scale(1.04, 0.96); } }
  @keyframes img-av-swirl { 0% { transform: rotate(0) scale(1); } 50% { transform: rotate(180deg) scale(0.78); } 100% { transform: rotate(360deg) scale(1); } }
  @keyframes img-av-squeeze { 0%, 100% { transform: scale(1, 1); } 45% { transform: scale(0.72, 1.14); } 70% { transform: scale(1.08, 0.94); } }
  @keyframes img-av-hatch { 0% { transform: scale(0) rotate(-20deg); opacity: 0; } 60% { transform: scale(1.12) rotate(6deg); opacity: 1; } 100% { transform: scale(1); } }
  @keyframes img-av-spin { to { transform: rotate(360deg); } }
  @keyframes img-av-dot { 0%, 80%, 100% { transform: translateY(0); opacity: 0.5; } 40% { transform: translateY(-70%); opacity: 1; } }
  @keyframes img-av-sweep { from { left: 12%; } to { left: 84%; } }
  @keyframes img-av-blink { 0% { opacity: 1; } 50% { opacity: 0; } }
  @keyframes img-av-fly { 0% { transform: translate(0, 0) scale(1); opacity: 1; } 100% { transform: translate(calc(var(--av-size) * 0.65), calc(var(--av-size) * -0.65)) scale(0.3); opacity: 0; } }
  @keyframes img-av-ripple { 0% { transform: scale(0.95); opacity: 0.7; } 100% { transform: scale(1.45); opacity: 0; } }
  @keyframes img-av-pulse { 0% { opacity: 0.9; transform: scale(0.95); } 100% { opacity: 0; transform: scale(1.2); } }
</style>
