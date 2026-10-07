/* Settings › Skills: which skills an agent's spawns load. */
import type { AgentSkill } from "./api/team.js";

export const SKILL_SOURCE_LABEL: Record<AgentSkill["source"], string> = {
  local: "Local",
  global: "Global",
  builtin: "Built-in",
};

/** toggleSkill switches name off (on=false) or back on; sorted, once. */
export function toggleSkill(disabled: string[], name: string, on: boolean): string[] {
  const next = new Set(disabled);
  if (on) next.delete(name);
  else next.add(name);
  return [...next].sort();
}

/** skillNote is the small line under a skill. */
export function skillNote(s: AgentSkill): string {
  if (s.shadowed) return "Hidden: a local skill with the same name wins.";
  if (s.overrides) return `Local wins over the ${SKILL_SOURCE_LABEL[s.overrides]} skill of the same name.`;
  if (s.required) return "Required by wick — always on.";
  return "";
}

/** CREATE_SKILL_PROMPT is what "Create skill from this chat" sends to
    the agent: it writes the SKILL.md itself, into its project folder. */
export function createSkillPrompt(localDir: string): string {
  const dir = localDir || ".claude/skills/ in your project folder";
  return (
    "Turn what we worked out in this chat into a reusable skill for yourself. " +
    `Write it as ${dir.replace(/\/$/, "")}/<kebab-name>/SKILL.md with frontmatter (name, description: when to use it) ` +
    "and the steps, then tell me the path. Keep it to this project — do not write to any global skills folder."
  );
}
