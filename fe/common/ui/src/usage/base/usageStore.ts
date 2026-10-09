/* One client-side usage cache shared by every place that shows an
 * account's usage: the providers list, the usage panel, the /usage popover
 * and the composer's provider picker.
 *
 * It reads only the server's paced cache (usage_probe.go) — the server
 * decides whether an upstream call ever happens — and on top of that keeps
 * one copy per provider key in the browser so opening the picker never
 * fires a request per row or waits on one:
 *   - a fresh entry (younger than USAGE_TTL_MS) is served as-is;
 *   - a stale or missing entry is served as-is AND refreshed in the
 *     background, once: concurrent readers share the in-flight request;
 *   - surfaces that already hold a reading (the providers list polls the
 *     connections endpoint) seed it, so nobody refetches what is on screen. */

import { writable, type Readable } from "svelte/store";
import { parseSavedResets, type SavedResets, type WireSavedResets } from "./savedResets.js";

export type GlanceWindow = { key: string; utilization: number; resetsAt: string };

export type UsageGlance = {
  /* false = the type reports no usage; render nothing. */
  supported: boolean;
  /* true once a reading (or a definite failure) exists. */
  checked: boolean;
  windows: GlanceWindow[];
  savedResets: SavedResets | null;
  error: string;
  fetchedAt: string;
};

export type WireGlance = {
  supported?: boolean;
  windows?: { key?: string; utilization?: number; resets_at?: string }[] | null;
  saved_resets?: WireSavedResets | null;
  error?: string;
  pending?: boolean;
  fetched_at?: string;
};

export const USAGE_TTL_MS = 60_000;

/** glanceFromWire normalises any usage JSON surface carrying windows + saved_resets. */
export function glanceFromWire(w: WireGlance | null | undefined): UsageGlance {
  const windows = (w?.windows ?? []).map((x) => ({
    key: x.key ?? "",
    utilization: x.utilization ?? 0,
    resetsAt: x.resets_at ?? "",
  }));
  return {
    supported: w?.supported ?? false,
    checked: !w?.pending && (windows.length > 0 || !!w?.error || !!w?.fetched_at),
    windows,
    savedResets: parseSavedResets(w?.saved_resets),
    error: w?.error ?? "",
    fetchedAt: w?.fetched_at ?? "",
  };
}

type Entry = { glance: UsageGlance; at: number };

const state = writable<Record<string, Entry>>({});
const inflight = new Map<string, Promise<void>>();
let current: Record<string, Entry> = {};
state.subscribe((v) => (current = v));

/** seedUsage stores a reading another surface already fetched. */
export function seedUsage(key: string, glance: UsageGlance, at = Date.now()): void {
  state.update((m) => ({ ...m, [key]: { glance, at } }));
}

/** peekUsage returns what the cache holds, without fetching. */
export function peekUsage(key: string): UsageGlance | null {
  return current[key]?.glance ?? null;
}

/** loadUsage refreshes key in the background unless it is fresh or already
 *  in flight. base is the app's API base ("" for same-origin). force skips
 *  the freshness check (a Re-check just ran), never the in-flight dedup. */
export function loadUsage(base: string, key: string, force = false): Promise<void> {
  const e = current[key];
  if (!force && e && Date.now() - e.at < USAGE_TTL_MS) return Promise.resolve();
  const running = inflight.get(key);
  if (running) return running;
  const p = (async () => {
    try {
      const res = await fetch(`${base}/api/composer/usage?provider=${encodeURIComponent(key)}`, {
        headers: { Accept: "application/json" },
        credentials: "same-origin",
      });
      if (!res.ok) return; // keep whatever we had; the next read retries
      seedUsage(key, glanceFromWire((await res.json()) as WireGlance));
    } catch {
      /* offline / aborted: keep the last copy */
    } finally {
      inflight.delete(key);
    }
  })();
  inflight.set(key, p);
  return p;
}

/** usageStore is the readable map of every cached glance, keyed by "type/name". */
export const usageStore: Readable<Record<string, Entry>> = { subscribe: state.subscribe };

/** resetUsageStore clears the cache (tests). */
export function resetUsageStore(): void {
  inflight.clear();
  state.set({});
}
