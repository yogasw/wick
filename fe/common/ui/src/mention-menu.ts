/* Rows of the composer's `@` menu, kept out of Composer.svelte so the
   grouping and the filter can be tested without mounting the component. */

import type { ComposerCommand, ComposerMentionAgent } from "./composer-types.js";

/** Section titles of the `@` menu, in the order they are listed: a Team
    agent is a named colleague, a sub-agent is a helper spun up for this
    chat, a file only supplies context — the more consequential pick first. */
export const MENTION_GROUPS = { team: "Team", subagent: "Sub-agents", files: "Files" } as const;

/** agentMentionRows filters agents by query and orders them Team first,
    then Sub-agents. A handle is a short exact token, so a plain substring
    match on it (and on a Team agent's name) is enough — matching on the
    description too would surface an agent for a query aimed at a path. */
export function agentMentionRows(agents: ComposerMentionAgent[], query: string): ComposerCommand[] {
  const q = query.trim().toLowerCase();
  const hit = (a: ComposerMentionAgent) =>
    !q || a.handle.toLowerCase().includes(q) || (a.group === "team" && a.label.toLowerCase().includes(q));
  const team = agents.filter((a) => a.group === "team" && hit(a));
  const sub = agents.filter((a) => a.group !== "team" && hit(a));
  return [
    ...team.map((a) => ({
      key: `team:${a.handle}`,
      value: a.handle,
      label: a.label,
      hint: a.hint ? `@${a.handle} · ${a.hint}` : `@${a.handle}`,
      category: MENTION_GROUPS.team,
      avatar: a.avatar ?? {},
    })),
    ...sub.map((a) => ({
      key: `agent:${a.handle}`,
      value: a.handle,
      label: a.label,
      hint: a.hint,
      category: MENTION_GROUPS.subagent,
    })),
  ];
}

/** fileMentionRows titles file paths with the Files section — only when an
    agent section precedes them; a files-only menu stays untitled as before. */
export function fileMentionRows(paths: string[], titled: boolean): ComposerCommand[] {
  return paths.map((p) => ({
    key: `file:${p}`,
    value: p,
    label: p,
    ...(titled ? { category: MENTION_GROUPS.files } : {}),
  }));
}
