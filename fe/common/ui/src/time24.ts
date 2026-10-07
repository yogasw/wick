// time24.ts — the one 24-hour clock the agents pages use ("14:13",
// "13:46:31"). toLocaleTimeString with the default locale renders
// "02:13 PM" on an en-US browser; operators read these next to logs and
// server times, which are 24-hour.

function valid(v: string | number | Date): Date | null {
  const d = v instanceof Date ? v : new Date(v);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** clock24 renders HH:MM, or HH:MM:SS with seconds; "" for a bad date. */
export function clock24(v: string | number | Date, seconds = false): string {
  const d = valid(v);
  if (!d) return "";
  return d.toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
    ...(seconds ? { second: "2-digit" } : {}),
    hourCycle: "h23",
  });
}
