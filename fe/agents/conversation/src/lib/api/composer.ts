import { apiGetE } from "@wick-fe/common-api";

/* Mirrors internal/tools/agents/api_composer.go — GET /api/composer/commands.
   The `/` menu's single source: built-in actions + installed skills. `action`
   is resolved to an FE handler; `insert` (skills) is text placed after `/`. */
export type ComposerApiCommand = {
  id: string;
  label: string;
  hint?: string;
  category?: string;
  action?: string;
  insert?: string;
};

/* scope="new" (pre-session, e.g. the project landing) returns only insert-type
   commands (skills); the default returns built-in actions + skills.
   provider (a type like "claude"/"codex") scopes skills to that provider. */
export const listComposerCommands = (base: string, scope?: string, provider?: string) => {
  const p = new URLSearchParams();
  if (scope) p.set("scope", scope);
  if (provider) p.set("provider", provider);
  const qs = p.toString();
  return apiGetE<{ commands: ComposerApiCommand[] }>(
    `${base}/api/composer/commands${qs ? `?${qs}` : ""}`,
  );
};
