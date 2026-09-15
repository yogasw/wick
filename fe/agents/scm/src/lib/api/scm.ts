// SCM API — git source control for a session's working directory.
// All endpoints live under the agents tool mount and are session-scoped.

import { apiGet, apiPost } from "@wick-fe/common-api";

const BASE = "/tools/agents";

export type RepoSummary = {
  rel: string;
  name: string;
  branch: string;
  changed: number;
  ahead: number;
  behind: number;
};

// Full session-wide snapshot: returned by GET /git/repos AND pushed over
// SSE (git_status). statuses is keyed by RepoSummary.rel.
export type GitStatusSnapshot = {
  repos: RepoSummary[];
  statuses: Record<string, StatusResult>;
  // Which repo the session works in, resolved server-side from session
  // meta — the same answer the agent's system prompt and the Source
  // connector give. active_explicit=false means nobody picked one and
  // this is just the first repo found.
  active: string;
  active_explicit: boolean;
  total_changed: number;
};

export type FileChange = {
  path: string;
  orig_path?: string;
  index: string;
  work_tree: string;
  staged: boolean;
  unstaged: boolean;
  untracked: boolean;
  // The entry stands for a whole folder (a nested repo git will not look
  // inside), not a single file.
  dir?: boolean;
  // Filled in client-side when that folder is a repo wick already knows:
  // what this repo cannot say about it, the snapshot can.
  nested?: { rel: string; branch: string; changed: number };
};

export type BranchInfo = {
  name: string;
  upstream?: string;
  ahead: number;
  behind: number;
  detached: boolean;
};

export type StatusResult = {
  branch: BranchInfo;
  changes: FileChange[];
};

export type BranchList = {
  current: string;
  branches: string[];
  remotes: string[];
};

// How far a commit has travelled. The graph's whole job is telling these
// apart: work that only exists on this machine reads very differently from
// work that has landed on the trunk.
export type CommitState = "local" | "pushed" | "trunk";

export type LogEntry = {
  sha: string;
  subject: string;
  author: string;
  rel_date: string;
  iso_date: string;
  /** Short shas this commit descends from — the graph draws lanes from these. */
  parents?: string[];
  /** Branch/tag names pointing at this commit, e.g. "master", "origin/master". */
  refs?: string[];
  state?: CommitState;
  /** Author email as git recorded it; the key into the avatars map. */
  author_email?: string;
};

// One selectable reference in the graph picker.
export type HistoryRef = {
  name: string;
  sha: string;
  remote: boolean;
  current: boolean;
  trunk: boolean;
};

export type HistoryRefsResponse = {
  refs: HistoryRef[];
  /** The ref "trunk" state is measured against; "" when the repo has none. */
  trunk: string;
};

export type CommitFile = {
  path: string;
  status: string;
  /** Lines git counted for this file. -1 on both means binary. */
  additions?: number;
  deletions?: number;
};

export type CommitDetail = {
  sha: string;
  subject: string;
  author: string;
  /** Who to ask about it, and what they wrote under the subject line. */
  email?: string;
  body?: string;
  iso_date: string;
  files: CommitFile[];
};

const s = (id: string) => `${BASE}/api/sessions/${encodeURIComponent(id)}/git`;
const q = (v: string) => encodeURIComponent(v);

export const getRepos = (id: string) => apiGet<GitStatusSnapshot>(`${s(id)}/repos`);

// Which repo this session is working in. Server-side (session meta) and
// not browser-local, because the agent reads the same selection — it is
// what its system prompt names and what wick_scm reports.
export type ActiveRepo = {
  rel: string;
  name: string;
  dir: string;
  explicit: boolean;
  total: number;
};

export const getActiveRepo = (id: string) => apiGet<ActiveRepo>(`${s(id)}/active`);

export const setActiveRepo = (id: string, repo: string) =>
  apiPost<ActiveRepo>(`${s(id)}/active`, { repo });

export const getStatus = (id: string, repo: string) =>
  apiGet<StatusResult>(`${s(id)}/status?repo=${q(repo)}`);

export const getDiff = (id: string, repo: string, path: string, staged: boolean) =>
  apiGet<{ diff: string }>(
    `${s(id)}/diff?repo=${q(repo)}&path=${q(path)}&staged=${staged ? "1" : "0"}`,
  );

export const getFile = (id: string, repo: string, path: string) =>
  apiGet<{ content: string; path: string }>(
    `${s(id)}/file?repo=${q(repo)}&path=${q(path)}`,
  );

export const getBranches = (id: string, repo: string) =>
  apiGet<BranchList>(`${s(id)}/branches?repo=${q(repo)}`);

export const stage = (id: string, repo: string, paths: string[]) =>
  apiPost(`${s(id)}/stage`, { repo, paths });

export const unstage = (id: string, repo: string, paths: string[]) =>
  apiPost(`${s(id)}/unstage`, { repo, paths });

// Discard is destructive (git restore / clean). untracked lists which of
// paths are untracked so the server picks clean vs restore.
export const discard = (id: string, repo: string, paths: string[], untracked: string[]) =>
  apiPost(`${s(id)}/discard`, { repo, paths, untracked });
// Hide paths from git (writes .git/info/exclude) without touching disk.
export const exclude = (id: string, repo: string, paths: string[]) =>
  apiPost(`${s(id)}/exclude`, { repo, paths });

export const commit = (id: string, repo: string, message: string) =>
  apiPost<{ sha: string }>(`${s(id)}/commit`, { repo, message });

export const switchBranch = (id: string, repo: string, branch: string) =>
  apiPost(`${s(id)}/branch/switch`, { repo, branch });

// from: start point (branch, tag, sha). Empty = HEAD.
export const createBranch = (
  id: string,
  repo: string,
  branch: string,
  checkout: boolean,
  from = "",
) => apiPost(`${s(id)}/branch/create`, { repo, branch, checkout, from });

export const renameBranch = (id: string, repo: string, branch: string, newName: string) =>
  apiPost(`${s(id)}/branch/rename`, { repo, branch, new_name: newName });

// force mirrors `git branch -D`: git refuses an unmerged branch without it,
// and a squash-merged branch trips that same refusal.
export const deleteBranch = (id: string, repo: string, branch: string, force: boolean) =>
  apiPost(`${s(id)}/branch/delete`, { repo, branch, force });

// push/pull run through a Git CLI connector when one is chosen for the
// repo — that is where the credentials and the branch policy live.
// connector_id is only sent by the picker, on the first push in a repo;
// after that the session remembers it.
export const push = (id: string, repo: string, connectorID?: string) =>
  apiPost<{ output: string; connector_id?: string }>(`${s(id)}/push`, {
    repo,
    connector_id: connectorID ?? "",
  });

// fetch updates remote-tracking refs and prunes dead ones. Same credential
// path as push/pull; changes nothing locally.
export const fetchRemote = (id: string, repo: string, connectorID?: string) =>
  apiPost<{ output: string; connector_id?: string }>(`${s(id)}/fetch`, {
    repo,
    connector_id: connectorID ?? "",
  });

export const pull = (id: string, repo: string, connectorID?: string) =>
  apiPost<{ output: string; connector_id?: string }>(`${s(id)}/pull`, {
    repo,
    connector_id: connectorID ?? "",
  });

export type GitConnector = {
  id: string;
  label: string;
  author?: string;
  /** The connector whose label names the remote's host — a guess, since
      the connector stores a credential, not a host. */
  suggested?: boolean;
};

export type GitConnectorsResponse = {
  repo: string;
  remote_url?: string;
  remote_host?: string;
  selected?: string;
  candidates: GitConnector[];
};

export const getGitConnectors = (id: string, repo: string) =>
  apiGet<GitConnectorsResponse>(`${s(id)}/connectors?repo=${q(repo)}`);

export const setGitConnector = (id: string, repo: string, connectorID: string) =>
  apiPost<{ ok: boolean }>(`${s(id)}/connectors`, { repo, connector_id: connectorID });

export const saveFile = (id: string, repo: string, path: string, content: string) =>
  apiPost(`${s(id)}/file`, { repo, path, content });

// Raw content at a ref (default HEAD) — the "original" side for Monaco's
// diff editor. Empty content means the file didn't exist at that ref.
export const getBlob = (id: string, repo: string, path: string, ref = "HEAD") =>
  apiGet<{ content: string; path: string; ref: string }>(
    `${s(id)}/blob?repo=${q(repo)}&path=${q(path)}&ref=${q(ref)}`,
  );

// Two raw sides for a working-tree change, picked git-correctly by the
// server (staged → HEAD↔index, unstaged → index↔working).
export const getCompare = (
  id: string,
  repo: string,
  path: string,
  staged: boolean,
  untracked: boolean,
) =>
  apiGet<{ original: string; modified: string; path: string }>(
    `${s(id)}/compare?repo=${q(repo)}&path=${q(path)}&staged=${staged ? "1" : "0"}&untracked=${untracked ? "1" : "0"}`,
  );

// refs picks which history to walk: "auto" (current branch + upstream),
// "all", or explicit ref names — the same three the picker offers.
// avatars maps author email -> picture URL, and only contains emails that
// belong to a wick account WITH an avatar. Missing = draw nothing.
export const getLog = (id: string, repo: string, limit = 50, refs: string[] = [], skip = 0) =>
  apiGet<{ commits: LogEntry[]; avatars?: Record<string, string>; has_more?: boolean }>(
    `${s(id)}/log?repo=${q(repo)}&limit=${limit}&skip=${skip}` +
      (refs.length ? `&refs=${q(refs.join(","))}` : ""),
  );

export const getHistoryRefs = (id: string, repo: string) =>
  apiGet<HistoryRefsResponse>(`${s(id)}/refs?repo=${q(repo)}`);

export const getCommit = (id: string, repo: string, sha: string) =>
  apiGet<CommitDetail>(`${s(id)}/commit?repo=${q(repo)}&sha=${q(sha)}`);

export const getCommitDiff = (id: string, repo: string, sha: string, path: string) =>
  apiGet<{ original: string; modified: string; path: string }>(
    `${s(id)}/commit-diff?repo=${q(repo)}&sha=${q(sha)}&path=${q(path)}`,
  );
