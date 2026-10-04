<script lang="ts">
  /* Agent avatar: a coloured blob (circle / squircle / triangle / diamond)
     with two dark eyes. It is alive rather than a still picture (PLAN 6.5,
     phase 1a): idle breathes and blinks, thinking wobbles and looks up,
     orbit (a tool is running) wobbles with a dot circling it, alert opens wide, notify plays once when the agent finishes a turn, a
     disabled agent sleeps and a new one hatches from an egg. The eyes
     follow the pointer, hover makes it attentive and a click winks.

     Every avatar on the page rides one shared rAF and one pointermove
     listener (avatarTicker.ts), paused while the tab is hidden. Under
     prefers-reduced-motion (or with `still`) nothing subscribes and the
     still frame is drawn.

     kind="blob" hands drawing to BlobAvatar (the vendored blob mascot):
     still PNG by default, animated only where `live` is set. The state
     logic below (working → thinking, notify after a turn, …) is shared. */
  import BlobAvatar from "./BlobAvatar.svelte";
  import { isBlobKind, blobStateFor } from "./blob.js";
  import { blobPath, eyesAt, orbitDot, gazeTarget, approach, followsPointer, normalizeShape, normalizeState, stateFor, DOT_R, type AvatarState, type Vec } from "./shape.js";
  import { subscribe, pointer, pointerActive, prefersReducedMotion } from "./ticker.js";
  import { createFidget, type FidgetPose } from "./blob/motion/fidget";
  import { idleAnimationsOn } from "./idle.js";

  type Props = {
    /** "" / absent = classic, "blob" = blob mascot (team.Avatar.kind). */
    kind?: string;
    shape?: string;
    /** Blob only: one of BLOB_EXPRESSIONS. */
    expression?: string;
    color?: string;
    size?: number;
    working?: boolean;
    /** The running turn is on a tool: orbit instead of thinking. */
    tool?: boolean;
    /** Greyed and eyes closed: a disabled agent. */
    asleep?: boolean;
    /** Just created: drawn as an egg that pops into the agent. */
    hatching?: boolean;
    /** Needs the user's attention. */
    alert?: boolean;
    /** Has something new to read. */
    notify?: boolean;
    /** Force a state (Settings preview); otherwise derived from the flags. */
    pose?: AvatarState;
    /** Draw one still frame and never animate (cards, dense lists). */
    still?: boolean;
    /** Animate this one (roster, header, empty state, preview). A blob
        without it is a cached still frame, so dense lists stay cheap; a
        classic one without it skips the fidgets. */
    live?: boolean;
    /** Waiting on something (a chat loading): a live avatar fidgets
        often, a random bit each time (blob/motion/fidget.ts). */
    restless?: boolean;
    title?: string;
  };
  let {
    kind,
    shape = "circle",
    expression,
    color = "#6366f1",
    size = 40,
    working = false,
    tool = false,
    asleep = false,
    hatching = false,
    alert = false,
    notify = false,
    pose,
    still = false,
    live = false,
    restless = false,
    title,
  }: Props = $props();

  const blob = $derived(isBlobKind(kind));

  const NOTIFY_MS = 2400;
  const WINK_MS = 450;

  const s = $derived(normalizeShape(shape));
  const motionReduced = prefersReducedMotion();
  const reduced = $derived(still || motionReduced);

  /* "Pesan masuk dari agent ini → notify sekali": a finished turn plays
     notify for a moment, without needing an unread field. */
  let justReplied = $state(false);
  let wasWorking = false;
  let replyTimer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const w = working;
    if (wasWorking && !w && !asleep) {
      justReplied = true;
      clearTimeout(replyTimer);
      replyTimer = setTimeout(() => (justReplied = false), NOTIFY_MS);
    }
    wasWorking = w;
  });
  $effect(() => () => clearTimeout(replyTimer));

  const current = $derived<AvatarState>(
    pose ? normalizeState(pose) : stateFor({ working, tool, asleep, hatching, alert, notify: notify || justReplied }),
  );

  let hover = $state(false);
  let wink = $state(false);
  let winkTimer: ReturnType<typeof setTimeout> | undefined;
  function onClick() {
    if (current === "sleep" || current === "egg") return;
    wink = true;
    clearTimeout(winkTimer);
    winkTimer = setTimeout(() => (wink = false), WINK_MS);
  }
  $effect(() => () => clearTimeout(winkTimer));

  let svgEl: SVGSVGElement | undefined = $state();
  let t = $state(0);
  let gaze = $state<Vec>({ x: 0, y: 0 });

  /* A live classic avatar fidgets on the same schedule as the blob one:
     a glance or a wink moves the eyes, a hop or a tilt the whole body. */
  const fidget = createFidget({ now: typeof performance === "undefined" ? 0 : performance.now() / 1000 });
  $effect(() => () => fidget.stop());
  let fp = $state<FidgetPose | null>(null);

  $effect(() => {
    if (reduced || blob) return;
    const off = subscribe((now) => {
      t = now;
      const next = live
        ? fidget.step({ time: now, size, busy: current !== "idle" || hover, enabled: idleAnimationsOn(), hidden: document.visibilityState === "hidden", restless })
        : null;
      if (next !== fp) fp = next;
      let target: Vec = { x: 0, y: 0 };
      const look = fp?.gaze;
      if (look && look !== "cursor") target = gazeTarget(look.yaw * 6, look.pitch * 6);
      else if (svgEl && (look === "cursor" || followsPointer(current)) && pointerActive(performance.now())) {
        const r = svgEl.getBoundingClientRect();
        target = gazeTarget(pointer.x - (r.left + r.width / 2), pointer.y - (r.top + r.height / 2));
      }
      // Settled: skip the write so an idle avatar does not re-render the eyes for nothing.
      const g = approach(gaze, target);
      if (Math.abs(g.x - gaze.x) > 1e-4 || Math.abs(g.y - gaze.y) > 1e-4) gaze = g;
    });
    return () => {
      off();
      fidget.stop();
    };
  });
  // The body part of a fidget: a hop lifts it, a tilt leans it.
  const body = $derived(
    fp?.motion ? `translate(0 ${((fp.motion.bounce ?? 0) * 0.02).toFixed(3)}) rotate(${(((fp.motion.tilt ?? 0) * 180) / Math.PI).toFixed(2)})` : "",
  );

  const path = $derived(blobPath(s, current, t, !reduced));
  const eyes = $derived(eyesAt(current, t, gaze, { animate: !reduced, hover, wink: wink || (fp?.wink ?? 0) > 0.5 }));
  const dot = $derived(orbitDot(current, t, !reduced));
  const fill = $derived(/^#[0-9a-f]{3,8}$/i.test(color) ? color : "#6366f1");
</script>

{#if blob}
  <BlobAvatar {shape} {expression} {color} {size} pose={blobStateFor(current)} {live} {restless} {still} hatching={hatching && !reduced} {title} />
{:else}
<!-- The click is a reaction, not an action: the row button around the
     avatar does the navigating, so there is nothing for a key to trigger. -->
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<svg
  bind:this={svgEl}
  width={size}
  height={size}
  viewBox="-1.4 -1.4 2.8 2.8"
  class="agent-avatar shrink-0 overflow-visible"
  class:hatch={hatching && !reduced}
  class:lift={hover && !reduced && current !== "sleep"}
  style:opacity={current === "sleep" ? 0.45 : 1}
  role="img"
  aria-label={title ?? "avatar"}
  data-state={current}
  onpointerenter={() => (hover = true)}
  onpointerleave={() => (hover = false)}
  onclick={onClick}
>
  {#if title}<title>{title}</title>{/if}
  <g transform={body} data-fidget={fidget.current || undefined}>
  <path d={path} fill={fill} transform="translate({(gaze.x * 0.35).toFixed(3)} {(gaze.y * 0.35).toFixed(3)})" />
  {#each eyes as e, i (i)}
    <ellipse cx={e.cx.toFixed(3)} cy={e.cy.toFixed(3)} rx={e.rx} ry={e.ry.toFixed(3)} fill="#16181d" opacity="0.88" />
  {/each}
  </g>
  {#if dot}<circle cx={dot.x.toFixed(3)} cy={dot.y.toFixed(3)} r={DOT_R} fill="#27b199" />{/if}
</svg>
{/if}

<style>
  .agent-avatar {
    transition: transform 0.18s ease-out;
    transform-origin: center;
  }
  .agent-avatar.lift {
    transform: scale(1.06);
  }
  .agent-avatar.hatch {
    animation: agent-hatch 0.6s ease-out;
  }
  @keyframes agent-hatch {
    0% { transform: scale(0.4) rotate(-10deg); }
    60% { transform: scale(1.15) rotate(4deg); }
    100% { transform: none; }
  }
</style>
