import { normalizeProviderKey } from "@wick-fe/common-api";
import type { FailoverTarget } from "./api.js";
import type { ProviderHealthDTO } from "./types.js";

/* Limits mirror internal/agents/provider/failover_config.go. */
export const MAX_FAILOVER_TARGETS = 6;
export const MIN_RECHECK_MINUTES = 1;
export const MAX_RECHECK_MINUTES = 60;

export const FAILOVER_TRIGGERS: { key: string; label: string }[] = [
  { key: "limit", label: "Usage limit" },
  { key: "auth", label: "Auth error" },
  { key: "unavailable", label: "Unavailable" },
];

/* clockLabel is a time in the browser's zone: "14:30" today, with the
   date in front on another day. */
export function clockLabel(iso: string, now: Date = new Date()): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const hm = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
  if (d.toDateString() === now.toDateString()) return hm;
  return `${d.toLocaleDateString([], { month: "short", day: "numeric" })} ${hm}`;
}

/* healthLabel is the badge of an unhealthy account, "" while healthy. */
export function healthLabel(h: ProviderHealthDTO | null | undefined, now: Date = new Date()): string {
  if (!h) return "";
  switch (h.Kind) {
    case "limit": {
      const at = h.Until ? clockLabel(h.Until, now) : "";
      return at ? `Limited until ${at}` : "Limited";
    }
    case "auth":
      return h.Retrying ? "Retrying auth" : "Auth failed";
    case "unavailable":
      return "Unavailable";
    default:
      return "";
  }
}

/* healthBadgeClass colours a health badge the same on the list and the
   detail card: amber for what heals by itself (a limit, a login being
   retried), red for what needs someone (a dead login, an outage). */
export function healthBadgeClass(h: ProviderHealthDTO | null | undefined): string {
  return h?.Kind === "limit" || h?.Retrying
    ? "bg-amber-100 dark:bg-amber-900 text-amber-700 dark:text-amber-300"
    : "bg-red-100 dark:bg-red-900 text-red-700 dark:text-red-300";
}

/* targetType is the provider type of a "type/name" tag (or bare type). */
export function targetType(tag: string): string {
  return tag.split("/")[0];
}

/* chainProblems is the client-side check the server repeats: no target
   may be the instance itself or appear twice. One message per index,
   "" for a fine row; an empty instance is left to the "pick" hint. */
export function chainProblems(homeType: string, homeName: string, chain: FailoverTarget[]): string[] {
  const self = normalizeProviderKey(`${homeType}/${homeName}`);
  const seen = new Set<string>();
  return chain.map((t) => {
    if (!t.instance) return "";
    const key = normalizeProviderKey(t.instance);
    if (key === self) return "This is the provider itself — pick another one.";
    if (seen.has(key)) return `${key} is already in the list.`;
    seen.add(key);
    return "";
  });
}

/* crossTypeNote is the alert of a target on another provider type: its
   CLI cannot resume this one's history. "" for the same type. */
export function crossTypeNote(homeType: string, tag: string): string {
  const t = targetType(tag);
  if (!tag || t === homeType) return "";
  return `Conversation history does not carry over to ${t}. Wick will send the last message and a short summary instead.`;
}

/* pickValue / fromPickValue map a target onto ProviderPicker's value
   ("type/name" or "type/name::modelID") and back. */
export function pickValue(t: FailoverTarget): string {
  if (!t.instance) return "";
  const key = normalizeProviderKey(t.instance);
  return t.model_id ? `${key}::${t.model_id}` : key;
}

export function fromPickValue(v: string): FailoverTarget {
  const i = v.indexOf("::");
  return i < 0 ? { instance: v, model_id: "" } : { instance: v.slice(0, i), model_id: v.slice(i + 2) };
}
