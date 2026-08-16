// Formatting helpers shared by the page and its chart.
//
// Kept apart from the components so the number rendering — the part an
// operator actually reads a decision off — is unit-testable without a DOM.

export function humanBytes(b: number): string {
  if (!Number.isFinite(b) || b <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = b;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  // Whole bytes read oddly as "512.0 B"; everything larger wants one
  // decimal so 1.4 GB and 1.9 GB stay distinguishable.
  return i === 0 ? `${Math.round(v)} B` : `${v.toFixed(1)} ${units[i]}`;
}

export function humanBps(b: number): string {
  if (!Number.isFinite(b) || b <= 0) return "—";
  return `${humanBytes(b)}/s`;
}

export function humanPct(p: number): string {
  if (!Number.isFinite(p) || p <= 0) return "—";
  return `${p.toFixed(0)}%`;
}

// humanDuration renders a span in the largest unit that keeps it readable:
// "45s", "12m", "3h 20m".
export function humanDuration(sec: number): string {
  if (!Number.isFinite(sec) || sec <= 0) return "0s";
  if (sec < 60) return `${Math.round(sec)}s`;
  if (sec < 3600) return `${Math.round(sec / 60)}m`;
  const h = Math.floor(sec / 3600);
  const m = Math.round((sec % 3600) / 60);
  return m > 0 ? `${h}h ${m}m` : `${h}h`;
}

export function clockTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

// pctOf returns a 0..100 percentage, guarding the divide-by-zero that
// happens on a machine whose total memory could not be read.
export function pctOf(part: number, whole: number): number {
  if (!Number.isFinite(whole) || whole <= 0) return 0;
  return Math.min(100, (part / whole) * 100);
}
