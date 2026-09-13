import { apiGetE } from "@wick-fe/common-api";

/* Mirrors internal/tools/agents/api_composer_usage.go — GET
   /api/composer/usage. Backs the `/usage` popover: the session provider's
   account + rate-limit windows, read from the server's shared, paced cache
   (so opening the popover never costs an upstream request). */
export type ComposerUsageWindow = { key: string; utilization: number; resetsAt: string };

export type ComposerUsage = {
  provider: string;
  /* supported=false means this provider TYPE has no usage API (codex,
     gemini, wick). `reason` says so in words. Not an error. */
  supported: boolean;
  reason: string;
  account: {
    connected: boolean;
    email: string;
    plan: string;
    org: string;
    authMethod: string;
    expiresAt: string;
  } | null;
  windows: ComposerUsageWindow[];
  error: string;
  pending: boolean;
  checking: boolean;
  fetchedAt: string;
  ageS: number;
  nextS: number;
  /* canManage gates the Re-check button — forcing a probe is a manage
     grant, reading the cached number is not. */
  canManage: boolean;
};

type WireComposerUsage = {
  provider?: string;
  supported?: boolean;
  reason?: string;
  account?: {
    connected?: boolean; email?: string; plan?: string; org?: string;
    auth_method?: string; expires_at?: string;
  } | null;
  windows?: { key?: string; utilization?: number; resets_at?: string }[] | null;
  error?: string;
  pending?: boolean;
  checking?: boolean;
  fetched_at?: string;
  age_s?: number;
  next_s?: number;
  can_manage?: boolean;
};

export function normalizeComposerUsage(w: WireComposerUsage): ComposerUsage {
  return {
    provider: w.provider ?? "",
    supported: w.supported ?? false,
    reason: w.reason ?? "",
    account: w.account
      ? {
          connected: w.account.connected ?? false,
          email: w.account.email ?? "",
          plan: w.account.plan ?? "",
          org: w.account.org ?? "",
          authMethod: w.account.auth_method ?? "",
          expiresAt: w.account.expires_at ?? "",
        }
      : null,
    windows: (w.windows ?? []).map((x) => ({
      key: x.key ?? "",
      utilization: x.utilization ?? 0,
      resetsAt: x.resets_at ?? "",
    })),
    error: w.error ?? "",
    pending: w.pending ?? false,
    checking: w.checking ?? false,
    fetchedAt: w.fetched_at ?? "",
    ageS: w.age_s ?? 0,
    nextS: w.next_s ?? 0,
    canManage: w.can_manage ?? false,
  };
}

export const getComposerUsage = (base: string, provider: string) =>
  apiGetE<WireComposerUsage>(`${base}/api/composer/usage?provider=${encodeURIComponent(provider)}`);
