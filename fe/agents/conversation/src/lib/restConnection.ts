import { apiGetE, apiPostE, apiPutE } from "@wick-fe/common-api";

/* Connections › REST (api_team_rest.go). The agent is called on the
   OpenAI-compatible endpoint with model "agent:<handle>" and the caller's
   own Personal Access Token — the connection has no key of its own, so no
   response here ever carries a token. */
export type AgentRESTStatus = {
  enabled: boolean;
  base_url: string;
  model: string;
  tokens_url: string;
};
export type RESTTestResult = { ok: boolean; model: string; detail: string };

const enc = encodeURIComponent;
const path = (base: string, id: string) => `${base}/api/team/agents/${enc(id)}/rest`;

export const getAgentREST = (base: string, id: string) => apiGetE<AgentRESTStatus>(path(base, id));
export const updateAgentREST = (base: string, id: string, enabled: boolean) => apiPutE<AgentRESTStatus>(path(base, id), { enabled });
export const testAgentREST = (base: string, id: string) => apiPostE<RESTTestResult>(`${path(base, id)}/test`, {});

/** restCurlExample is a ready-to-run chat completion. The token is always
    the $WICK_TOKEN placeholder — a real token never lands in a snippet that
    gets pasted into shells, tickets and chats. */
export function restCurlExample(baseURL: string, model: string): string {
  const body = JSON.stringify({ model, messages: [{ role: "user", content: "Hello" }] });
  return [
    `curl -X POST '${baseURL}/chat/completions' \\`,
    `  -H "Authorization: Bearer $WICK_TOKEN" \\`,
    `  -H 'Content-Type: application/json' \\`,
    `  -d '${body}'`,
  ].join("\n");
}

/** restSDKExample is the same call with the official OpenAI Python SDK,
    reading the token from the environment. */
export function restSDKExample(baseURL: string, model: string): string {
  return [
    "import os",
    "from openai import OpenAI",
    "",
    `client = OpenAI(base_url="${baseURL}", api_key=os.environ["WICK_TOKEN"])`,
    "reply = client.chat.completions.create(",
    `    model="${model}",`,
    `    messages=[{"role": "user", "content": "Hello"}],`,
    ")",
    "print(reply.choices[0].message.content)",
  ].join("\n");
}
