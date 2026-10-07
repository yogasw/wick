/* Same thresholds as event.HumanBytes, so a chip reads identically whether
   the backend or the FE classifier labelled it. */
export function humanBytes(n: number): string {
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${(n + 512) >> 10} KB`;
  return `${n} B`;
}

export function utf8Bytes(s: string): number {
  return new TextEncoder().encode(s).length;
}

/* "Trace truncated — showing X of Y KB …" — null when nothing was cut. */
export function truncationNote(body: string, originalBytes?: number): string | null {
  if (!originalBytes) return "Trace truncated (the agent saw the full output)";
  const shown = utf8Bytes(body);
  const unit = originalBytes >= 1 << 20 ? "MB" : "KB";
  const fmt = (n: number) => (unit === "MB" ? (n / (1 << 20)).toFixed(1) : String(Math.max(1, Math.round(n / 1024))));
  return `Trace truncated — showing ${fmt(shown)} of ${fmt(originalBytes)} ${unit} (the agent saw the full output)`;
}

/* Raw view of a binary: never the broken half of a base64 string. */
export function rawPreview(raw: string, binary: boolean): string {
  if (!binary || raw.length <= 400) return raw;
  return `${raw.slice(0, 200)}… ${humanBytes(raw.length)}`;
}
