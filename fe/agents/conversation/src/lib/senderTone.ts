/**
 * Avatar tones for sender chips.
 *
 * A shared Slack thread puts several people in one conversation, and their
 * bubbles are otherwise identical. A stable colour per person makes the
 * thread scannable — you recognise who is talking before you read the name.
 *
 * Two properties matter and both come from hashing rather than assignment:
 *
 *  - Stable across reloads and sessions. The colour is derived from the key,
 *    not from arrival order, so the same person is the same colour in every
 *    render and on every device. Prefer passing the sender's platform ID:
 *    it survives a display-name change, which would otherwise reshuffle the
 *    palette mid-thread.
 *  - Never status-coloured. These are muted slate/amber/violet tones, kept
 *    clear of the green accent and of the pos/neg/warning ramps, so an
 *    avatar can never be misread as "this message failed".
 */

export type AvatarTone = {
  /** Tailwind classes for the avatar disc: background + text, both themes. */
  cls: string;
};

/* Six hues, each paired light/dark. Deliberately low-saturation: the chip
   sits beside a message, not in front of it.
   Only shades declared in tailwind.config.js are used — the palette there is
   trimmed to what the app actually renders, so an undeclared shade emits no
   CSS and the avatar would come out transparent. */
const TONES: AvatarTone[] = [
  { cls: "bg-slate-200 text-slate-700 dark:bg-slate-700 dark:text-slate-100" },
  { cls: "bg-sky-100 text-sky-800 dark:bg-sky-800 dark:text-sky-100" },
  { cls: "bg-violet-100 text-violet-800 dark:bg-violet-800 dark:text-violet-100" },
  { cls: "bg-amber-50 text-amber-800 dark:bg-amber-800 dark:text-amber-50" },
  { cls: "bg-teal-100 text-teal-800 dark:bg-teal-800 dark:text-teal-100" },
  { cls: "bg-indigo-100 text-indigo-700 dark:bg-indigo-700 dark:text-indigo-100" },
];

/**
 * avatarTone picks a stable tone for a sender key (their platform ID, or
 * their name when no ID is available).
 *
 * Uses FNV-1a — small, dependency-free, and well-spread over short strings
 * like "U0104" or "8812", which a naive charCode sum is not: sequential
 * Slack IDs would otherwise land on the same few buckets and hand a whole
 * team one colour.
 */
export function avatarTone(key: string): AvatarTone {
  const k = (key ?? "").trim();
  if (!k) return TONES[0];

  let hash = 0x811c9dc5;
  for (let i = 0; i < k.length; i++) {
    hash ^= k.charCodeAt(i);
    // FNV prime, via shifts so the result stays in 32-bit range.
    hash = (hash + ((hash << 1) + (hash << 4) + (hash << 7) + (hash << 8) + (hash << 24))) >>> 0;
  }
  return TONES[hash % TONES.length];
}
