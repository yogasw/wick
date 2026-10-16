<script lang="ts">
  import type { Snippet } from "svelte";
  import { prefersReducedMotion } from "@wick-fe/common-avatar";
  import type { AvatarActivity } from "../../avatarActivity.js";

  /* The busy cues around another session's avatar (mockup `.av` states),
     CSS only: no timer and no per-frame JS, so a long roster costs nothing
     while idle (the animations still run off screen or in a hidden tab,
     compositor-only transforms, one element per busy avatar). tool wobbles
     with an orbit, remote spins a dashed ring, alert pulses an amber ring.
     The orbit needs room, so a small avatar (chips, the tray) only wobbles.
     thinking draws nothing here: no task reaches it, and the roster's live
     avatar acts it out itself. rings = the avatar inside already acts out
     its work (the live roster avatar): only the remote and alert rings are
     added, and the caller turns the avatar's own remote cue off while the
     ring shows. Colours from the wick palette: green-400, amber-500 (it has
     no amber-400). Reduced motion keeps every cue, standing still. */
  type Props = {
    activity: AvatarActivity;
    size: number;
    rings?: boolean;
    children: Snippet;
  };
  let { activity, size, rings = false, children }: Props = $props();

  const still = prefersReducedMotion();
  const roomy = $derived(size >= 24 && !rings);
  const wobble = $derived(!rings && activity === "tool");
</script>

<span
  class="av-act relative inline-flex shrink-0"
  class:still
  class:alert={activity === "alert"}
  data-testid="avatar-activity"
  data-activity={activity}
  data-still={still || undefined}
>
  <span class="av-body inline-flex" class:wobble>{@render children()}</span>
  {#if activity === "tool" && roomy}<span class="av-orbit" aria-hidden="true" data-testid="avatar-orbit"></span>{/if}
  {#if activity === "remote"}<span class="av-remote" aria-hidden="true" data-testid="avatar-remote"></span>{/if}
</span>

<style>
  .av-body.wobble {
    animation: av-wob 1.6s ease-in-out infinite;
    transform-origin: 50% 80%;
  }
  @keyframes av-wob {
    0%, 100% { transform: scale(1, 1); }
    30% { transform: scale(1.05, 0.95); }
    60% { transform: scale(0.97, 1.04); }
  }
  .av-orbit {
    pointer-events: none;
    position: absolute;
    inset: -3px;
    border-radius: 999px;
    animation: av-spin 1.4s linear infinite;
  }
  .av-orbit::after {
    content: "";
    position: absolute;
    top: -1px;
    left: 50%;
    width: 6px;
    height: 6px;
    margin-left: -3px;
    border-radius: 999px;
    background: #6EC5B2;
    box-shadow: 0 0 6px #6EC5B2;
  }
  @keyframes av-spin {
    to { transform: rotate(360deg); }
  }
  .av-act.alert::before {
    content: "";
    pointer-events: none;
    position: absolute;
    inset: -3px;
    border-radius: 999px;
    border: 2px solid #f59e0b;
    animation: av-pulse 1.6s ease-out infinite;
  }
  @keyframes av-pulse {
    0% { opacity: 0.9; transform: scale(0.95); }
    100% { opacity: 0; transform: scale(1.25); }
  }
  .av-remote {
    pointer-events: none;
    position: absolute;
    inset: -3px;
    border-radius: 999px;
    border: 1.5px dashed #6EC5B2;
    animation: av-spin 6s linear infinite;
  }
  /* Reduced motion: the same cues, still. The pulse ring stays visible
     instead of fading out. */
  .av-act.still .av-body,
  .av-act.still .av-orbit,
  .av-act.still .av-remote,
  .av-act.still.alert::before {
    animation: none;
  }
  @media (prefers-reduced-motion: reduce) {
    .av-body,
    .av-orbit,
    .av-remote,
    .av-act.alert::before {
      animation: none !important;
    }
  }
</style>
