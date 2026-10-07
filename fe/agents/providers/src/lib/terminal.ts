/* terminal.ts — client for the web terminal (gotty) of an omp/opencode
   instance. The browser only ever names a command KEY from the server's
   allowlist; the server starts gotty on 127.0.0.1 and returns a path
   under wick's own proxy for the iframe. Admin-only server-side. */

import { get, post } from "./api.js";

export type TerminalCommand = { key: string; label: string };

export type TerminalStatus = { commands: TerminalCommand[]; gottyInstalled: boolean };

export type TerminalSession = { id: string; url: string; command: string };

function termPath(base: string, type: string, name: string): string {
  return `${base}/api/providers/${encodeURIComponent(type)}/${encodeURIComponent(name)}/terminal`;
}

export async function apiTerminalStatus(base: string, type: string, name: string): Promise<TerminalStatus> {
  const r = await get<{ commands?: TerminalCommand[] | null; gotty_installed?: boolean }>(termPath(base, type, name));
  return { commands: r.commands ?? [], gottyInstalled: !!r.gotty_installed };
}

export async function apiTerminalStart(base: string, type: string, name: string, command: string): Promise<TerminalSession> {
  return post<TerminalSession>(`${termPath(base, type, name)}?command=${encodeURIComponent(command)}`);
}

/* Close = kill gotty and its command. Best-effort: the server also ends
   the session when the websocket drops or idles out. */
export async function apiTerminalClose(base: string, type: string, name: string, id: string): Promise<void> {
  await post(`${termPath(base, type, name)}/${encodeURIComponent(id)}/close`);
}
