<script lang="ts">
  /* An image avatar: the picture inside a round frame, cropped by the
     stored numbers (image.ts). Poses move the frame from outside — the
     same motion names as the mockup's afx-* set — so the picture itself
     never changes. `animate` off (still, reduced motion) is one frame. */
  import { imageTransform, safeImageSrc, type AvatarImageView } from "./image.js";

  type Props = {
    image: AvatarImageView; size?: number; pose?: string; animate?: boolean; title?: string; color?: string;
    /** The picture failed to load (deleted, 404): the caller draws classic. */
    onerror?: () => void;
  };
  let { image, size = 40, pose = "idle", animate = false, title, color = "#27b199", onerror }: Props = $props();

  const src = $derived(safeImageSrc(image.src));
  const ring = $derived(/^#[0-9a-f]{3,8}$/i.test(color) ? color : "#27b199");
</script>

<span
  class="image-avatar relative inline-block shrink-0"
  class:animate
  style:width="{size}px"
  style:height="{size}px"
  data-pose={pose}
  data-testid="image-avatar"
  role="img"
  aria-label={title ?? "avatar"}
>
  <span class="frame absolute inset-0 overflow-hidden rounded-full bg-white-200 dark:bg-navy-700" style:box-shadow="0 0 0 1.5px {ring}">
    {#if src}
      <img {src} alt="" draggable="false" onerror={() => onerror?.()} class="h-full w-full select-none object-contain" style:transform={imageTransform(image)} />
    {/if}
  </span>
  {#if animate && (pose === "orbit" || pose === "thinking")}<span class="dot" style:background-color={ring}></span>{/if}
</span>

<style>
  .image-avatar[data-pose="sleep"] .frame { filter: grayscale(1); opacity: 0.5; }
  .image-avatar.animate[data-pose="idle"] .frame { animation: img-av-idle 3.2s ease-in-out infinite; }
  .image-avatar.animate[data-pose="thinking"] .frame { animation: img-av-tilt 1.6s ease-in-out infinite; }
  .image-avatar.animate[data-pose="orbit"] .frame { animation: img-av-wob 1.2s ease-in-out infinite; }
  .image-avatar.animate[data-pose="alert"] .frame { animation: img-av-shake 0.5s ease-in-out infinite; }
  .image-avatar.animate[data-pose="notify"] .frame { animation: img-av-hop 1.4s ease-out infinite; }
  .image-avatar.animate[data-pose="egg"] .frame { animation: img-av-hatch 0.8s ease-out; }
  .dot {
    position: absolute; top: 50%; left: 50%; width: 14%; height: 14%; margin: -7%;
    border-radius: 9999px; animation: img-av-spin 1.4s linear infinite; transform-origin: 50% 50%;
  }
  @keyframes img-av-idle { 0%, 100% { transform: translateY(0) scale(1); } 50% { transform: translateY(-4%) scale(1.02); } }
  @keyframes img-av-tilt { 0%, 100% { transform: rotate(-5deg); } 50% { transform: rotate(6deg); } }
  @keyframes img-av-wob { 0%, 100% { transform: scale(1, 1); } 30% { transform: scale(1.06, 0.94); } 60% { transform: scale(0.96, 1.05); } }
  @keyframes img-av-shake { 25% { transform: rotate(-5deg); } 75% { transform: rotate(5deg); } }
  @keyframes img-av-hop { 0%, 45%, 100% { transform: translateY(0) scale(1, 1); } 10% { transform: translateY(4%) scale(1.06, 0.92); } 25% { transform: translateY(-14%) scale(0.96, 1.05); } }
  @keyframes img-av-hatch { 0% { transform: scale(0) rotate(-20deg); opacity: 0; } 60% { transform: scale(1.12) rotate(6deg); opacity: 1; } 100% { transform: scale(1); } }
  @keyframes img-av-spin { from { transform: rotate(0) translateX(380%); } to { transform: rotate(360deg) translateX(380%); } }
</style>
