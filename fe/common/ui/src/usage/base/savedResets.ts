/* Saved rate-limit resets — the shared, provider-agnostic model.
 *
 * Mirrors savedResetsDTO in internal/tools/agents/api_provider_connections.go,
 * the `saved_resets` field every usage surface carries (providers list,
 * usage panel, /usage popover, composer picker). The server normalises each
 * provider's own shape, so nothing here branches on the provider type: a
 * new provider that registers a reader shows up without FE changes. */

export type SavedResetItem = {
  id: string;
  label: string;
  expiresAt: string; // RFC 3339, "" when the provider gives none
  startsAt: string;
  usableNow: boolean;
  /* Spendable only while the account is AT a limit. */
  requiresLimit: boolean;
};

export type SavedResets = {
  /* false never reaches the UI today; kept so a reader can say "unknown". */
  supported: boolean;
  available: number;
  total: number;
  items: SavedResetItem[];
  cooldownUntil: string;
  /* Server-side reason when there is nothing to offer; the UI hides the section then. */
  note: string;
  /* The provider's one-line explanation, shown under the list. */
  hint: string;
};

export type WireSavedResets = {
  supported?: boolean;
  available?: number;
  total?: number;
  items?: {
    id?: string;
    label?: string;
    expires_at?: string;
    starts_at?: string;
    usable_now?: boolean;
    requires_limit?: boolean;
  }[] | null;
  cooldown_until?: string;
  note?: string;
  hint?: string;
};

/** parseSavedResets turns the wire field into the model; null when absent
 *  (unsupported type, or the read failed — the section is then hidden). */
export function parseSavedResets(w: WireSavedResets | null | undefined): SavedResets | null {
  if (!w || typeof w !== "object") return null;
  return {
    supported: w.supported ?? false,
    available: Math.max(0, Number(w.available ?? 0) || 0),
    total: Math.max(0, Number(w.total ?? 0) || 0),
    items: (w.items ?? []).map((i) => ({
      id: i.id ?? "",
      label: i.label ?? "",
      expiresAt: i.expires_at ?? "",
      startsAt: i.starts_at ?? "",
      usableNow: i.usable_now ?? false,
      requiresLimit: i.requires_limit ?? false,
    })),
    cooldownUntil: w.cooldown_until ?? "",
    note: w.note ?? "",
    hint: w.hint ?? "",
  };
}

/** Days before expiry at which the chip turns amber. */
export const SAVED_RESET_SOON_DAYS = 3;

function parseTime(s: string): number {
  if (!s) return NaN;
  return Date.parse(s);
}

/** soonestExpiry is the earliest dated item's expiry, or "" when none is dated. */
export function soonestExpiry(r: SavedResets | null | undefined): string {
  let best = "";
  let bestT = Infinity;
  for (const i of r?.items ?? []) {
    const t = parseTime(i.expiresAt);
    if (Number.isFinite(t) && t < bestT) {
      bestT = t;
      best = i.expiresAt;
    }
  }
  return best;
}

/** expiresSoon: the soonest saved reset lapses within SAVED_RESET_SOON_DAYS. */
export function expiresSoon(r: SavedResets | null | undefined, now = Date.now()): boolean {
  const t = parseTime(soonestExpiry(r));
  return Number.isFinite(t) && t - now <= SAVED_RESET_SOON_DAYS * 86_400_000;
}

/** showChip: the ✦N chip only appears when there is something to spend. */
export function showSavedResetsChip(r: SavedResets | null | undefined): boolean {
  return !!r && r.available > 0;
}

/** fmtResetDate renders a date as "Oct 30" (local time). */
export function fmtResetDate(iso: string): string {
  const t = parseTime(iso);
  if (!Number.isFinite(t)) return "";
  return new Date(t).toLocaleDateString("en-US", { month: "short", day: "numeric" });
}

/** fmtResetDateTime renders "Oct 11, 14:00" (local time, 24h). */
export function fmtResetDateTime(iso: string): string {
  const t = parseTime(iso);
  if (!Number.isFinite(t)) return "";
  const d = new Date(t);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${fmtResetDate(iso)}, ${hh}:${mm}`;
}

/** savedResetsTooltip: "1 saved reset · use by Oct 30". */
export function savedResetsTooltip(r: SavedResets | null | undefined): string {
  if (!r || r.available <= 0) return "";
  const n = r.available;
  const head = `${n} saved reset${n === 1 ? "" : "s"}`;
  const by = fmtResetDate(soonestExpiry(r));
  return by ? `${head} · use by ${by}` : head;
}

/** inCooldown: the provider spaces resets out and the next one is not usable yet. */
export function inCooldown(r: SavedResets | null | undefined, now = Date.now()): boolean {
  const t = parseTime(r?.cooldownUntil ?? "");
  return Number.isFinite(t) && t > now;
}

/** usableNowText is the "Usable now" row value: Yes / Only at a limit / No. */
export function usableNowText(r: SavedResets | null | undefined, now = Date.now()): string {
  if (!r || r.available <= 0) return "";
  if (inCooldown(r, now)) return "No";
  const items = r.items;
  if (items.some((i) => i.usableNow)) return "Yes";
  if (items.length && items.every((i) => i.requiresLimit)) return "Only at a limit";
  return items.length ? "No" : "";
}

/** Ring tone for a utilization percentage: green < 80, amber 80–99, red ≥ 100. */
export type RingTone = "ok" | "warn" | "full";
export function ringTone(pct: number): RingTone {
  if (pct >= 100) return "full";
  if (pct >= 80) return "warn";
  return "ok";
}
