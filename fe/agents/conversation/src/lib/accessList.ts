/* The Access list's selection and bulk arithmetic: one list per tier with
   a Granted / Not granted filter, row checkboxes that only select, and a
   bulk bar that acts on the selected rows. Kept out of the component so
   it can be unit tested. */
import type { AgentConnector, ConnectorGrant } from "./api/team.js";
import { setLevelFor, setOverride, tierDefault, tierOf } from "./accessTiers.js";

/** "choose" = the checklist; "owner" = "Same as me" (team.AccessOwner). */
export type AccessMode = "choose" | "owner";
export type AccessFilter = "all" | "granted" | "not_granted";
export type BulkAction = "read" | "all" | "off" | "default";
export type SelectState = "none" | "some" | "all";

export const normalizeAccessMode = (v: string | undefined | null): AccessMode => (v === "owner" ? "owner" : "choose");

/** effectiveLevel is what a row ends up with: its grant, else the tier
    default (Connectors rows default to off). */
export function effectiveLevel(c: AgentConnector, grants: ConnectorGrant[], isCaptain: boolean): ConnectorGrant["level"] {
  return grants.find((g) => g.connector_id === c.id)?.level ?? tierDefault(c, isCaptain);
}

export const isGranted = (c: AgentConnector, grants: ConnectorGrant[], isCaptain: boolean) =>
  effectiveLevel(c, grants, isCaptain) !== "off";

/** filterRows keeps the rows a filter chip shows. */
export function filterRows(rows: AgentConnector[], grants: ConnectorGrant[], f: AccessFilter, isCaptain: boolean): AgentConnector[] {
  if (f === "all") return rows;
  const want = f === "granted";
  return rows.filter((c) => isGranted(c, grants, isCaptain) === want);
}

/** filterCounts feeds the chips' numbers. */
export function filterCounts(rows: AgentConnector[], grants: ConnectorGrant[], isCaptain: boolean): Record<AccessFilter, number> {
  const granted = rows.filter((c) => isGranted(c, grants, isCaptain)).length;
  return { all: rows.length, granted, not_granted: rows.length - granted };
}

/** selectState drives the header checkbox: checked, indeterminate or
    empty, judged on the shown rows only. */
export function selectState(selected: ReadonlySet<string>, shown: AgentConnector[]): SelectState {
  const n = shown.filter((c) => selected.has(c.id)).length;
  if (n === 0) return "none";
  return n === shown.length ? "all" : "some";
}

/** toggleShown is the header checkbox: every shown row selected → they are
    deselected, otherwise all of them get selected. Rows hidden by the
    search or filter keep their state. */
export function toggleShown(selected: ReadonlySet<string>, shown: AgentConnector[]): Set<string> {
  const out = new Set(selected);
  if (selectState(selected, shown) === "all") for (const c of shown) out.delete(c.id);
  else for (const c of shown) out.add(c.id);
  return out;
}

export function toggleOne(selected: ReadonlySet<string>, id: string, on: boolean): Set<string> {
  const out = new Set(selected);
  if (on) out.add(id);
  else out.delete(id);
  return out;
}

/** bulkApply sets one level on the selected rows only. "off" clears a
    Connectors row and stores an off override on a tier row; "default"
    drops the grant (a tier row falls back to its default). */
export function bulkApply(grants: ConnectorGrant[], rows: AgentConnector[], selected: ReadonlySet<string>, action: BulkAction): ConnectorGrant[] {
  const picked = rows.filter((c) => selected.has(c.id));
  if (action === "default") return picked.reduce((out, c) => setOverride(out, c.id, "default"), grants);
  return setLevelFor(grants, picked, action);
}

/** setRowLevel is a row's own level control. On a Connectors row "off"
    removes the grant; anything else keeps the row's accounts. */
export function setRowLevel(grants: ConnectorGrant[], c: AgentConnector, level: "off" | "read" | "all" | "pick"): ConnectorGrant[] {
  if (level === "off" && tierOf(c) === "connectors") return setOverride(grants, c.id, "default");
  return setOverride(grants, c.id, level);
}

/** accessPayload is what a save sends for the Access tab. The checklist is
    kept in "owner" mode too, so switching back restores it. */
export function accessPayload(mode: AccessMode, grants: ConnectorGrant[], includeNew: boolean): {
  access_mode: AccessMode;
  allowed_connectors: ConnectorGrant[];
  include_new_connectors: boolean;
} {
  return { access_mode: mode, allowed_connectors: grants, include_new_connectors: includeNew };
}
