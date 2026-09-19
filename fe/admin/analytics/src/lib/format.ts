/** Small formatters the table leans on. Kept out of the component so they
 *  can be tested without rendering anything. */

/** "3 days ago" reads faster than a timestamp when the question is "is this
 *  person still around". The exact stamp stays in the title attribute. */
export function ago(iso: string | undefined, now: Date = new Date()): string {
  if (!iso) return "never";
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "never";
  const secs = Math.floor((now.getTime() - t) / 1000);
  if (secs < 0) return "just now";
  if (secs < 60) return "just now";
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  if (months < 12) return `${months}mo ago`;
  return `${Math.floor(months / 12)}y ago`;
}

/** Older than this and the row is dimmed: the account still exists, the
 *  person has moved on. 30 days matches the usual "still active?" question. */
export function isDormant(iso: string | undefined, now: Date = new Date()): boolean {
  if (!iso) return true;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return true;
  return now.getTime() - t > 30 * 24 * 3600 * 1000;
}

/** Channels get a colour so a row's provenance is legible at a glance,
 *  and an unknown channel still gets a readable neutral chip. */
export function channelClass(channel: string): string {
  switch (channel) {
    case "slack":
      return "bg-fuchsia-100 text-fuchsia-700 dark:bg-fuchsia-900/40 dark:text-fuchsia-300";
    case "telegram":
      return "bg-sky-100 text-sky-700 dark:bg-sky-900/40 dark:text-sky-300";
    case "ui":
      return "bg-green-100 text-green-700 dark:bg-green-900/40 dark:text-green-300";
    case "rest":
      return "bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300";
    case "schedule":
      return "bg-white-300 text-black-700 dark:bg-navy-600 dark:text-black-600";
  }
  return "bg-white-300 text-black-700 dark:bg-navy-600 dark:text-black-600";
}

/** Line colours for the per-channel curves.
 *
 *  Hex rather than Tailwind classes, and for the same reason the chart's own
 *  colours are inline: `stroke-*` utilities that nothing else on the page
 *  uses never make it into the admin stylesheet, so the class resolves to
 *  nothing and the path falls back to black. */
const CHANNEL_COLORS: Record<string, string> = {
  slack: "#d946ef",
  telegram: "#38bdf8",
  ui: "#22c55e",
  rest: "#f59e0b",
  schedule: "#a78bfa",
  workflow: "#f472b6",
  recover: "#94a3b8",
};

/** A stable colour per channel, falling back to a deterministic pick so an
 *  unknown channel still gets its own line rather than sharing one. */
export function channelColor(channel: string): string {
  const known = CHANNEL_COLORS[channel];
  if (known) return known;
  const spare = ["#2dd4bf", "#fb7185", "#facc15", "#60a5fa", "#c084fc"];
  let h = 0;
  for (let i = 0; i < channel.length; i++) h = (h * 31 + channel.charCodeAt(i)) >>> 0;
  return spare[h % spare.length];
}
