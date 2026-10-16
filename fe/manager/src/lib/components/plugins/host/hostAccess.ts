/* Display rules shared by the Host access tab and the "What it can do"
   card. Pure functions only. */
import type { HostAccessScope, HostAccessStatus, PluginHostAccess } from "$lib/api.js";

export const STATUS_LABEL: Record<HostAccessStatus, string> = {
  allowed: "allowed",
  blocked: "blocked",
  undeclared: "undeclared",
  "off-by-default": "off by default",
  unknown: "unknown",
};

export function statusClass(s: HostAccessStatus): string {
  const base = "inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium";
  switch (s) {
    case "allowed": return `${base} bg-green-200 text-green-700`;
    case "blocked": return `${base} bg-red-900 text-red-400`;
    case "undeclared": return `${base} bg-cau-100 text-cau-400`;
    default: return `${base} bg-white-300 text-black-800 dark:bg-navy-600 dark:text-black-600`;
  }
}

/* Whether the scope's toggle shows ON. Unknown scopes have no toggle. */
export const scopeOn = (s: HostAccessScope) => s.status === "allowed" || s.status === "undeclared";

/* A scope can be toggled when wick knows it and a decision means
   something: declarative scopes (datadir, net.listen) have no call to
   allow, and a resource scope the plugin did not declare stays denied. */
export const canToggle = (s: HostAccessScope) => s.known && !s.declarative && (!s.resource || s.declared);

export function scopeTone(s: HostAccessScope): string {
  if (s.destructive) return "text-red-600";
  if (s.sensitive) return "text-cau-400";
  return "text-black-900 dark:text-white-100";
}

/* One-line summary for the detail card. */
export function summary(a: PluginHostAccess): string {
  if (!a.declares && a.scopes.length === 0) return "No host access declared.";
  const on = a.scopes.filter(scopeOn).length;
  const parts = [`${a.scopes.length} scope${a.scopes.length === 1 ? "" : "s"}`, `${on} allowed`];
  if (a.item_count) parts.push(`${a.item_count} item${a.item_count === 1 ? "" : "s"}`);
  return parts.join(" · ");
}

export const hostAccessLink = (key: string) => `/admin/plugins?tab=host&q=${encodeURIComponent(key)}`;
