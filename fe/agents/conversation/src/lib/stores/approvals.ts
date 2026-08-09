import { writable } from "svelte/store";
import { APIError } from "@wick-fe/common-api";
import type { ApprovalRequest } from "../types/agents.js";

export const currentApproval = writable<ApprovalRequest | null>(null);

export function showApproval(req: ApprovalRequest) {
  currentApproval.set(req);
}

export function hideApproval(payload?: { id?: string }) {
  currentApproval.update((cur) => (payload?.id && cur && payload.id !== cur.id ? cur : null));
}

/**
 * Reports whether a failed approval POST means the request is simply
 * gone — the daemon blocked it on timeout, or another tab answered it
 * first. The API returns 410 for this.
 *
 * Callers must NOT reopen the modal on these: the request id is dead,
 * so every retry earns another 410 and the modal can only be escaped by
 * reloading the page. Genuine failures (500, network) still deserve the
 * inline error and a chance to click again.
 */
export function isExpiredApprovalError(err: unknown): boolean {
  if (err instanceof APIError) return err.status === 410;
  // The promise-based client path can surface a plain Error carrying the
  // server's message instead of a typed APIError.
  if (err instanceof Error) return /no longer pending/i.test(err.message);
  return false;
}
