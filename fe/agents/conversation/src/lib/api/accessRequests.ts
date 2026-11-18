import { apiGetE, apiPostE } from "@wick-fe/common-api";
import type { AccessRequestView } from "../accessRequests.js";

/* Access requests: an agent's card of actions waiting for a person. The
   server computes everything for the signed-in viewer (what they may
   decide, why not); these calls only read that view and send choices. */

export const getAccessRequestView = (api: string) => apiGetE<AccessRequestView>(`${api}/view`);

export type AccessDecisionsBody = {
  decisions?: { item_id: string; mode: string }[];
  note?: string;
  deny_all?: boolean;
};

export type AccessDecideResult = { item_id: string; ok: boolean; code?: string; error?: string };

export const sendAccessDecisions = (api: string, body: AccessDecisionsBody) =>
  apiPostE<{ view: AccessRequestView; results: AccessDecideResult[] }>(`${api}/decisions`, body);
