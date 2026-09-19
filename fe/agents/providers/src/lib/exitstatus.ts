/** Human labels for a spawn's exit reason.
 *
 * The raw reasons come from the spawn log (see exitReasonString in
 * internal/agents/pool/factory.go): "" while alive, then clean / idle /
 * stopped / respawn / oom / error, plus "unclean" written by the reader
 * when a PID is gone with no exit line.
 *
 * Those names describe HOW the pool reaped the process, which is not what
 * someone scanning a session list is asking. "idle" in particular reads as
 * "this session is sitting idle right now" when it actually means the
 * process already exited after its idle window — so a finished session is
 * labelled "ended" and the raw reason moves into the tooltip.
 */
export type ExitTone = "running" | "ended" | "bad";

export type ExitStatus = {
  /** Short badge text. */
  label: string;
  /** Green (alive) / neutral (finished normally) / red (needs attention). */
  tone: ExitTone;
  /** Tooltip: the detail from the log, else why this label was chosen. */
  title: string;
};

const ENDED_WHY: Record<string, string> = {
  clean: "process returned normally",
  idle: "process was reaped after its idle window (no output within the idle TTL)",
  respawn: "process was replaced to run the next turn",
  stopped: "process was stopped (preempt, session change, or shutdown)",
};

/**
 * @param reason raw ExitReason / LastStatus ("" = still running)
 * @param detail ReasonDetail from the spawn log, when the caller has one
 * @param exitCode process exit code, shown next to an error
 */
export function exitStatus(reason: string, detail = "", exitCode = 0): ExitStatus {
  if (!reason) return { label: "running", tone: "running", title: detail || "process is still alive" };

  switch (reason) {
    case "unclean":
      return {
        label: "unclean exit",
        tone: "bad",
        title: detail || "process died without recording an exit",
      };
    case "error":
      return {
        label: exitCode !== 0 ? `error (${exitCode})` : "error",
        tone: "bad",
        title: detail || "process exited abnormally",
      };
    case "oom":
      return { label: "out of memory", tone: "bad", title: detail || "process was killed for exceeding its memory limit" };
    case "stopped":
      return { label: "stopped", tone: "ended", title: detail || ENDED_WHY.stopped };
    case "clean":
    case "idle":
    case "respawn":
      return { label: "ended", tone: "ended", title: detail || ENDED_WHY[reason] };
  }
  // Unknown reason: show it verbatim rather than inventing a label.
  return { label: reason, tone: "ended", title: detail };
}

/** Tailwind classes for a status badge, matching the tone. */
export function exitBadgeClass(tone: ExitTone): string {
  switch (tone) {
    case "running":
      return "bg-green-100 dark:bg-green-900 text-green-700 dark:text-green-300";
    case "bad":
      return "bg-red-100 dark:bg-red-900 text-red-700 dark:text-red-300";
  }
  return "bg-white-300 dark:bg-navy-600 text-black-700 dark:text-black-600";
}
