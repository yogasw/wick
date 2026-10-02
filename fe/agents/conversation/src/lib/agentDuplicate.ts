import type { AgentItem, AgentWrite } from "./api/team.js";

/* Pure helpers behind "Duplikat agent" in the Team header menu. */

// Mirrors the server's handle rule (api_team.go): ^[a-z0-9][a-z0-9-]{1,30}$.
const HANDLE_MAX = 31;

/** duplicateHandle picks the first free `<handle>-N` (N from 2). A handle
    that already ends in -N counts on from its root, so duplicating
    @ops-2 gives @ops-3 rather than @ops-2-2. The root is cut short when
    the suffix would push the handle past the server's length limit. */
export function duplicateHandle(handle: string, taken: Iterable<string>): string {
  const used = new Set(taken);
  const root = handle.replace(/-\d+$/, "") || handle;
  for (let n = 2; ; n++) {
    const suffix = `-${n}`;
    const head = root.slice(0, HANDLE_MAX - suffix.length).replace(/-+$/, "");
    const next = head + suffix;
    if (!used.has(next)) return next;
  }
}

/** duplicateBody is the POST body for a copy of `a`: same project (so the
    same persona), its own handle, and the same look, features and access.
    Captain, disabled and the sessions stay with the original. */
export function duplicateBody(a: AgentItem, taken: Iterable<string>): AgentWrite {
  return {
    handle: duplicateHandle(a.handle, taken),
    project_id: a.project_id,
    avatar: a.avatar,
    features: a.features,
    allowed_connectors: a.allowed_connectors ?? [],
    include_new_connectors: a.include_new_connectors,
    run_as: a.run_as ?? "caller",
  };
}
