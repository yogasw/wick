/*
 * Purpose:    Pure helper — computes idle countdown display text.
 * Caller:     ConversationHeader.svelte (idle lifecycle badge)
 * Dependencies: none
 * Main Functions: idleCountdownText
 * Side Effects: none
 */

export function idleCountdownText(atMs: number, idleTimeoutMs: number, nowMs: number): string {
  const remaining = Math.max(0, atMs + idleTimeoutMs - nowMs);
  if (remaining <= 0) return "0s";
  return `kill in ${Math.ceil(remaining / 1000)}s`;
}
