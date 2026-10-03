import { describe, test, expect } from "vitest";
import { TEAM_SETTINGS_TABS, teamSettingsTabOf, invalidReason, isTextKey } from "../teamSettingsTabs.js";
import { TEAM_SETTING_KEYS, type TeamSettings } from "../api/team.js";

const saved: TeamSettings = { prompt: "", open_team: true, idle_animations: true, max_prompt_bytes: 10 };

describe("Team settings tab registry", () => {
  test("ids are unique and an unknown tab falls back to the first", () => {
    const ids = TEAM_SETTINGS_TABS.map((t) => t.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(teamSettingsTabOf("nope")).toBe(ids[0]);
    expect(teamSettingsTabOf(null)).toBe(ids[0]);
    expect(teamSettingsTabOf("general")).toBe("general");
  });

  test("every text key is a real setting", () => {
    for (const t of TEAM_SETTINGS_TABS) for (const k of t.textKeys) expect(TEAM_SETTING_KEYS).toContain(k);
    expect(isTextKey("prompt")).toBe(true);
    expect(isTextKey("open_team")).toBe(false);
  });

  test("a prompt over the limit is refused, counting UTF-8 bytes", () => {
    expect(invalidReason({ prompt: "x".repeat(10), open_team: true, idle_animations: true }, saved)).toBe("");
    expect(invalidReason({ prompt: "é".repeat(6), open_team: true, idle_animations: true }, saved)).toMatch(/over/);
  });
});
