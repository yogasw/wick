/* Settings › Session: bounds of "compact when idle for N hours", mirroring
   team.MinIdleHours / MaxIdleHours / DefaultIdleHours on the server. */
export const MIN_IDLE_HOURS = 1;
export const MAX_IDLE_HOURS = 168;
export const DEFAULT_IDLE_HOURS = 8;

/** clampIdleHours rounds and clamps a typed value; empty or junk → default. */
export function clampIdleHours(v: unknown): number {
  const n = Math.round(Number(v));
  if (!Number.isFinite(n) || n === 0) return DEFAULT_IDLE_HOURS;
  return Math.min(MAX_IDLE_HOURS, Math.max(MIN_IDLE_HOURS, n));
}
