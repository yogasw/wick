import { apiGetE, apiPostE } from "@wick-fe/common-api";
import type { ConfigField } from "@wick-fe/common-ui";

/* Mirrors the wick session-override endpoints in
   internal/tools/agents/providers_wick.go
   (GET/POST /providers/wick/sessions/{id}/overrides). The runtime, per-session
   settings the `/`-menu override popover edits — it SAVES state (one field per
   POST), it does NOT send a chat message. Runtime-only: a fresh session falls
   back to the provider's configured baseline.

   The schema is a generic entity.Config[] reflected from the provider's
   wick-tagged override struct, so the popover renders any field type without a
   bespoke component. */

export type OverrideConfig = {
  schema: ConfigField[];
  values: Record<string, string>;
};

export const getSessionOverrides = (base: string, sessionId: string, provider?: string) => {
  const qs = provider ? `?provider=${encodeURIComponent(provider)}` : "";
  return apiGetE<OverrideConfig>(
    `${base}/providers/wick/sessions/${encodeURIComponent(sessionId)}/overrides${qs}`,
  );
};

export const setSessionOverride = (
  base: string,
  sessionId: string,
  key: string,
  value: string,
  provider?: string,
) => {
  const qs = provider ? `?provider=${encodeURIComponent(provider)}` : "";
  return apiPostE<{ values: Record<string, string> }>(
    `${base}/providers/wick/sessions/${encodeURIComponent(sessionId)}/overrides${qs}`,
    { key, value },
  );
};
