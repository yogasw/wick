import type { DataFailure, DataReason, InstanceRef, Overview, Status, WatchdogState } from "./types.js";

// Pure presentation logic for the Overview tab. It lives here rather than in
// the component so the rules that actually decide what the user is told —
// which warning fires, what "unknown" means, whether an instance records —
// can be tested without mounting anything.

// ── daemon ───────────────────────────────────────────────────────────

export type Badge = { text: string; cls: string };

// badgeFor maps the daemon state onto its pill.
//
// Wick palette only, and only shades the theme actually defines: `black` stops
// at 600 and there is no amber-400/600, so the transitional state is a lighter
// green rather than a class that would be purged and render as inherited text.
export function badgeFor(st: Status | undefined): Badge {
  switch (st?.state) {
    case "running":
      return { text: "Running", cls: "text-green-600 dark:text-green-300" };
    case "starting":
      return { text: "Starting…", cls: "text-green-700 dark:text-green-400" };
    case "not-installed":
      return { text: "Not installed", cls: "text-black-800 dark:text-black-600" };
    default:
      return { text: "Stopped", cls: "text-black-800 dark:text-black-600" };
  }
}

// dotFor is the switcher tile's status dot. The transitional dot pulses, so
// "starting" is told apart from "running" by motion as well as by shade.
export function dotFor(st: Status | undefined): string {
  switch (st?.state) {
    case "running":
      return "bg-green-500";
    case "starting":
      return "bg-green-400 animate-pulse";
    default:
      return "bg-white-400 dark:bg-navy-500";
  }
}

// uptimeOf renders how long the daemon has been up.
//
// The server only times a daemon WICK spawned. An adopted one — started by
// hand, or outliving a wick restart — reports no start time, and this says
// "unknown" instead of inventing one from the moment the page loaded.
export function uptimeOf(st: Status | undefined, now: number = Date.now()): string {
  if (!st?.running) return "—";
  const started = st.started_at_ms ?? 0;
  if (started <= 0) return "unknown (not started by wick)";
  return humanDuration(Math.max(0, now - started));
}

// humanDuration renders a millisecond span at one unit of precision plus its
// neighbour, which is as much as an uptime is ever read for.
export function humanDuration(ms: number): string {
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

// portLabel shows the bound port, and the preferred one too when they differ
// — the daemon keeps the port it bound, so a changed setting is visible as a
// disagreement rather than silently ignored.
export function portLabel(st: Status | undefined): string {
  if (!st) return "—";
  if (!st.bound_port) return "—";
  if (st.pref_port && st.pref_port !== st.bound_port) {
    return `${st.bound_port} (prefers ${st.pref_port} on next start)`;
  }
  return String(st.bound_port);
}

// ── numbers ──────────────────────────────────────────────────────────

// formatBytes renders a byte count. Binary units, because every producer of
// these numbers (RSS from /proc, SQLite page counts, a directory walk) is
// binary too.
export function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

// measured renders a number that might not have been measurable. An
// unmeasured RSS is "unknown", never "0 B" — a zero there reads as "costs
// nothing", which is the opposite of the truth (PLAN §18.5).
export function measured(bytes: number, known: boolean): string {
  return known ? formatBytes(bytes) : "unknown";
}

// formatCount groups thousands so 48231 does not read as 4823.
export function formatCount(n: number | undefined): string {
  return typeof n === "number" && Number.isFinite(n) ? n.toLocaleString("en-US") : "—";
}

// ── instances ────────────────────────────────────────────────────────

// instanceLabel is how an instance is named, matching the Go side: "type/name",
// collapsed to the bare type for the per-type default instance.
export function instanceLabel(r: InstanceRef): string {
  if (!r.name || r.name === r.type) return r.type;
  return `${r.type}/${r.name}`;
}

export type CaptureChip = { text: string; cls: string; title: string };

// captureChip is the recording indicator §13.3 asks for. "Read only" is not a
// lesser green — it is the state where the store looks alive and nothing new
// ever lands in it, so it gets its own neutral colour rather than a dimmer
// version of the recording one.
export function captureChip(r: InstanceRef): CaptureChip {
  return r.capture
    ? {
        text: "recording",
        cls: "bg-green-200 dark:bg-green-800 text-green-700 dark:text-green-300",
        title: "This instance writes what it learns back into the store.",
      }
    : {
        text: "read only",
        cls: "bg-white-300 dark:bg-navy-600 text-black-800 dark:text-black-600",
        title: "This instance reads the store and writes nothing back — nothing it does is captured.",
      };
}

// ── warnings ─────────────────────────────────────────────────────────

export type WarnLevel = "warn" | "info";
export type Warning = { id: string; level: WarnLevel; title: string; body: string };

// warningsFor is the Overview's banner set. Every entry states the real
// consequence rather than the flag's name (PLAN §13.5 point 5), and each one
// is derived — nothing here is a banner someone has to remember to dismiss.
export function warningsFor(ov: Overview | null): Warning[] {
  if (!ov) return [];
  const out: Warning[] = [];

  if (ov.store_reason) {
    out.push({
      id: "store",
      level: "warn",
      title: reasonTitle(ov.store_reason as DataReason),
      body: reasonHint(ov.store_reason as DataReason, ov.store_error),
    });
  }

  // Zero-LLM is not just "fewer features": long identifiers get truncated out
  // of the stored fact, so the memory is quietly lossy (PLAN §9).
  if (ov.store && isZeroLLM(ov.store.llm?.status)) {
    out.push({
      id: "zero-llm",
      level: "info",
      title: "Running zero-LLM",
      body:
        "No language model is wired up, so facts are stored as captured: long identifiers get truncated and nothing is consolidated. " +
        "Recall still works — it is the quality of what gets written that drops. Pick a local or cloud provider in Settings to change this.",
    });
  }

  if (ov.store && ov.store.index?.embedding_rows === 0) {
    out.push({
      id: "no-embeddings",
      level: "info",
      title: "Semantic search is off",
      body: `No embeddings are stored${
        ov.store.index.latest_pages_missing_embeddings
          ? ` (${ov.store.index.latest_pages_missing_embeddings} latest pages have none)`
          : ""
      }, so search falls back to whole-token full-text matching only.`,
    });
  }

  if (ov.store && ov.store.spool?.pending > 0) {
    out.push({
      id: "spool",
      level: "warn",
      title: "Hook events are queueing up",
      body: `${formatCount(ov.store.spool.pending)} events are spooled on the hook side — they were sent but not absorbed. A store that looks idle while agents are working usually looks like this.`,
    });
  }

  if (ov.autostart_lock?.locked) {
    out.push({
      id: "autostart-lock",
      level: "info",
      title: "Autostart is locked on",
      body:
        (ov.autostart_lock.reason ?? "A provider instance uses Agent Memory.") +
        " It cannot be switched off while something depends on the daemon — turn the instance's toggle off first.",
    });
  }

  return out;
}

// isZeroLLM reads the provider status. Anything that is not an explicit "ok"
// counts as not usable, so a provider stuck in an error state warns the same
// way a disabled one does rather than passing for healthy.
export function isZeroLLM(status: string | undefined): boolean {
  return (status ?? "disabled").toLowerCase() !== "ok";
}

// reasonTitle / reasonHint turn the server's named reason into the sentence
// shown. They mirror hintFor() in handlers.go — the two fixable states get
// told apart, and anything else keeps the raw error.
export function reasonTitle(reason: DataReason): string {
  switch (reason) {
    case "web_disabled":
      return "The backend's web API is off";
    case "daemon_not_running":
      return "The daemon is not running";
    default:
      return "The store could not be read";
  }
}

export function reasonHint(reason: DataReason, err?: string): string {
  switch (reason) {
    case "web_disabled":
      return 'Turn on "web API" in Agent Memory settings and restart the daemon — the counters, project list and search are served by it.';
    case "daemon_not_running":
      return "Start the daemon to read the store. Nothing is lost while it is down — the numbers just cannot be read.";
    default:
      return err ?? "Unknown error.";
  }
}

// ── blocked reads ────────────────────────────────────────────────────

// Blocked is a panel read that came back with a reason instead of data. It
// carries an `action` when the fix is somewhere the user can go, so the tab
// can offer a way forward instead of a dead end (PLAN §13.5 point 5).
export type Blocked = { title: string; body: string; action?: string };

// blockedBy turns a DataFailure into that state, or null when the response
// actually carried data. The two named reasons are ordinary configuration —
// the server answers them with HTTP 200 for exactly that reason — so a tab
// renders them as something to fix, never as a red crash.
export function blockedBy(res: DataFailure | null | undefined): Blocked | null {
  if (!res || (!res.reason && !res.error)) return null;
  const reason = (res.reason ?? "") as DataReason;
  return {
    title: reasonTitle(reason),
    body: res.hint || reasonHint(reason, res.error),
    action: reason === "web_disabled" ? "Open Settings" : reason === "daemon_not_running" ? "Go to Overview" : undefined,
  };
}

// ── time ─────────────────────────────────────────────────────────────

// relativeTime renders an RFC3339 timestamp as an age. The absolute value is
// kept for the title attribute by the caller — an age answers "is this still
// alive?", which is the only question a last-activity column is ever read for.
export function relativeTime(iso: string | undefined, now: number = Date.now()): string {
  if (!iso) return "never";
  const t = Date.parse(iso);
  if (!Number.isFinite(t)) return "never";
  const delta = now - t;
  if (delta < 0) return "just now";
  if (delta < 60_000) return "just now";
  return `${humanDuration(delta)} ago`;
}

// ── ratios ───────────────────────────────────────────────────────────

// share renders n/total as a percentage. A zero total is "—", not "0%": no
// events at all is a different statement from "none of them were accepted".
export function share(n: number, total: number): string {
  if (!total) return "—";
  const p = (n / total) * 100;
  if (p > 0 && p < 0.1) return "<0.1%";
  return `${p >= 10 ? Math.round(p) : p.toFixed(1)}%`;
}

// pctWidth is the same ratio as a bar width, clamped so a hairline stays
// visible — a drop share of 0.2% still has to be seeable.
export function pctWidth(n: number, total: number): number {
  if (!total || n <= 0) return 0;
  return Math.min(100, Math.max(1.5, (n / total) * 100));
}

// ── access ───────────────────────────────────────────────────────────

// MANAGE_ADMIN_ONLY is the one line a viewer is shown where the managing
// controls would have been.
//
// One sentence, one place. The controls themselves are LEFT OUT rather than
// rendered dead: a disabled button asks a question nobody can answer from the
// screen, while a line that says who may do it does (Yoga, PLAN §23.3). It
// reads as a division of labour, not as a refusal — looking is allowed, and
// everything on the page around it still works.
export const MANAGE_ADMIN_ONLY = "Managing Agent Memory — starting the daemon, changing settings, importing or sweeping — is restricted to admins. Everything here is readable.";

// ── watchdog (PLAN §25) ──────────────────────────────────────────────

// WATCHDOG_CHURN is how many interventions turn "it recovered" into "look at
// this". Three is the point where a daemon is not having a bad moment, it is
// failing repeatedly and something upstream — the binary, the store, the
// port — is the real subject.
export const WATCHDOG_CHURN = 3;

// WatchdogLine is the Overview's one-line reading of supervision: what state
// it is in, how that should look, and the sentence underneath.
export type WatchdogLine = { label: string; cls: string; detail: string };

// watchdogLine reads the supervision record the way a person asks about it:
// is anything watching this daemon, and has it had to do anything?
//
// The four states are kept apart because the right reaction differs. Gave-up
// is the only one that is a standing problem; churn is a hint to look; off is
// a fact about configuration, not a fault; and a quiet watchdog is the normal
// case and must not look like an achievement.
export function watchdogLine(w: WatchdogState | undefined): WatchdogLine {
  if (!w || !w.watching) {
    return {
      label: "Not watching",
      cls: "text-black-800 dark:text-black-600",
      detail:
        "Nothing depends on this daemon, so nothing is supervising it. Supervision follows the same autostart signal shown above — there is no separate switch.",
    };
  }
  if (w.stopped_by_operator) {
    return {
      label: "Paused — stopped by hand",
      cls: "text-black-800 dark:text-black-600",
      detail: "Someone stopped this daemon, so the watchdog is leaving it down. Starting it again resumes supervision.",
    };
  }
  if (w.gave_up) {
    return {
      label: "Gave up",
      cls: "text-rose-700 dark:text-rose-300",
      detail:
        w.gave_up_reason ||
        `${w.consecutive_failures} starts in a row failed, so the watchdog stopped trying rather than respawning forever.`,
    };
  }
  if (w.consecutive_failures > 0) {
    return {
      label: "Retrying",
      cls: "text-cau-600 dark:text-cau-400",
      detail: `${w.consecutive_failures} start${w.consecutive_failures === 1 ? "" : "s"} failed so far${
        w.last_error ? `: ${w.last_error}` : "."
      } Each retry waits longer than the last.`,
    };
  }
  return {
    label: w.restarts > 0 ? `Watching — ${w.restarts} restart${w.restarts === 1 ? "" : "s"}` : "Watching",
    cls: w.restarts >= WATCHDOG_CHURN ? "text-cau-600 dark:text-cau-400" : "text-green-600 dark:text-green-300",
    detail: watchdogDetail(w),
  };
}

// watchdogDetail is the sentence under a healthy watchdog: what it has had to
// do, and when. "Nothing" is said out loud rather than left blank — a blank
// line reads as an unanswered question.
export function watchdogDetail(w: WatchdogState): string {
  if (w.restarts === 0) {
    return "The daemon has not needed an intervention since wick started.";
  }
  const hung = w.hung_restarts > 0 ? `, ${w.hung_restarts} of them wedged rather than dead` : "";
  const last = w.last_restart_ms
    ? ` Last: ${reasonWord(w.last_reason)} ${relativeTime(new Date(w.last_restart_ms).toISOString())}.`
    : "";
  return `${w.restarts} intervention${w.restarts === 1 ? "" : "s"} since wick started${hung}.${last}`;
}

// reasonWord spells out the machine token the server sends. "off" is not a
// failure of the daemon at all — it was never running — so it does not get a
// failure's wording.
export function reasonWord(reason: WatchdogState["last_reason"]): string {
  switch (reason) {
    case "hung":
      return "restarted a daemon that had stopped answering";
    case "dead":
      return "started a daemon whose process had exited";
    case "off":
      return "started a daemon that should have been running";
    default:
      return "acted";
  }
}
