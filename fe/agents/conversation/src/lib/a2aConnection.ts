import { apiGetE, apiPostE, apiPutE } from "@wick-fe/common-api";

/* Connections › A2A (api_team_a2a.go). The API key comes back only in the
   response that minted it (first Enable, Rotate); every other response
   says whether one is set and its last four characters. */
export type AgentA2AStatus = {
  enabled: boolean;
  public_card: boolean;
  allowed_callers: string[];
  key_set: boolean;
  key_hint?: string;
  rotated_at?: string;
  endpoint_url: string;
  card_url: string;
  api_key?: string;
};
export type AgentA2AUpdate = Partial<{ enabled: boolean; public_card: boolean; allowed_callers: string[] }>;
export type A2AProbe = { ok: boolean; status: string; card_ms: number; send_ms: number; reply?: string; error?: string };

const enc = encodeURIComponent;
const path = (base: string, id: string) => `${base}/api/team/agents/${enc(id)}/a2a`;

export const getAgentA2A = (base: string, id: string) => apiGetE<AgentA2AStatus>(path(base, id));
export const updateAgentA2A = (base: string, id: string, body: AgentA2AUpdate) => apiPutE<AgentA2AStatus>(path(base, id), body);
export const rotateAgentA2A = (base: string, id: string) => apiPostE<AgentA2AStatus>(`${path(base, id)}/rotate`, {});
export const revokeAgentA2A = (base: string, id: string) => apiPostE<AgentA2AStatus>(`${path(base, id)}/revoke`, {});
export const testAgentA2A = (base: string, id: string) => apiPostE<A2AProbe>(`${path(base, id)}/test`, {});

/** maskedKey is how a stored key reads: never the key, only its hint. */
export const maskedKey = (s: Pick<AgentA2AStatus, "key_set" | "key_hint">) =>
  s.key_set ? `wa2a_••••••••${s.key_hint ?? ""}` : "No key";

/** probeLine is the Test result in one line. */
export function probeLine(p: A2AProbe): string {
  if (p.ok) return `OK — card ${p.card_ms} ms, ping ${p.send_ms} ms`;
  return `Failed (${p.status})${p.error ? `: ${p.error}` : ""}`;
}

/** curlExample is a ready-to-run message/send against the endpoint. The key
    is always the $A2A_KEY placeholder — a real key never lands in a snippet
    that gets pasted into shells, tickets and chats. */
export function curlExample(endpoint: string): string {
  const body = JSON.stringify({
    jsonrpc: "2.0", id: 1, method: "SendMessage",
    params: { message: { messageId: "m1", role: "ROLE_USER", parts: [{ text: "Hello" }] } },
  });
  return [
    `curl -X POST '${endpoint}' \\`,
    `  -H "Authorization: Bearer $A2A_KEY" \\`,
    `  -H 'Content-Type: application/json' \\`,
    `  -H 'A2A-Version: 1.0' \\`,
    `  -d '${body}'`,
  ].join("\n");
}
