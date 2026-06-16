import { writable } from "svelte/store";
import type { ApprovalRequest } from "../types/agents.js";

export const currentApproval = writable<ApprovalRequest | null>(null);

export function showApproval(req: ApprovalRequest) {
  currentApproval.set(req);
}

export function hideApproval(payload?: { id?: string }) {
  currentApproval.update((cur) => (payload?.id && cur && payload.id !== cur.id ? cur : null));
}
