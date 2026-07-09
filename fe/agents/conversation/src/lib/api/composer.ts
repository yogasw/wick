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

export const listComposerCommands = (base: string) =>
  apiGetE<{ commands: ComposerApiCommand[] }>(`${base}/api/composer/commands`);
