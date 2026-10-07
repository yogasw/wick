/* TraceDisplay mirrors event.Display (internal/agents/event/display.go):
   the render hint the backend stamps on a tool_use input / tool_result.
   Only kind is always set. A trace written before Display existed has none
   — classifyTrace() derives one from the raw text with the same rules. */
export type TraceDisplay = {
  kind: string;
  body?: string;
  lang?: string;
  mime?: string;
  original_bytes?: number;
  truncated?: boolean;
  blob_ref?: string;
  too_large?: boolean;
  tool?: string;
  title?: string;
  summary?: string;
  command?: string;
  cwd?: string;
  timeout?: number;
  path?: string;
  pattern?: string;
  connector?: string;
  op?: string;
  exit_code?: number;
  name?: string;
  parts?: TraceDisplay[];
  /* In-memory only: the base64 of a binary seen live (inflight / SSE),
     before the backend stored it as a blob. Never decoded until the chip
     is clicked. */
  data?: string;
  /* In-memory only: the file lines a skill card shows (skill.ts) — the
     range of a numbered read and whether it was only part of the file. */
  lines?: { from: number; to: number; partial: boolean };
};

/* note: a caveat the viewer shows over the media, e.g. that it is the file
   as it is now rather than as the trace saw it. */
export type TraceMedia = { url: string; name: string; kind: "image" | "pdf" | "file"; mime: string; note?: string };

/* What the session's file endpoint says about a path a trace named.
   "unknown" covers anything outside the session folder or that could not
   be checked — never a claim either way. rel feeds loadPath. */
export type TraceFileStat = { status: "present" | "missing" | "unknown"; rel?: string; size?: number; mtime?: number };

/* What a renderer may reach outside its own display. All optional: a
   renderer must still work (degraded) when a caller wires none of it. */
export type TraceContext = {
  /* Fetches a stored binary by its blob_ref. */
  loadBlob?: (ref: string) => Promise<Blob>;
  /* Opens a clicked media item in the host app's viewer (lightbox). */
  onOpenMedia?: (m: TraceMedia) => void;
  /* The file the tool call named (Read/Write/Edit file_path), and when the
     call ran (ms). A binary the trace did not keep can still be opened from
     disk through these, and its chip says whether the file is gone or has
     changed since. */
  sourcePath?: string;
  calledAt?: number;
  statPath?: (path: string) => Promise<TraceFileStat>;
  loadPath?: (rel: string) => Promise<Blob>;
};

export const BINARY_KINDS = new Set(["image", "pdf", "audio", "video", "binary"]);

export function isBinaryKind(kind: string | undefined): boolean {
  return !!kind && BINARY_KINDS.has(kind);
}
