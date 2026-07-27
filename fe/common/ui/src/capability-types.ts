/* Model capabilities as reported by a vendor's /models "capabilities" object,
   passed through verbatim by the backend. The well-known keys are typed for
   the chip icons; the index signature keeps ANY extra key the vendor sends so
   the detail modal can list capabilities wick doesn't act on yet. undefined =
   the vendor didn't declare a capabilities object (distinct from "all false"). */
export type ModelCaps = {
  contextWindow?: number;
  maxOutput?: number;
  vision?: boolean;
  reasoning?: boolean;
  tools?: boolean;
  search?: boolean;
  thinkingCanDisable?: boolean;
  thinkingFormat?: string;
  audioInput?: boolean;
  audioOutput?: boolean;
  imageOutput?: boolean;
  videoInput?: boolean;
  pdf?: boolean;
  [key: string]: unknown;
};

/* How CapabilityChips renders:
   - "list" (default) = one small row per capability, name + value (clearest);
   - "name" = compact text chips (vision, reasoning, 500K ctx);
   - "icon" = icon + tooltip (most compact). */
export type CapabilityDisplayMode = "list" | "name" | "icon";

/* CAP_DESCRIPTORS is the single source of truth for how each known capability
   key is presented — its short human label, a one-line explanation for the
   detail modal, an icon glyph for icon mode, and whether wick's engine actually
   ACTS on it today (so the modal can flag "declared but not handled yet").
   Keys not listed here still render generically in the modal. */
export type CapDescriptor = {
  key: string;
  label: string;
  explain: string;
  /** Inline SVG path data (16x16 viewBox) for icon mode. */
  icon: string;
  /** Whether the wick engine currently uses this capability. Informational. */
  handled: boolean;
  /** "bool" renders on/off; "number" renders a token count; "text" a string. */
  kind: "bool" | "number" | "text";
};

// Icon path data (16x16). Simple, recognizable glyphs; stroke-based.
const I = {
  eye: "M1 8s2.5-4.5 7-4.5S15 8 15 8s-2.5 4.5-7 4.5S1 8 1 8Z M8 10a2 2 0 1 0 0-4 2 2 0 0 0 0 4Z",
  brain: "M6 3a2 2 0 0 0-2 2 2 2 0 0 0-1 3.7A2 2 0 0 0 5 12a2 2 0 0 0 3 .5 2 2 0 0 0 3-.5 2 2 0 0 0 2-3.3A2 2 0 0 0 12 5a2 2 0 0 0-2-2 2 2 0 0 0-2 1 2 2 0 0 0-2-1Z",
  wrench: "M10.5 2.5a3 3 0 0 0-3.9 3.7L2 10.8l1.2 1.2 4.6-4.6a3 3 0 0 0 3.7-3.9l-1.7 1.7-1.4-.3-.3-1.4 1.7-1.7Z",
  search: "M7 12a5 5 0 1 0 0-10 5 5 0 0 0 0 10Z M11 11l3 3",
  window: "M2 3h12v10H2z M2 6h12",
  gauge: "M8 13a5 5 0 1 1 0-10 5 5 0 0 1 0 10Z M8 8l2.5-2.5",
  doc: "M4 2h5l3 3v9H4z M9 2v3h3",
  mic: "M8 2a2 2 0 0 0-2 2v4a2 2 0 0 0 4 0V4a2 2 0 0 0-2-2Z M4 8a4 4 0 0 0 8 0 M8 12v2",
  speaker: "M3 6h3l3-3v10l-3-3H3z M11 6a3 3 0 0 1 0 4",
  image: "M2 3h12v10H2z M2 11l3-3 2 2 3-4 4 5",
  video: "M2 4h9v8H2z M11 7l3-2v6l-3-2",
} as const;

export const CAP_DESCRIPTORS: CapDescriptor[] = [
  { key: "contextWindow", label: "context", explain: "Maximum input the model reads in one call.", icon: I.window, handled: true, kind: "number" },
  { key: "maxOutput", label: "max output", explain: "Most tokens the model will generate in one reply.", icon: I.gauge, handled: true, kind: "number" },
  { key: "vision", label: "vision", explain: "Accepts image input.", icon: I.eye, handled: false, kind: "bool" },
  { key: "reasoning", label: "reasoning", explain: "Supports extended reasoning / thinking.", icon: I.brain, handled: true, kind: "bool" },
  { key: "tools", label: "tools", explain: "Supports tool / function calling.", icon: I.wrench, handled: true, kind: "bool" },
  { key: "search", label: "search", explain: "Supports built-in web search.", icon: I.search, handled: false, kind: "bool" },
  { key: "thinkingCanDisable", label: "thinking off", explain: "Reasoning can be turned off.", icon: I.brain, handled: true, kind: "bool" },
  { key: "thinkingFormat", label: "thinking fmt", explain: "Reasoning wire format (e.g. openai).", icon: I.brain, handled: false, kind: "text" },
  { key: "pdf", label: "pdf", explain: "Accepts PDF input.", icon: I.doc, handled: false, kind: "bool" },
  { key: "audioInput", label: "audio in", explain: "Accepts audio input.", icon: I.mic, handled: false, kind: "bool" },
  { key: "audioOutput", label: "audio out", explain: "Produces audio output.", icon: I.speaker, handled: false, kind: "bool" },
  { key: "imageOutput", label: "image out", explain: "Produces image output.", icon: I.image, handled: false, kind: "bool" },
  { key: "videoInput", label: "video", explain: "Accepts video input.", icon: I.video, handled: false, kind: "bool" },
];

const DESC_BY_KEY: Record<string, CapDescriptor> = Object.fromEntries(CAP_DESCRIPTORS.map((d) => [d.key, d]));

export function capDescriptor(key: string): CapDescriptor | undefined {
  return DESC_BY_KEY[key];
}

/* fmtTokens renders a token count compactly: 500000 → "500K", 1000000 → "1M". */
export function fmtTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(n % 1_000_000 ? 1 : 0)}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}K`;
  return String(n);
}

/* hasAnyCaps reports whether a caps object carries at least one meaningful
   (truthy / present-number / non-empty-string) value worth rendering. */
export function hasAnyCaps(caps: ModelCaps | undefined): boolean {
  if (!caps) return false;
  for (const v of Object.values(caps)) {
    if (typeof v === "number" && v > 0) return true;
    if (typeof v === "boolean" && v) return true;
    if (typeof v === "string" && v.trim() !== "") return true;
  }
  return false;
}
