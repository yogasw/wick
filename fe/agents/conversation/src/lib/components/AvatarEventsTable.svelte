<script lang="ts">
  /* Settings › Avatar: what the avatar does on each event (events.ts).
     A row can take another pose or expression, or be turned off: an off
     row falls back to the plain cues (orbit / thinking / alert / notify),
     never to a still avatar. Edits come back as the cleaned override, so
     a table set back to its defaults saves as none. */
  import { Toggle } from "@wick-fe/common-ui";
  import {
    AgentAvatar, AVATAR_EVENTS, AVATAR_EVENT_LABELS, AVATAR_STATES, BLOB_EXPRESSIONS, BLOB_STATES, DEFAULT_EVENT_POSES, IMAGE_POSES,
    classicPoseOf, cleanOverrides, imagePoseOf, isBlobKind, isImageKind, isImagePose, poseFor, type AvatarEvent, type AvatarImageView, type EventOverride, type EventPose, type EventOverrides,
  } from "@wick-fe/common-avatar";

  type Props = {
    kind?: string;
    shape: string;
    color: string;
    expression?: string;
    events?: EventOverrides | null;
    /** Style Image: the picture the rows move. */
    image?: AvatarImageView | null;
    onChange: (events: Record<string, EventOverride> | undefined) => void;
  };
  let { kind, shape, color, expression, events, image, onChange }: Props = $props();

  const blob = $derived(isBlobKind(kind));
  const pictured = $derived(isImageKind(kind));
  /* The classic avatar draws fewer states: a blob-only pose shows as the
     one it draws for it (classicPoseOf), while the stored override keeps
     the blob pose so switching style back loses nothing. A row with a
     classic default of its own (error → fail) shows that. The image
     avatar has its own 13 poses with their own defaults (imagePoseOf); a
     legacy pick (sleep, a blob-only pose) shows as its look-alike until
     the owner picks again. */
  const states = $derived<readonly string[]>(blob ? BLOB_STATES : pictured ? IMAGE_POSES : AVATAR_STATES.filter((s) => s !== "egg"));
  const shown = (ev: AvatarEvent, p: EventPose) => (blob ? p.state : pictured ? imagePoseOf(ev, events) : classicPoseOf(ev, p, events));
  /* The stored name behind a look-alike, as the select's title. An
     image-only pick (scan, type, …) is named as saved on every style: the
     blob and classic tables draw a stand-in for it. */
  function stored(ev: AvatarEvent, p: EventPose): string | undefined {
    const own = events?.[ev]?.state;
    if (pictured || (isImagePose(own) && !(BLOB_STATES as readonly string[]).includes(own))) {
      return own && own !== shown(ev, p) ? `Saved pose: ${own}` : undefined;
    }
    if (blob) return undefined;
    return shown(ev, p) !== p.state ? `Blob pose: ${p.state}` : undefined;
  }
  const select =
    "rounded-lg border border-white-300 bg-white-100 px-2 py-1 text-xs text-black-900 focus:border-green-500 focus:outline-none disabled:opacity-50 dark:border-navy-600 dark:bg-navy-800 dark:text-white-100";

  function set(ev: AvatarEvent, row: EventOverride) {
    onChange(cleanOverrides({ ...(events ?? {}), [ev]: row }, { classic: !blob, image: pictured, only: ev }));
  }
  function current(ev: AvatarEvent): EventOverride {
    return events?.[ev] ?? {};
  }
</script>

<div data-testid="avatar-events">
  <p class="text-sm font-semibold text-black-900 dark:text-white-100">Reacts to</p>
  <p class="mt-1 text-xs text-black-800 dark:text-black-600">
    What the avatar does while something happens, then it goes back to its own expression. Turn a row off to keep the plain working pose.
  </p>
  <div class="mt-3 overflow-hidden rounded-lg border border-white-300 dark:border-navy-600">
    <table class="w-full text-left text-xs">
      <thead class="bg-white-200 text-black-800 dark:bg-navy-700 dark:text-black-600">
        <tr>
          <th class="px-3 py-2 font-medium">Event</th>
          <th class="px-3 py-2 font-medium">Pose</th>
          {#if blob}<th class="px-3 py-2 font-medium">Expression</th>{/if}
          <th class="px-3 py-2 font-medium">On</th>
        </tr>
      </thead>
      <tbody>
        {#each AVATAR_EVENTS as ev (ev)}
          {@const row = current(ev)}
          {@const pose = poseFor(ev, events)}
          {@const d = DEFAULT_EVENT_POSES[ev]}
          <tr class="border-t border-white-300 dark:border-navy-600" data-testid="avatar-event-{ev}" data-off={row.off ? "1" : undefined}>
            <td class="px-3 py-2">
              <span class="flex items-center gap-2">
                <!-- live, not still: a still avatar never runs its ticker (classic)
                     and draws a cached frame (blob), so the preview never moved. -->
                <AgentAvatar {kind} {shape} {expression} {color} {image} {events} event={ev} size={28} live />
                <span class="text-black-900 dark:text-white-100">{AVATAR_EVENT_LABELS[ev]}</span>
              </span>
            </td>
            <td class="px-3 py-2">
              <!-- Classic shows a blob pose as its look-alike, or a row's classic
                   default (error → fail, drawn as alert) while the owner picked
                   none: the title names the stored blob pose either way. Image
                   lists its own poses; the title names a legacy saved one. -->
              <select
                class={select}
                aria-label="{AVATAR_EVENT_LABELS[ev]} pose"
                disabled={!pose}
                value={shown(ev, pose ?? d)}
                title={stored(ev, pose ?? d)}
                onchange={(e) => set(ev, { ...row, state: (e.currentTarget as HTMLSelectElement).value })}
              >
                {#each states as s (s)}<option value={s}>{s}</option>{/each}
              </select>
            </td>
            {#if blob}
              <td class="px-3 py-2">
                <select
                  class={select}
                  aria-label="{AVATAR_EVENT_LABELS[ev]} expression"
                  disabled={!pose}
                  value={pose ? (pose.expression ?? "") : (d.expression ?? "")}
                  onchange={(e) => set(ev, { ...row, expression: (e.currentTarget as HTMLSelectElement).value })}
                >
                  <option value="">own</option>
                  {#each BLOB_EXPRESSIONS as x (x)}<option value={x}>{x}</option>{/each}
                </select>
              </td>
            {/if}
            <td class="px-3 py-2">
              <Toggle checked={!row.off} onChange={(on) => set(ev, on ? { ...row, off: false } : { off: true })} label="{AVATAR_EVENT_LABELS[ev]} on" />
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if events && Object.keys(events).length}
    <button type="button" class="mt-2 text-xs text-green-600 hover:underline dark:text-green-400" data-testid="avatar-events-reset" onclick={() => onChange(undefined)}>Reset to defaults</button>
  {/if}
</div>
