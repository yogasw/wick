import { humanBytes } from "./format.js";
import type { TraceFileStat } from "./types.js";

/* How a file a trace named compares with what is on disk now. Only what
   the stat proves is claimed: no stat → unknown, no recorded time → only
   present/missing. */
export type TraceFileState = {
  state: "same" | "changed" | "missing" | "unknown";
  /* Short suffix for the chip ("deleted"); empty when same. */
  label: string;
  /* Longer explanation for the tooltip. */
  title: string;
};

// mtime and the call's clock come from the same host but are stamped at
// slightly different moments (a Write finishes after its own start).
const SLACK_MS = 2000;

export function traceFileState(st: TraceFileStat | null | undefined, recordedBytes?: number, calledAt?: number): TraceFileState {
  if (!st || st.status === "unknown") {
    return { state: "unknown", label: "can't check", title: "Outside the session folder or can't be checked" };
  }
  if (st.status === "missing") return { state: "missing", label: "deleted", title: "Deleted from disk" };
  const sizes = recordedBytes && st.size !== undefined && st.size !== recordedBytes
    ? ` — ${humanBytes(recordedBytes)} recorded, now ${humanBytes(st.size)}`
    : "";
  // The file's mtime is the proof it was written after the call. A size
  // difference alone is not: a tool may re-encode what it returns.
  const newer = !!calledAt && !!st.mtime && st.mtime > calledAt + SLACK_MS;
  const sizeOnly = !calledAt && !!sizes;
  if (newer || sizeOnly) {
    return { state: "changed", label: "changed since this trace", title: `Changed since this trace${sizes}` };
  }
  return { state: "same", label: "", title: "Click to load from file" };
}
