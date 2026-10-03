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
     still frame is drawn. */
  import { blobPath, eyesAt, orbitDot, gazeTarget, approach, followsPointer, normalizeShape, normalizeState, stateFor, DOT_R, type AvatarState, type Vec } from "./shape.js";
  import { subscribe, pointer, pointerActive, prefersReducedMotion } from "./ticker.js";

  type Props = {
    shape?: string;
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
    title?: string;
  };
  let {
    shape = "circle",
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
    title,
  }: Props = $props();

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

  $effect(() => {
    if (reduced) return;
    return subscribe((now) => {
      t = now;
      let target: Vec = { x: 0, y: 0 };
      if (svgEl && followsPointer(current) && pointerActive(performance.now())) {
        const r = svgEl.getBoundingClientRect();
        target = gazeTarget(pointer.x - (r.left + r.width / 2), pointer.y - (r.top + r.height / 2));
      }
      const next = approach(gaze, target);
      // Settled: skip the write so an idle avatar does not re-render the eyes for nothing.
      if (Math.abs(next.x - gaze.x) > 1e-4 || Math.abs(next.y - gaze.y) > 1e-4) gaze = next;
    });
  });

  const path = $derived(blobPath(s, current, t, !reduced));
  const eyes = $derived(eyesAt(current, t, gaze, { animate: !reduced, hover, wink }));
  const dot = $derived(orbitDot(current, t, !reduced));
  const fill = $derived(/^#[0-9a-f]{3,8}$/i.test(color) ? color : "#6366f1");
</script>

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
  <path d={path} fill={fill} transform="translate({(gaze.x * 0.35).toFixed(3)} {(gaze.y * 0.35).toFixed(3)})" />
  {#each eyes as e, i (i)}
    <ellipse cx={e.cx.toFixed(3)} cy={e.cy.toFixed(3)} rx={e.rx} ry={e.ry.toFixed(3)} fill="#16181d" opacity="0.88" />
  {/each}
  {#if dot}<circle cx={dot.x.toFixed(3)} cy={dot.y.toFixed(3)} r={DOT_R} fill="#27b199" />{/if}
</svg>

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
