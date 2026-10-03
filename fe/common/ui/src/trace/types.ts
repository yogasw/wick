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
};

export type TraceMedia = { url: string; name: string; kind: "image" | "pdf" | "file"; mime: string };

/* What a renderer may reach outside its own display. All optional: a
   renderer must still work (degraded) when a caller wires none of it. */
export type TraceContext = {
  /* Fetches a stored binary by its blob_ref. */
  loadBlob?: (ref: string) => Promise<Blob>;
  /* Opens a clicked media item in the host app's viewer (lightbox). */
  onOpenMedia?: (m: TraceMedia) => void;
};

export const BINARY_KINDS = new Set(["image", "pdf", "audio", "video", "binary"]);

export function isBinaryKind(kind: string | undefined): boolean {
  return !!kind && BINARY_KINDS.has(kind);
}
