/* What a session's next answer waits on, from the stream's "wait_state"
   event (the stream snapshot sends one on page open, the server a fresh
   one whenever it changes). Without it a turn stuck in the queue, or
   behind a host stalled on memory, read "spawning…" / "thinking…" for
   twenty minutes as if it were running. */

export type WaitState = {
  queue_pos: number;
  queue_len: number;
  restart_attempt?: number;
  restart_max?: number;
  provider?: string;
  used: number;
  max: number;
  global_used: number;
  global_max: number;
  hold: boolean;
  hold_reason?: string;
  mem_pct: number;
  cpu_pct: number;
  mem_pressure: number;
  cpu_pressure: number;
};

/** A turn this long without a stream event is called out. */
export const SILENT_AFTER_MS = 30_000;

export function parseWaitState(data: string | undefined): WaitState | null {
  try {
    const w = JSON.parse(data ?? "") as WaitState;
    return w && typeof w === "object" ? w : null;
  } catch {
    return null;
  }
}

export function formatSilence(ms: number): string {
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}

function slots(w: WaitState): string {
  if (!w.provider) return "";
  return ` · ${w.provider} ${w.used}/${w.max > 0 ? w.max : "∞"}`;
}

/* The host as the resource guard last saw it, only when it is strained:
   memory nearly full or stalling, CPU saturated, or new agents held. */
function host(w: WaitState | null): string {
  if (!w) return "";
  const strained = w.hold || w.mem_pressure >= 20 || w.mem_pct >= 90 || w.cpu_pct >= 95;
  if (!strained) return "";
  const why = w.hold_reason || `memory ${w.mem_pct}%, CPU ${w.cpu_pct}%`;
  return ` · host strained (${why})`;
}

export type WaitNote = {
  label: string;
  /** The note says what is happening instead of the activity label
      ("queued" is not "thinking"); otherwise it is appended to it. */
  replaces: boolean;
};

/** waitNote is what to tell the reader about a running turn, or null
    when it is simply working. silentMs: time since the last stream event. */
export function waitNote(w: WaitState | null, silentMs: number): WaitNote | null {
  if (w && w.queue_pos > 0) {
    return { label: `queued #${w.queue_pos}${slots(w)}${host(w)}`, replaces: true };
  }
  if (w && w.restart_attempt) {
    return { label: `restarting after no output, retry ${w.restart_attempt}/${w.restart_max ?? "?"}${host(w)}`, replaces: true };
  }
  if (silentMs < SILENT_AFTER_MS) return null;
  return { label: `no output for ${formatSilence(silentMs)}${host(w)}`, replaces: false };
}
