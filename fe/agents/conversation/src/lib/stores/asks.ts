import { writable } from "svelte/store";
import type { AskRequest } from "../types/agents.js";

export const currentAsk = writable<AskRequest | null>(null);

export function showAsk(req: AskRequest) {
  currentAsk.set(req);
}

export function hideAsk(payload?: { id?: string }) {
  currentAsk.update((cur) => (payload?.id && cur && payload.id !== cur.id ? cur : null));
}
