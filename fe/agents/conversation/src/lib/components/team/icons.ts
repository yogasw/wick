/* The small Team icons (16×16 grid, stroked in currentColor), taken from
   the grouped mockup's symbols so the delegation block, the tray, the
   Tasks tab and the roster draw the same glyphs. Each entry is the inner
   markup of an <svg viewBox="0 0 16 16"> (TeamIcon); constants only, never
   user input. */
export const TEAM_ICONS = {
  /** A person and a list: a task another agent sent (rail, roster mark). */
  tasks:
    '<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"><circle cx="5" cy="5" r="2"/><path d="M2 12a3 3 0 016 0"/><path d="M10 4.5h4M10 8h4M10 11.5h2.5"/></g>',
  /** An open arc: working (spin it). */
  spin: '<path d="M8 2a6 6 0 1 1-6 6" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>',
  check: '<path d="M3 8.5l3 3 7-7" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/>',
  /** Points down; rotate for a closed fold. */
  chevron: '<path d="M4 6l4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>',
  reply: '<path d="M6.5 4L2.5 8l4 4M2.5 8h7a4 4 0 014 4v.5" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>',
  /** A panel opening: open the teammate's chat. */
  open: '<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"><rect x="2" y="2.5" width="12" height="11" rx="2"/><path d="M10 2.5v11M6 6.5l1.8 1.5L6 9.5" stroke-linecap="round"/></g>',
  /** An arrow up: send an answer. */
  send: '<path d="M8 13V3M3.5 7.5L8 3l4.5 4.5" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"/>',
  msg: '<path d="M2.5 3.5h11v7h-6l-3 2.5v-2.5h-2z" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round"/>',
  /** An arrow down: jump to the message. */
  jump: '<path d="M8 3v9M4 8.5L8 12.5l4-4" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>',
  retry: '<path d="M13 8a5 5 0 11-1.6-3.7M13 2.5v3h-3" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>',
  /** Recovered after a restart. */
  recover: '<path d="M3 8a5 5 0 109-3M12 2v3H9" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>',
} as const;

export type TeamIconName = keyof typeof TEAM_ICONS;

export const TEAM_ICON_NAMES = Object.keys(TEAM_ICONS) as TeamIconName[];
