import { apiGetE, apiPostE, apiDeleteE } from "@wick-fe/common-api";
import type { ApprovalsResponse, ApprovalDecision } from "../types/agents.js";

type ApprovalDecisionBody = {
  id: string;
  decision: ApprovalDecision;
  match_key: string;
  reason?: string;
};

export const getApprovals = (base: string, id: string) =>
  apiGetE<ApprovalsResponse>(`${base}/sessions/${id}/approvals`);

export const sendApprovalDecision = (base: string, id: string, body: ApprovalDecisionBody) =>
  apiPostE<{ status: string }>(`${base}/sessions/${id}/approve`, body);

export const revokeApproval = (base: string, id: string, matchKey: string, scope: "session" | "always") =>
  apiDeleteE<{ status: string }>(`${base}/sessions/${id}/approve/${encodeURIComponent(matchKey)}?scope=${scope}`);
