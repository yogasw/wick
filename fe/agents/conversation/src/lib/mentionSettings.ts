/* The Mention tab of an agent's Settings: who may hand the agent a turn
   over the Team link and how many agent-to-agent turns an exchange it is
   in may take. The server enforces both (teamlink/policy.go); this file
   only words them. */

import type { MentionFrom } from "./api/team.js";

export const MAX_HOPS_MIN = 1;
export const MAX_HOPS_MAX = 10;
export const MAX_HOPS_DEFAULT = 4;

export const MENTION_FROM_OPTIONS: { value: MentionFrom; label: string; hint: string }[] = [
  { value: "all", label: "Any of my agents", hint: "Every agent in your Team can hand it a turn." },
  { value: "captain", label: "Only the Captain", hint: "Only your Captain can pull it in." },
  { value: "list", label: "Only agents I pick", hint: "Only the agents ticked below." },
  { value: "off", label: "Nobody", hint: "Other agents can't mention it, and it leaves the @ menu. You still can." },
];

/** mentionFromOf reads a stored value; anything unknown is "all", as on the server. */
export function mentionFromOf(v: string | null | undefined): MentionFrom {
  return v === "captain" || v === "list" || v === "off" ? v : "all";
}

/** clampHops keeps a typed cap in 1..10; empty/NaN reads as the default. */
export function clampHops(n: number | null | undefined): number {
  if (n == null || !Number.isFinite(n) || n <= 0) return MAX_HOPS_DEFAULT;
  return Math.min(MAX_HOPS_MAX, Math.max(MAX_HOPS_MIN, Math.round(n)));
}

/** hopsNote explains the cap in one line. */
export function hopsNote(n: number): string {
  const v = clampHops(n);
  return `After ${v} agent-to-agent turn${v === 1 ? "" : "s"} in a row the agent stops and says so — your reply starts a fresh count. In an exchange or group the smallest cap of the agents involved wins.`;
}
