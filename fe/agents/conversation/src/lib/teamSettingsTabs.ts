import type { TeamSettings, TeamSettingValues } from "./api/team.js";

/* Tabs of the Team settings drawer. Team settings hold every per-user Team
   option, so a new tab is one entry here plus its view in
   TeamSettings.svelte's VIEWS (the type makes that map complete). The
   router reads the ids from here, so ?tab= follows without another edit.
   No components in this file: the router imports it. */

export type TeamSettingsTabSpec = {
  id: string;
  label: string;
  /** Typed fields: saved after a pause or on blur instead of at once. */
  textKeys: (keyof TeamSettingValues)[];
  /** Why the draft cannot be saved yet ("" = it can). Checked for every
      tab, not only the open one, so a hidden tab never saves bad input. */
  invalid?: (draft: TeamSettingValues, saved: TeamSettings) => string;
};

export const TEAM_SETTINGS_TABS = [
  {
    id: "general",
    label: "General",
    textKeys: ["prompt"],
    invalid: (d, s) =>
      promptBytes(d.prompt) > s.max_prompt_bytes ? `the prompt is over ${Math.round(s.max_prompt_bytes / 1024)} KB` : "",
  },
] as const satisfies readonly TeamSettingsTabSpec[];

export type TeamSettingsTab = (typeof TEAM_SETTINGS_TABS)[number]["id"];

/** teamSettingsTabOf resolves a `tab=` value; unknown → the first tab. */
export function teamSettingsTabOf(t: string | null): TeamSettingsTab {
  return TEAM_SETTINGS_TABS.find((x) => x.id === t)?.id ?? TEAM_SETTINGS_TABS[0].id;
}

/** promptBytes is the UTF-8 size the server's limit counts. */
export function promptBytes(s: string): number {
  return new TextEncoder().encode(s).length;
}

/** invalidReason is the first reason any tab refuses the draft, or "". */
export function invalidReason(draft: TeamSettingValues, saved: TeamSettings): string {
  for (const t of TEAM_SETTINGS_TABS as readonly TeamSettingsTabSpec[]) {
    const why = t.invalid?.(draft, saved) ?? "";
    if (why) return why;
  }
  return "";
}

/** isTextKey reports whether k is typed text (debounced save). */
export function isTextKey(k: string): boolean {
  return (TEAM_SETTINGS_TABS as readonly TeamSettingsTabSpec[]).some((t) => (t.textKeys as string[]).includes(k));
}
